package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/qlfu/grav-proxy/internal/happ"
)

func TestBuildAndParseSingBoxConfigs(t *testing.T) {
	endpoints := []string{
		"vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?type=grpc&security=reality&pbk=PUBKEY&fp=chrome&sni=google.com&sid=abcd&serviceName=grpc.svc",
		"socks5://127.0.0.1:10808",
		"smartdns://https://eu.geohide.ru/dns-query",
		"smartdns://tls://dns.malw.link:853",
		"smartdns://1.1.1.1",
	}
	for _, raw := range endpoints {
		ep, ok := happ.ParseProxyEndpointURL(raw)
		if !ok {
			ep, ok = happ.ParseSmartDNSURL(raw)
		}
		if !ok {
			t.Fatalf("failed to parse %s", raw)
		}
		data, err := BuildConfigJSON(ep, 28900, "error")
		if err != nil {
			t.Fatalf("BuildConfigJSON(%s): %v", raw, err)
		}
		if _, _, err := ParseSingBoxOptions(data); err != nil {
			t.Fatalf("ParseSingBoxOptions(%s): %v", raw, err)
		}
	}
}

func TestVlessConfigHasIPv4OutboundResolver(t *testing.T) {
	ep, ok := happ.ParseProxyEndpointURL("vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?security=reality&pbk=PUBKEY&fp=chrome&sni=google.com")
	if !ok {
		t.Fatal("ParseProxyEndpointURL failed")
	}
	raw, err := BuildConfigJSON(ep, 28900, "warn")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	dnsMap, _ := m["dns"].(map[string]any)
	if dnsMap == nil || dnsMap["strategy"] != "ipv4_only" {
		t.Fatalf("expected dns.strategy=ipv4_only, got %+v", m["dns"])
	}
	outs, _ := m["outbounds"].([]any)
	if len(outs) == 0 {
		t.Fatal("missing outbounds")
	}
	firstOut, _ := outs[0].(map[string]any)
	dr, _ := firstOut["domain_resolver"].(map[string]any)
	if dr == nil || dr["server"] != "local-dns" || dr["strategy"] != "ipv4_only" {
		t.Fatalf("expected outbound domain_resolver={local-dns, ipv4_only}, got %+v", firstOut["domain_resolver"])
	}
	routeMap, _ := m["route"].(map[string]any)
	if _, hasDefault := routeMap["default_domain_resolver"]; hasDefault {
		t.Fatalf("did not expect route.default_domain_resolver in VLESS config: %+v", routeMap)
	}
}

func TestVlessProbeRankPrefersReachableOverFastError(t *testing.T) {
	deadFast := VlessProbeResult{Reachable: false, LatencyMs: 90}
	cleanSlow := VlessProbeResult{Reachable: true, GoogleClean: true, NotebookOk: true, LatencyMs: 550}
	reachableOnly := VlessProbeResult{Reachable: true, LatencyMs: 420}
	if vlessProbeRank(cleanSlow) <= vlessProbeRank(deadFast) {
		t.Fatalf("clean node rank (%d) must exceed dead node rank (%d)", vlessProbeRank(cleanSlow), vlessProbeRank(deadFast))
	}
	if vlessProbeRank(reachableOnly) <= vlessProbeRank(deadFast) {
		t.Fatalf("reachable node rank (%d) must exceed dead node rank (%d)", vlessProbeRank(reachableOnly), vlessProbeRank(deadFast))
	}
}

func TestGoogleGeoAndSorryMatching(t *testing.T) {
	sample := `<p>Country version:</a> Netherlands</p>`
	m := googleRegionMatch.FindStringSubmatch(sample)
	if len(m) < 2 || m[1] != "Netherlands" {
		t.Fatalf("expected Netherlands from googleRegionMatch, got %v", m)
	}
	if !sorryLocationRe.MatchString("https://www.google.com/sorry/index?continue=https://gemini.google.com/app") {
		t.Fatal("expected sorryLocationRe to match Google /sorry redirect")
	}
	if ctry := "Russia"; !blockedCountryRe.MatchString(ctry) {
		t.Fatal("expected Russia to be blocked")
	}
}

func TestProbeURLReadsLargeBodyBeyond256KB(t *testing.T) {
	pad := bytes.Repeat([]byte("a"), 460*1024)
	payload := append(pad, []byte(`Country version:</a> Netherlands</p>`)...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	res := probeURL(context.Background(), srv.Client(), http.MethodGet, srv.URL, "")
	if res.code != 200 {
		t.Fatalf("expected 200, got %d err=%s", res.code, res.err)
	}
	m := googleRegionMatch.FindStringSubmatch(res.body)
	if len(m) < 2 || m[1] != "Netherlands" {
		t.Fatalf("expected Netherlands at >450KB offset, got %v (bodyLen=%d)", m, len(res.body))
	}
}

func TestLiveInProcessSmartDNSProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live SmartDNS network test in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ep, results, err := ResolveSmartDNSAuto(ctx, "")
	t.Logf("SmartDNS auto: chosen=%s err=%v", ep.Raw, err)
	for _, r := range results {
		t.Logf("  %s score=%d ag=%v nb=%v gem=%v anth=%v ms=%d err=%s", r.Name, r.Score, r.AgOk, r.NotebookOk, r.GeminiOk, r.AnthOk, r.LatencyMs, r.Error)
	}
	if err != nil {
		t.Fatalf("ResolveSmartDNSAuto failed: %v", err)
	}
	if ep.Kind != happ.KindSmartDNS {
		t.Fatalf("expected SmartDNS endpoint, got %+v", ep)
	}
}

