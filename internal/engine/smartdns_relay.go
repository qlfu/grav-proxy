package engine

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/qlfu/grav-proxy/internal/happ"
)

const (
	relayQueryTimeout = 3500 * time.Millisecond
	relayProbeTimeout = 3000 * time.Millisecond
	relayCacheTTL     = 90 * time.Second
)

type dnsCacheEntry struct {
	ips     []net.IP
	expires time.Time
}

type tlsProbeFunc func(ctx context.Context, ip, sni string) (time.Duration, error)

// SmartDNSRelay is a local UDP DNS relay that pins the fastest reachable upstream
// endpoint IP and filters SNI-proxy IPs returning dead/blackholed endpoints.
type SmartDNSRelay struct {
	ep     happ.Endpoint
	pc     net.PacketConn
	srv    *dns.Server
	port   int
	closed chan struct{}

	mu             sync.RWMutex
	upstreamIPs    []string
	pinnedUpstream string
	liveIPs        []string
	deadIPs        map[string]struct{}
	cache          map[string]dnsCacheEntry

	dohMu      sync.Mutex
	dohClients map[string]*http.Client

	tlsProbe tlsProbeFunc
}

func StartSmartDNSRelay(ctx context.Context, ep happ.Endpoint) (*SmartDNSRelay, error) {
	return startSmartDNSRelay(ctx, ep, probeTLSHTTP)
}

func startSmartDNSRelay(ctx context.Context, ep happ.Endpoint, probeFn tlsProbeFunc) (*SmartDNSRelay, error) {
	if ep.Kind != happ.KindSmartDNS {
		return nil, fmt.Errorf("expected SmartDNS endpoint, got %s", ep.Kind)
	}
	if ep.IsAuto {
		return nil, fmt.Errorf("smartdns://auto must be resolved before starting relay")
	}
	if probeFn == nil {
		probeFn = probeTLSHTTP
	}

	upstreamIPs, err := resolveUpstreamIPs(ctx, ep)
	if err != nil {
		return nil, err
	}

	r := &SmartDNSRelay{
		ep:          ep,
		closed:      make(chan struct{}),
		upstreamIPs: upstreamIPs,
		deadIPs:     make(map[string]struct{}),
		cache:       make(map[string]dnsCacheEntry),
		dohClients:  make(map[string]*http.Client),
		tlsProbe:    probeFn,
	}

	pinned, _, err := r.raceUpstreamIPs(ctx, "daily-cloudcode-pa.googleapis.com")
	if err != nil {
		r.closeClients()
		return nil, fmt.Errorf("upstream %s unreachable: %w", ep.Server, err)
	}
	r.mu.Lock()
	r.pinnedUpstream = pinned
	r.mu.Unlock()

	if err := r.probeSNIProxyPool(ctx); err != nil {
		r.closeClients()
		return nil, err
	}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		r.closeClients()
		return nil, err
	}
	r.pc = pc
	r.port = pc.LocalAddr().(*net.UDPAddr).Port

	started := make(chan struct{})
	srv := &dns.Server{
		PacketConn:        pc,
		Handler:           dns.HandlerFunc(r.handleDNS),
		NotifyStartedFunc: func() { close(started) },
	}
	r.srv = srv

	errCh := make(chan error, 1)
	go func() {
		if sErr := srv.ActivateAndServe(); sErr != nil {
			select {
			case <-r.closed:
			default:
				errCh <- sErr
			}
		}
	}()

	select {
	case <-started:
		return r, nil
	case sErr := <-errCh:
		_ = r.Close()
		return nil, sErr
	case <-ctx.Done():
		_ = r.Close()
		return nil, ctx.Err()
	}
}

func (r *SmartDNSRelay) Port() int {
	return r.port
}

func (r *SmartDNSRelay) LiveIPs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.liveIPs))
	copy(out, r.liveIPs)
	return out
}

func (r *SmartDNSRelay) DeadIPCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.deadIPs)
}

func (r *SmartDNSRelay) Close() error {
	select {
	case <-r.closed:
		return nil
	default:
		close(r.closed)
	}
	var err error
	if r.srv != nil {
		err = r.srv.Shutdown()
	}
	if r.pc != nil {
		_ = r.pc.Close()
	}
	r.closeClients()
	return err
}

func (r *SmartDNSRelay) closeClients() {
	r.dohMu.Lock()
	defer r.dohMu.Unlock()
	for k, c := range r.dohClients {
		if tr, ok := c.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
		delete(r.dohClients, k)
	}
}

func resolveUpstreamIPs(ctx context.Context, ep happ.Endpoint) ([]string, error) {
	if ep.IsIP {
		if parsed := net.ParseIP(strings.TrimSpace(ep.Server)); parsed != nil && parsed.To4() != nil {
			return []string{parsed.To4().String()}, nil
		}
		return []string{ep.Server}, nil
	}
	lCtx, cancel := context.WithTimeout(ctx, relayQueryTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(lCtx, "ip4", ep.Server)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", ep.Server, err)
	}
	seen := make(map[string]struct{}, len(ips))
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		s := v4.String()
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no IPv4 addresses for %s", ep.Server)
	}
	return out, nil
}

func (r *SmartDNSRelay) raceUpstreamIPs(ctx context.Context, probeDomain string) (string, []net.IP, error) {
	r.mu.RLock()
	ips := append([]string(nil), r.upstreamIPs...)
	r.mu.RUnlock()
	if len(ips) == 0 {
		return "", nil, errors.New("no upstream IPs")
	}

	type raceHit struct {
		ip  string
		ans []net.IP
		err error
	}

	rCtx, cancel := context.WithTimeout(ctx, relayQueryTimeout)
	defer cancel()
	ch := make(chan raceHit, len(ips))
	for _, ip := range ips {
		go func(target string) {
			ans, err := r.queryUpstreamIP(rCtx, target, probeDomain)
			ch <- raceHit{ip: target, ans: ans, err: err}
		}(ip)
	}

	var firstErr error
	for i := 0; i < len(ips); i++ {
		select {
		case res := <-ch:
			if res.err == nil && len(res.ans) > 0 {
				return res.ip, res.ans, nil
			}
			if firstErr == nil && res.err != nil {
				firstErr = res.err
			}
		case <-rCtx.Done():
			if firstErr != nil {
				return "", nil, firstErr
			}
			return "", nil, rCtx.Err()
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("no A records for %s", probeDomain)
	}
	return "", nil, firstErr
}

func (r *SmartDNSRelay) probeSNIProxyPool(ctx context.Context) error {
	probeDomains := []string{
		"daily-cloudcode-pa.googleapis.com",
		"cloudcode-pa.googleapis.com",
		"notebook.google.com",
		"api.anthropic.com",
	}

	r.mu.RLock()
	pinned := r.pinnedUpstream
	r.mu.RUnlock()

	type domAns struct {
		domain string
		ips    []net.IP
	}
	domCh := make(chan domAns, len(probeDomains))
	for _, d := range probeDomains {
		go func(dom string) {
			qCtx, cancel := context.WithTimeout(ctx, relayQueryTimeout)
			defer cancel()
			ips, err := r.queryUpstreamIP(qCtx, pinned, dom)
			if err != nil {
				domCh <- domAns{domain: dom}
				return
			}
			domCh <- domAns{domain: dom, ips: ips}
		}(d)
	}

	// Prefer daily-cloudcode-pa.googleapis.com as SNI probe if returned on multiple domains.
	ipSNI := make(map[string]string)
	for i := 0; i < len(probeDomains); i++ {
		res := <-domCh
		for _, ip := range res.ips {
			v4 := ip.To4()
			if v4 == nil {
				continue
			}
			s := v4.String()
			prev, exists := ipSNI[s]
			if !exists || res.domain == "daily-cloudcode-pa.googleapis.com" {
				ipSNI[s] = res.domain
			} else if prev == "" {
				ipSNI[s] = res.domain
			}
		}
	}

	if len(ipSNI) == 0 {
		return errors.New("SmartDNS returned no IPv4 addresses for probe domains")
	}

	type ipCheck struct {
		ip  string
		lat time.Duration
		ok  bool
	}
	checkCh := make(chan ipCheck, len(ipSNI))
	for ip, sni := range ipSNI {
		go func(targetIP, targetSNI string) {
			pCtx, cancel := context.WithTimeout(ctx, relayProbeTimeout)
			defer cancel()
			lat, err := r.tlsProbe(pCtx, targetIP, targetSNI)
			checkCh <- ipCheck{ip: targetIP, lat: lat, ok: err == nil}
		}(ip, sni)
	}

	var liveHits []ipCheck
	dead := make(map[string]struct{})
	for i := 0; i < len(ipSNI); i++ {
		c := <-checkCh
		if c.ok {
			liveHits = append(liveHits, c)
		} else {
			dead[c.ip] = struct{}{}
		}
	}

	sort.SliceStable(liveHits, func(i, j int) bool {
		return liveHits[i].lat < liveHits[j].lat
	})

	live := make([]string, len(liveHits))
	for i, h := range liveHits {
		live[i] = h.ip
	}

	if len(live) == 0 {
		return fmt.Errorf("no reachable SmartDNS proxy IPs (checked %d)", len(ipSNI))
	}

	r.mu.Lock()
	r.liveIPs = live
	r.deadIPs = dead
	r.mu.Unlock()
	return nil
}

func (r *SmartDNSRelay) filterIPs(raw []net.IP) []net.IP {
	if len(raw) == 0 {
		return nil
	}

	r.mu.RLock()
	live := append([]string(nil), r.liveIPs...)
	dead := r.deadIPs
	rawList := make([]string, 0, len(raw))
	rawSet := make(map[string]struct{}, len(raw))
	for _, ip := range raw {
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		s := v4.String()
		if _, dup := rawSet[s]; !dup {
			rawSet[s] = struct{}{}
			rawList = append(rawList, s)
		}
	}
	var hasLive bool
	var nonDead []net.IP
	for _, s := range rawList {
		if _, isDead := dead[s]; isDead {
			continue
		}
		nonDead = append(nonDead, net.ParseIP(s).To4())
	}

	var matchedLive []net.IP
	for _, ls := range live {
		if _, ok := rawSet[ls]; ok {
			hasLive = true
			matchedLive = append(matchedLive, net.ParseIP(ls).To4())
		}
	}
	r.mu.RUnlock()

	if hasLive {
		return matchedLive
	}
	if len(nonDead) > 0 {
		return nonDead
	}
	if len(live) > 0 {
		out := make([]net.IP, len(live))
		for i, ls := range live {
			out[i] = net.ParseIP(ls).To4()
		}
		return out
	}
	return raw
}

func (r *SmartDNSRelay) handleDNS(w dns.ResponseWriter, req *dns.Msg) {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Authoritative = false
	resp.RecursionAvailable = true

	if len(req.Question) == 0 {
		_ = w.WriteMsg(resp)
		return
	}

	q := req.Question[0]
	if q.Qtype == dns.TypeAAAA {
		_ = w.WriteMsg(resp)
		return
	}
	if q.Qtype != dns.TypeA {
		_ = w.WriteMsg(resp)
		return
	}

	domain := strings.TrimSuffix(strings.ToLower(q.Name), ".")
	if domain == "" {
		_ = w.WriteMsg(resp)
		return
	}

	ips := r.lookupFilteredA(domain)
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{
				Name:   q.Name,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    60,
			},
			A: v4,
		})
	}
	_ = w.WriteMsg(resp)
}