func TestSmartDNSRelayFiltersDeadIPs(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mockPort := pc.LocalAddr().(*net.UDPAddr).Port
	started := make(chan struct{})
	srv := &dns.Server{
		PacketConn: pc,
		NotifyStartedFunc: func() {
			close(started)
		},
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			resp := new(dns.Msg)
			resp.SetReply(r)
			if len(r.Question) > 0 && r.Question[0].Qtype == dns.TypeA {
				qname := r.Question[0].Name
				var ips []string
				if qname == "ipinfo.io." {
					ips = []string{"34.117.59.81"}
				} else {
					ips = []string{"198.51.100.1", "203.0.113.10", "198.51.100.2"}
				}
				for _, s := range ips {
					resp.Answer = append(resp.Answer, &dns.A{
						Hdr: dns.RR_Header{Name: qname, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
						A:   net.ParseIP(s).To4(),
					})
				}
			}
			_ = w.WriteMsg(resp)
		}),
	}
	go func() { _ = srv.ActivateAndServe() }()
	defer srv.Shutdown()
	<-started

	ep := happ.Endpoint{
		Kind:    happ.KindSmartDNS,
		Raw:     fmt.Sprintf("smartdns://udp://127.0.0.1:%d", mockPort),
		Name:    "MockDNS",
		Server:  "127.0.0.1",
		Port:    mockPort,
		DNSType: "udp",
		IsIP:    true,
	}

	probeFn := func(_ context.Context, ip, _ string) (time.Duration, error) {
		switch ip {
		case "203.0.113.10":
			return 25 * time.Millisecond, nil
		default:
			return time.Second, errors.New("simulated blocked IP")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	relay, err := startSmartDNSRelay(ctx, ep, probeFn)
	if err != nil {
		t.Fatalf("startSmartDNSRelay failed: %v", err)
	}
	defer relay.Close()

	live := relay.LiveIPs()
	if len(live) != 1 || live[0] != "203.0.113.10" {
		t.Fatalf("expected live=[203.0.113.10], got %v", live)
	}
	if relay.DeadIPCount() != 2 {
		t.Fatalf("expected 2 dead IPs, got %d", relay.DeadIPCount())
	}

	c := &dns.Client{Net: "udp", Timeout: 2 * time.Second}
	q1 := new(dns.Msg)
	q1.SetQuestion("notebook.google.com.", dns.TypeA)
	ans1, _, err := c.Exchange(q1, fmt.Sprintf("127.0.0.1:%d", relay.Port()))
	if err != nil || len(ans1.Answer) != 1 {
		t.Fatalf("expected 1 filtered A record for notebook.google.com, got %v err=%v", ans1, err)
	}
	if gotIP := ans1.Answer[0].(*dns.A).A.String(); gotIP != "203.0.113.10" {
		t.Fatalf("expected filtered IP 203.0.113.10, got %s", gotIP)
	}

	q2 := new(dns.Msg)
	q2.SetQuestion("ipinfo.io.", dns.TypeA)
	ans2, _, err := c.Exchange(q2, fmt.Sprintf("127.0.0.1:%d", relay.Port()))
	if err != nil || len(ans2.Answer) != 1 {
		t.Fatalf("expected 1 passthrough A record for ipinfo.io, got %v err=%v", ans2, err)
	}
	if gotIP := ans2.Answer[0].(*dns.A).A.String(); gotIP != "34.117.59.81" {
		t.Fatalf("expected passthrough IP 34.117.59.81, got %s", gotIP)
	}
}

func TestSmartDNSLiveManagerConnectivity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live SmartDNS manager test in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	ep, _, err := ResolveSmartDNSAuto(ctx, "")
	if err != nil {
		t.Fatalf("ResolveSmartDNSAuto failed: %v", err)
	}

	ports, err := ReserveLoopbackPorts(1)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager()
	if err := mgr.Start(ep, ports[0]); err != nil {
		t.Fatalf("Manager.Start(%s) failed: %v", ep.Raw, err)
	}
	defer mgr.Stop()

	items := TestConnectivity(ctx, fmt.Sprintf("http://127.0.0.1:%d", ports[0]), true)
	byName := make(map[string]ConnectivityCheckItem)
	for _, it := range items {
		t.Logf("  [%v optional=%v] %-24s %s (%d ms)", it.OK, it.Optional, it.Name, it.Detail, it.LatencyMs)
		byName[it.Name] = it
	}
	for _, req := range []string{"Antigravity 2.0", "NotebookLM", "Claude / Anthropic API"} {
		it, ok := byName[req]
		if !ok || !it.OK {
			t.Fatalf("expected %s to succeed in SmartDNS mode, got %+v", req, it)
		}
	}
}