func (r *SmartDNSRelay) lookupFilteredA(domain string) []net.IP {
	now := time.Now()
	r.mu.RLock()
	if ent, ok := r.cache[domain]; ok && now.Before(ent.expires) {
		out := append([]net.IP(nil), ent.ips...)
		r.mu.RUnlock()
		return out
	}
	pinned := r.pinnedUpstream
	r.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), relayQueryTimeout+time.Second)
	defer cancel()

	var raw []net.IP
	var err error
	if pinned != "" {
		raw, err = r.queryUpstreamIP(ctx, pinned, domain)
	}
	if err != nil || len(raw) == 0 {
		newPin, raced, rErr := r.raceUpstreamIPs(ctx, domain)
		if rErr == nil && len(raced) > 0 {
			raw = raced
			r.mu.Lock()
			r.pinnedUpstream = newPin
			r.mu.Unlock()
		}
	}
	if len(raw) == 0 {
		if sysIPs, sErr := net.DefaultResolver.LookupIP(ctx, "ip4", domain); sErr == nil {
			raw = sysIPs
		}
	}
	if len(raw) == 0 {
		return nil
	}

	filtered := r.filterIPs(raw)
	if len(filtered) > 0 {
		r.mu.Lock()
		r.cache[domain] = dnsCacheEntry{
			ips:     append([]net.IP(nil), filtered...),
			expires: time.Now().Add(relayCacheTTL),
		}
		r.mu.Unlock()
	}
	return filtered
}

func (r *SmartDNSRelay) queryUpstreamIP(ctx context.Context, ip, domain string) ([]net.IP, error) {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	msg.RecursionDesired = true

	var resp *dns.Msg
	var err error

	switch r.ep.DNSType {
	case "https":
		resp, err = r.queryDoH(ctx, ip, msg)
	case "tls":
		port := r.ep.Port
		if port <= 0 {
			port = 853
		}
		c := &dns.Client{
			Net:     "tcp-tls",
			Timeout: relayQueryTimeout,
			TLSConfig: &tls.Config{
				ServerName: r.ep.Server,
				MinVersion: tls.VersionTLS12,
			},
		}
		resp, _, err = c.ExchangeContext(ctx, msg, net.JoinHostPort(ip, strconv.Itoa(port)))
	default:
		port := r.ep.Port
		if port <= 0 {
			port = 53
		}
		c := &dns.Client{
			Net:     "udp",
			Timeout: relayQueryTimeout,
		}
		resp, _, err = c.ExchangeContext(ctx, msg, net.JoinHostPort(ip, strconv.Itoa(port)))
	}

	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("empty DNS response")
	}
	if resp.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("DNS rcode %s", dns.RcodeToString[resp.Rcode])
	}

	var ips []net.IP
	for _, rr := range resp.Answer {
		if a, ok := rr.(*dns.A); ok && a.A != nil {
			if v4 := a.A.To4(); v4 != nil {
				ips = append(ips, v4)
			}
		}
	}
	return ips, nil
}

func (r *SmartDNSRelay) dohClientForIP(ip string) *http.Client {
	r.dohMu.Lock()
	defer r.dohMu.Unlock()
	if c, ok := r.dohClients[ip]; ok {
		return c
	}
	port := r.ep.Port
	if port <= 0 {
		port = 443
	}
	targetAddr := net.JoinHostPort(ip, strconv.Itoa(port))
	tr := &http.Transport{
		DialContext: func(dctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: relayQueryTimeout}).DialContext(dctx, "tcp", targetAddr)
		},
		TLSClientConfig: &tls.Config{
			ServerName: r.ep.Server,
			MinVersion: tls.VersionTLS12,
		},
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: relayQueryTimeout,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     30 * time.Second,
	}
	c := &http.Client{
		Transport: tr,
		Timeout:   relayQueryTimeout,
	}
	r.dohClients[ip] = c
	return c
}

func (r *SmartDNSRelay) queryDoH(ctx context.Context, ip string, query *dns.Msg) (*dns.Msg, error) {
	wire, err := query.Pack()
	if err != nil {
		return nil, err
	}
	port := r.ep.Port
	if port <= 0 {
		port = 443
	}
	path := r.ep.Path
	if path == "" {
		path = "/dns-query"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	hostPart := r.ep.Server
	if port != 443 {
		hostPart = net.JoinHostPort(r.ep.Server, strconv.Itoa(port))
	}
	endpointURL := fmt.Sprintf("https://%s%s", hostPart, path)
	client := r.dohClientForIP(ip)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(wire))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Fallback to RFC 8484 GET ?dns= for servers restricting POST.
		b64 := base64.RawURLEncoding.EncodeToString(wire)
		sep := "?"
		if strings.Contains(endpointURL, "?") {
			sep = "&"
		}
		getReq, gErr := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL+sep+"dns="+b64, nil)
		if gErr == nil {
			getReq.Header.Set("Accept", "application/dns-message")
			if gResp, gDoErr := client.Do(getReq); gDoErr == nil {
				gBody, gReadErr := io.ReadAll(io.LimitReader(gResp.Body, 64*1024))
				_ = gResp.Body.Close()
				if gResp.StatusCode == http.StatusOK && gReadErr == nil {
					out := new(dns.Msg)
					if uErr := out.Unpack(gBody); uErr == nil {
						return out, nil
					}
				}
			}
		}
		return nil, fmt.Errorf("DoH HTTP %d", resp.StatusCode)
	}
	if readErr != nil {
		return nil, readErr
	}

	out := new(dns.Msg)
	if err := out.Unpack(body); err != nil {
		return nil, err
	}
	return out, nil
}

func probeTLSHTTP(ctx context.Context, ip, sni string) (time.Duration, error) {
	start := time.Now()
	d := &net.Dialer{Timeout: relayProbeTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
	if err != nil {
		return time.Since(start), err
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(relayProbeTimeout))
	}

	tlsConn := tls.Client(conn, &tls.Config{
		ServerName: sni,
		MinVersion: tls.VersionTLS12,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return time.Since(start), err
	}

	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", sni)
	if _, err := tlsConn.Write([]byte(req)); err != nil {
		return time.Since(start), err
	}

	buf := make([]byte, 16)
	n, err := tlsConn.Read(buf)
	if err != nil && n == 0 {
		return time.Since(start), err
	}
	if !bytes.HasPrefix(buf[:n], []byte("HTTP/")) {
		return time.Since(start), fmt.Errorf("unexpected TLS/HTTP banner: %q", string(buf[:n]))
	}
	return time.Since(start), nil
}
