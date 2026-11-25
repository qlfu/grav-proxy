package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"

	"github.com/qlfu/grav-proxy/internal/happ"
)

func BuildConfigJSON(ep happ.Endpoint, listenPort int, logLevel string) ([]byte, error) {
	return BuildConfigJSONWithLocalDNS(ep, listenPort, 0, logLevel)
}

func BuildConfigJSONWithLocalDNS(ep happ.Endpoint, listenPort int, localDNSPort int, logLevel string) ([]byte, error) {
	if logLevel == "" {
		logLevel = "warn"
	}
	if listenPort <= 0 || listenPort > 65535 {
		listenPort = 28080
	}

	if ep.Kind == happ.KindSmartDNS {
		if ep.IsAuto {
			return nil, fmt.Errorf("smartdns://auto must be resolved before building config")
		}
		var servers []any
		if localDNSPort > 0 {
			servers = []any{
				map[string]any{
					"tag":         "smartdns",
					"type":        "udp",
					"server":      "127.0.0.1",
					"server_port": localDNSPort,
				},
			}
		} else {
			sdns := map[string]any{
				"tag":         "smartdns",
				"type":        ep.DNSType,
				"server":      ep.Server,
				"server_port": ep.Port,
			}
			if ep.DNSType == "https" {
				path := ep.Path
				if path == "" {
					path = "/dns-query"
				}
				sdns["path"] = path
			}
			if !ep.IsIP {
				sdns["domain_resolver"] = "bootstrap"
			}
			servers = []any{
				map[string]any{"tag": "bootstrap", "type": "local"},
				sdns,
			}
		}
		cfg := map[string]any{
			"log": map[string]any{"level": logLevel, "timestamp": true},
			"dns": map[string]any{
				"servers":  servers,
				"final":    "smartdns",
				"strategy": "ipv4_only",
			},
			"inbounds": []any{
				map[string]any{
					"type":        "mixed",
					"tag":         "ag-in",
					"listen":      "127.0.0.1",
					"listen_port": listenPort,
				},
			},
			"outbounds": []any{
				map[string]any{
					"type":            "direct",
					"tag":             "vps-out",
					"domain_resolver": map[string]any{"server": "smartdns", "strategy": "ipv4_only"},
				},
				map[string]any{"type": "direct", "tag": "direct-out"},
			},
			"route": map[string]any{
				"default_domain_resolver": "smartdns",
				"rules": []any{
					map[string]any{"domain_suffix": []string{"localhost", "local", "lan"}, "outbound": "direct-out"},
					map[string]any{"ip_cidr": privateCIDRs(), "outbound": "direct-out"},
				},
				"final": "vps-out",
			},
		}
		return json.MarshalIndent(cfg, "", "  ")
	}

	out := BuildOutboundMap(ep, "vps-out")
	if out == nil {
		return nil, fmt.Errorf("unsupported endpoint kind: %s", ep.Kind)
	}
	out["domain_resolver"] = map[string]any{"server": "local-dns", "strategy": "ipv4_only"}
	cfg := map[string]any{
		"log": map[string]any{"level": logLevel, "timestamp": true},
		"dns": map[string]any{
			"servers": []any{
				map[string]any{"tag": "local-dns", "type": "local"},
			},
			"final":    "local-dns",
			"strategy": "ipv4_only",
		},
		"inbounds": []any{
			map[string]any{
				"type":        "mixed",
				"tag":         "ag-in",
				"listen":      "127.0.0.1",
				"listen_port": listenPort,
			},
		},
		"outbounds": []any{
			out,
			map[string]any{"type": "direct", "tag": "direct-out"},
		},
		"route": map[string]any{
			"rules": []any{
				map[string]any{"domain_suffix": []string{"localhost", "local", "lan"}, "outbound": "direct-out"},
				map[string]any{"ip_cidr": privateCIDRs(), "outbound": "direct-out"},
			},
			"final": "vps-out",
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func BuildOutboundMap(ep happ.Endpoint, tag string) map[string]any {
	if ep.Kind == happ.KindUpstream {
		out := map[string]any{
			"type":        ep.Protocol,
			"tag":         tag,
			"server":      ep.Server,
			"server_port": ep.Port,
		}
		if ep.Protocol == "socks" {
			out["version"] = "5"
		}
		if ep.Username != "" {
			out["username"] = ep.Username
		}
		if ep.Password != "" {
			out["password"] = ep.Password
		}
		return out
	}
	if ep.Kind != happ.KindVLESS {
		return nil
	}

	out := map[string]any{
		"type":        "vless",
		"tag":         tag,
		"server":      ep.Server,
		"server_port": ep.Port,
		"uuid":        ep.UUID,
	}
	if ep.Flow != "" {
		out["flow"] = ep.Flow
	}
	if ep.Security == "reality" || ep.Security == "tls" || ep.PBK != "" {
		sni := ep.SNI
		if sni == "" {
			sni = ep.Server
		}
		fp := ep.FP
		if fp == "" {
			fp = "chrome"
		}
		tlsObj := map[string]any{
			"enabled":     true,
			"server_name": sni,
			"utls": map[string]any{
				"enabled":     true,
				"fingerprint": fp,
			},
		}
		if ep.Security == "reality" || ep.PBK != "" {
			tlsObj["reality"] = map[string]any{
				"enabled":    true,
				"public_key": ep.PBK,
				"short_id":   ep.SID,
			}
		} else if ep.ALPN != "" {
			var alpns []string
			for _, part := range strings.Split(ep.ALPN, ",") {
				if p := strings.TrimSpace(part); p != "" {
					alpns = append(alpns, p)
				}
			}
			if len(alpns) > 0 {
				tlsObj["alpn"] = alpns
			}
		}
		out["tls"] = tlsObj
	}

	switch ep.Transport {
	case "grpc":
		out["transport"] = map[string]any{
			"type":         "grpc",
			"service_name": ep.ServiceName,
		}
	case "ws":
		ws := map[string]any{
			"type": "ws",
			"path": defaultSlash(ep.Path),
		}
		if ep.Host != "" {
			ws["headers"] = map[string]any{"Host": ep.Host}
		}
		out["transport"] = ws
	case "httpupgrade":
		hu := map[string]any{
			"type": "httpupgrade",
			"path": defaultSlash(ep.Path),
		}
		if ep.Host != "" {
			hu["host"] = ep.Host
		}
		out["transport"] = hu
	case "http", "h2":
		h2 := map[string]any{
			"type": "http",
			"path": defaultSlash(ep.Path),
		}
		if ep.Host != "" {
			h2["host"] = []string{ep.Host}
		}
		out["transport"] = h2
	}
	return out
}

func defaultSlash(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func privateCIDRs() []string {
	return []string{
		"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
	}
}

func ParseSingBoxOptions(cfgJSON []byte) (context.Context, option.Options, error) {
	ctx := include.Context(context.Background())
	var opts option.Options
	if err := opts.UnmarshalJSONContext(ctx, cfgJSON); err != nil {
		return nil, opts, err
	}
	return ctx, opts, nil
}

type Manager struct {
	mu       sync.Mutex
	instance *box.Box
	relay    *SmartDNSRelay
	active   happ.Endpoint
	port     int
	lastErr  string
}

func NewManager() *Manager {
	return &Manager{port: 28080}
}

func (m *Manager) Start(ep happ.Endpoint, listenPort int) error {
	if listenPort <= 0 {
		listenPort = 28080
	}

	var newRelay *SmartDNSRelay
	localDNSPort := 0
	if ep.Kind == happ.KindSmartDNS {
		rCtx, rCancel := context.WithTimeout(context.Background(), 12*time.Second)
		relay, err := StartSmartDNSRelay(rCtx, ep)
		rCancel()
		if err != nil {
			m.mu.Lock()
			m.lastErr = err.Error()
			m.mu.Unlock()
			return err
		}
		newRelay = relay
		localDNSPort = relay.Port()
	}

	cfgBytes, err := BuildConfigJSONWithLocalDNS(ep, listenPort, localDNSPort, "warn")
	if err != nil {
		if newRelay != nil {
			_ = newRelay.Close()
		}
		m.mu.Lock()
		m.lastErr = err.Error()
		m.mu.Unlock()
		return err
	}
	ctx, opts, err := ParseSingBoxOptions(cfgBytes)
	if err != nil {
		if newRelay != nil {
			_ = newRelay.Close()
		}
		m.mu.Lock()
		m.lastErr = err.Error()
		m.mu.Unlock()
		return fmt.Errorf("invalid sing-box config: %w", err)
	}
	inst, err := box.New(box.Options{
		Context: ctx,
		Options: opts,
	})
	if err != nil {
		if newRelay != nil {
			_ = newRelay.Close()
		}
		m.mu.Lock()
		m.lastErr = err.Error()
		m.mu.Unlock()
		return fmt.Errorf("sing-box init failed: %w", err)
	}
	if err := inst.Start(); err != nil {
		_ = inst.Close()
		if newRelay != nil {
			_ = newRelay.Close()
		}
		m.mu.Lock()
		m.lastErr = err.Error()
		m.mu.Unlock()
		return fmt.Errorf("sing-box start failed: %w", err)
	}

	m.mu.Lock()
	old := m.instance
	oldRelay := m.relay
	m.instance = inst
	m.relay = newRelay
	m.active = ep
	m.port = listenPort
	m.lastErr = ""
	m.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	if oldRelay != nil {
		_ = oldRelay.Close()
	}
	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	inst := m.instance
	relay := m.relay
	m.instance = nil
	m.relay = nil
	m.mu.Unlock()
	if inst != nil {
		_ = inst.Close()
	}
	if relay != nil {
		_ = relay.Close()
	}
}

func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instance != nil
}

func (m *Manager) Active() (happ.Endpoint, int, bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active, m.port, m.instance != nil, m.lastErr
}

func ReserveLoopbackPorts(count int) ([]int, error) {
	listeners := make([]net.Listener, 0, count)
	ports := make([]int, 0, count)
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	for i := 0; i < count; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, ln)
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	return ports, nil
}

type SmartDNSProbeResult struct {
	Endpoint   happ.Endpoint `json:"endpoint"`
	Name       string        `json:"name"`
	Server     string        `json:"server"`
	URL        string        `json:"url"`
	Score      int           `json:"score"`
	AgOk       bool          `json:"agOk"`
	NotebookOk bool          `json:"notebookOk"`
	GeminiOk   bool          `json:"geminiOk"`
	AnthOk     bool          `json:"anthOk"`
	LatencyMs  int64         `json:"latencyMs"`
	Error      string        `json:"error,omitempty"`
}

func ProbeSmartDNS(ctx context.Context, candidates []happ.Endpoint) ([]SmartDNSProbeResult, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no SmartDNS candidates supplied")
	}

	relays := make([]*SmartDNSRelay, len(candidates))
	relayErrs := make([]error, len(candidates))
	var rWg sync.WaitGroup
	for i, c := range candidates {
		rWg.Add(1)
		go func(idx int, ep happ.Endpoint) {
			defer rWg.Done()
			rCtx, cancel := context.WithTimeout(ctx, 9*time.Second)
			defer cancel()
			relays[idx], relayErrs[idx] = StartSmartDNSRelay(rCtx, ep)
		}(i, c)
	}
	rWg.Wait()

	defer func() {
		for _, r := range relays {
			if r != nil {
				_ = r.Close()
			}
		}
	}()

	results := make([]SmartDNSProbeResult, len(candidates))
	var liveIndices []int
	for i, c := range candidates {
		if relays[i] != nil {
			liveIndices = append(liveIndices, i)
			continue
		}
		errMsg := "unreachable"
		if relayErrs[i] != nil {
			errMsg = relayErrs[i].Error()
		}
		results[i] = SmartDNSProbeResult{
			Endpoint: c,
			Name:     c.Name,
			Server:   c.Server,
			URL:      c.Raw,
			Error:    errMsg,
		}
	}
	if len(liveIndices) == 0 {
		return results, nil
	}

	ports, err := ReserveLoopbackPorts(len(liveIndices))
	if err != nil {
		return nil, err
	}

	dnsServers := []any{map[string]any{"tag": "bootstrap", "type": "local"}}
	inbounds := make([]any, 0, len(liveIndices))
	outbounds := make([]any, 0, len(liveIndices)+1)
	rules := make([]any, 0, len(liveIndices))

	for slot, idx := range liveIndices {
		dTag := fmt.Sprintf("sdns-%d", idx)
		inTag := fmt.Sprintf("in-%d", idx)
		outTag := fmt.Sprintf("out-%d", idx)
		dnsServers = append(dnsServers, map[string]any{
			"tag":         dTag,
			"type":        "udp",
			"server":      "127.0.0.1",
			"server_port": relays[idx].Port(),
		})
		inbounds = append(inbounds, map[string]any{
			"type": "mixed", "tag": inTag, "listen": "127.0.0.1", "listen_port": ports[slot],
		})
		outbounds = append(outbounds, map[string]any{
			"type":            "direct",
			"tag":             outTag,
			"domain_resolver": map[string]any{"server": dTag, "strategy": "ipv4_only"},
		})
		rules = append(rules, map[string]any{
			"inbound": []string{inTag}, "outbound": outTag,
		})
	}
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct-out"})

	cfgMap := map[string]any{
		"log":       map[string]any{"level": "error"},
		"dns":       map[string]any{"servers": dnsServers, "final": "bootstrap", "strategy": "ipv4_only"},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     map[string]any{"default_domain_resolver": "bootstrap", "rules": rules, "final": "direct-out"},
	}
	cfgBytes, err := json.Marshal(cfgMap)
	if err != nil {
		return nil, err
	}
	sCtx, opts, err := ParseSingBoxOptions(cfgBytes)
	if err != nil {
		return nil, err
	}
	inst, err := box.New(box.Options{Context: sCtx, Options: opts})
	if err != nil {
		return nil, err
	}
	if err := inst.Start(); err != nil {
		_ = inst.Close()
		return nil, err
	}
	defer inst.Close()

	if err := waitPortsReady(ports, 3*time.Second); err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	for slot, idx := range liveIndices {
		wg.Add(1)
		go func(resIdx int, ep happ.Endpoint, port int) {
			defer wg.Done()
			results[resIdx] = probeOneSmartDNS(ctx, ep, port)
		}(idx, candidates[idx], ports[slot])
	}
	wg.Wait()
	return results, nil
}

func probeOneSmartDNS(ctx context.Context, ep happ.Endpoint, port int) SmartDNSProbeResult {
	pURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pURL), DisableKeepAlives: true},
		Timeout:   7 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	nb := probeURL(ctx, client, http.MethodGet, "https://notebook.google.com/", "")
	gem := probeURL(ctx, client, http.MethodGet, "https://gemini.google.com/", "")
	ag := probeURL(ctx, client, http.MethodGet, "https://daily-cloudcode-pa.googleapis.com/", "")
	ant := probeURL(ctx, client, http.MethodPost, "https://api.anthropic.com/v1/messages", "{}")

	nbOk := nb.code == 200
	gemOk := gem.code == 200 && !sorryLocationRe.MatchString(gem.loc)
	agOk := ag.code == 200 || ag.code == 401 || ag.code == 404
	anthOk := ant.code == 401 && (strings.Contains(ant.body, "x-api-key") || strings.Contains(ant.body, "authentication_error"))

	score := 0
	if nbOk && gemOk && agOk && anthOk {
		score = 4
	} else if nbOk && agOk && anthOk {
		score = 3
	} else if (nbOk && agOk) || (agOk && anthOk) {
		score = 2
	} else if agOk {
		score = 1
	}

	avgMs := (nb.ms + ag.ms + ant.ms) / 3
	errStr := ""
	if score == 0 {
		for _, r := range []httpProbeOut{ag, nb, ant} {
			if r.err != "" {
				errStr = r.err
				break
			}
		}
	}
	return SmartDNSProbeResult{
		Endpoint:   ep,
		Name:       ep.Name,
		Server:     ep.Server,
		URL:        ep.Raw,
		Score:      score,
		AgOk:       agOk,
		NotebookOk: nbOk,
		GeminiOk:   gemOk,
		AnthOk:     anthOk,
		LatencyMs:  avgMs,
		Error:      errStr,
	}
}

func ResolveSmartDNSAuto(ctx context.Context, excludeURL string) (happ.Endpoint, []SmartDNSProbeResult, error) {
	presets := happ.SmartDNSPresets()
	cands := make([]happ.Endpoint, 0, len(presets))
	for _, pr := range presets {
		if excludeURL != "" && strings.EqualFold(pr.URL, excludeURL) {
			continue
		}
		if ep, ok := happ.ParseSmartDNSURL(pr.URL); ok {
			cands = append(cands, ep)
		}
	}
	if len(cands) == 0 {
		for _, pr := range presets {
			if ep, ok := happ.ParseSmartDNSURL(pr.URL); ok {
				cands = append(cands, ep)
			}
		}
	}
	results, err := ProbeSmartDNS(ctx, cands)
	if err != nil {
		return happ.Endpoint{}, nil, err
	}
	var working []SmartDNSProbeResult
	for _, r := range results {
		if r.Score >= 2 {
			working = append(working, r)
		}
	}
	if len(working) == 0 {
		for _, r := range results {
			if r.Score >= 1 {
				working = append(working, r)
			}
		}
	}
	sort.SliceStable(working, func(i, j int) bool {
		if working[i].Score != working[j].Score {
			return working[i].Score > working[j].Score
		}
		return working[i].LatencyMs < working[j].LatencyMs
	})
	if len(working) == 0 {
		var parts []string
		for _, r := range results {
			parts = append(parts, fmt.Sprintf("%s(AG=%v/NB=%v/Ant=%v)", r.Name, r.AgOk, r.NotebookOk, r.AnthOk))
		}
		return happ.Endpoint{}, results, fmt.Errorf("ни один из встроенных SmartDNS не отвечает (%s)", strings.Join(parts, ", "))
	}
	return working[0].Endpoint, results, nil
}

type VlessProbeResult struct {
	Endpoint    happ.Endpoint `json:"endpoint"`
	Reachable   bool          `json:"reachable"`
	GoogleClean bool          `json:"googleClean"`
	GeminiClean bool          `json:"geminiClean"`
	NotebookOk  bool          `json:"notebookOk"`
	Country     string        `json:"country"`
	ExternalIP  string        `json:"externalIp"`
	LatencyMs   int64         `json:"latencyMs"`
	Diagnostics string        `json:"diagnostics,omitempty"`
}

var (
	sorryLocationRe   = regexp.MustCompile(`(?i)/sorry|unsupported`)
	blockedCountryRe  = regexp.MustCompile(`(?i)^(Russia|Russian Federation|Belarus|China|Hong Kong|Macau|Iran|North Korea|Syria|Cuba|Crimea)$`)
	googleRegionMatch = regexp.MustCompile(`(?i)Country version:</a>\s*([^<]+)</p>`)
	unsupportedGoogle = regexp.MustCompile(`(?i)region is not supported|not available in your country`)
)

func vlessProbeRank(r VlessProbeResult) int {
	if r.GoogleClean && r.GeminiClean && r.NotebookOk {
		return 5
	}
	if r.GeminiClean && r.NotebookOk {
		return 4
	}
	if r.GoogleClean && r.NotebookOk {
		return 3
	}
	if r.GoogleClean {
		return 2
	}
	if r.Reachable {
		return 1
	}
	return 0
}

func ProbeVlessNodes(ctx context.Context, nodes []happ.Endpoint) ([]VlessProbeResult, error) {
	usable := make([]happ.Endpoint, 0, len(nodes))
	for _, n := range nodes {
		if happ.IsUsableNode(n) {
			usable = append(usable, n)
		}
	}
	if len(usable) == 0 {
		return nil, fmt.Errorf("нет пригодных VLESS-узлов (отфильтрованы FakeIP / заглушки)")
	}
	const batchSize = 20
	var all []VlessProbeResult
	for start := 0; start < len(usable); start += batchSize {
		end := start + batchSize
		if end > len(usable) {
			end = len(usable)
		}
		part, err := probeVlessBatch(ctx, usable[start:end])
		if err != nil {
			return all, err
		}
		all = append(all, part...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		ri, rj := vlessProbeRank(all[i]), vlessProbeRank(all[j])
		if ri != rj {
			return ri > rj
		}
		return all[i].LatencyMs < all[j].LatencyMs
	})
	return all, nil
}

func probeVlessBatch(ctx context.Context, nodes []happ.Endpoint) ([]VlessProbeResult, error) {
	ports, err := ReserveLoopbackPorts(len(nodes))
	if err != nil {
		return nil, err
	}
	inbounds := make([]any, 0, len(nodes))
	outbounds := make([]any, 0, len(nodes)+1)
	rules := make([]any, 0, len(nodes))
	for i, n := range nodes {
		inTag := fmt.Sprintf("in-%d", i)
		outTag := fmt.Sprintf("out-%d", i)
		inbounds = append(inbounds, map[string]any{"type": "mixed", "tag": inTag, "listen": "127.0.0.1", "listen_port": ports[i]})
		outMap := BuildOutboundMap(n, outTag)
		if outMap != nil {
			outMap["domain_resolver"] = map[string]any{"server": "local-dns", "strategy": "ipv4_only"}
		}
		outbounds = append(outbounds, outMap)
		rules = append(rules, map[string]any{"inbound": []string{inTag}, "outbound": outTag})
	}
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct-out"})
	cfgBytes, err := json.Marshal(map[string]any{
		"log": map[string]any{"level": "error"},
		"dns": map[string]any{
			"servers":  []any{map[string]any{"tag": "local-dns", "type": "local"}},
			"final":    "local-dns",
			"strategy": "ipv4_only",
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     map[string]any{"rules": rules, "final": "direct-out"},
	})
	if err != nil {
		return nil, err
	}
	sCtx, opts, err := ParseSingBoxOptions(cfgBytes)
	if err != nil {
		return nil, err
	}
	inst, err := box.New(box.Options{Context: sCtx, Options: opts})
	if err != nil {
		return nil, err
	}
	if err := inst.Start(); err != nil {
		_ = inst.Close()
		return nil, err
	}
	defer inst.Close()
	if err := waitPortsReady(ports, 3*time.Second); err != nil {
		return nil, err
	}

	res := make([]VlessProbeResult, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func(idx int, ep happ.Endpoint, port int) {
			defer wg.Done()
			res[idx] = probeSingleVlessNode(ctx, ep, port)
		}(i, n, ports[i])
	}
	wg.Wait()
	return res, nil
}

func probeSingleVlessNode(ctx context.Context, ep happ.Endpoint, port int) VlessProbeResult {
	pURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pURL), DisableKeepAlives: true},
		Timeout:   8 * time.Second,
	}
	noRedirClient := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pURL), DisableKeepAlives: true},
		Timeout:   6 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	geo := probeURL(ctx, client, http.MethodGet, "https://policies.google.com/terms?hl=en", "")
	country := ""
	if m := googleRegionMatch.FindStringSubmatch(geo.body); len(m) > 1 {
		country = strings.TrimSpace(m[1])
	}

	clean := geo.code == 200 && country != "" && country != "?" &&
		!blockedCountryRe.MatchString(country) &&
		!unsupportedGoogle.MatchString(geo.body) &&
		!sorryLocationRe.MatchString(geo.loc)

	var nbOk, gemClean bool
	shouldProbeServices := geo.code == 200 &&
		!blockedCountryRe.MatchString(country) &&
		!unsupportedGoogle.MatchString(geo.body) &&
		!sorryLocationRe.MatchString(geo.loc)
	if shouldProbeServices {
		var pWg sync.WaitGroup
		var nb, gem httpProbeOut
		pWg.Add(2)
		go func() {
			defer pWg.Done()
			nb = probeURL(ctx, noRedirClient, http.MethodGet, "https://notebook.google.com/", "")
		}()
		go func() {
			defer pWg.Done()
			gem = probeURL(ctx, noRedirClient, http.MethodGet, "https://gemini.google.com/app", "")
		}()
		pWg.Wait()
		nbOk = nb.code == 200
		gemClean = (gem.code == 200 || gem.code == 301 || gem.code == 302 || gem.code == 307 || gem.code == 308) &&
			!sorryLocationRe.MatchString(gem.loc) &&
			!sorryLocationRe.MatchString(gem.body)
	}

	diag := geo.err
	if diag == "" && !clean {
		diag = fmt.Sprintf("HTTP %d / country=%s", geo.code, country)
	}
	return VlessProbeResult{
		Endpoint:    ep,
		Reachable:   geo.code > 0,
		GoogleClean: clean,
		GeminiClean: gemClean,
		NotebookOk:  nbOk,
		Country:     country,
		LatencyMs:   geo.ms,
		Diagnostics: diag,
	}
}

type ConnectivityCheckItem struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"statusCode"`
	Detail     string `json:"detail"`
	LatencyMs  int64  `json:"latencyMs"`
	Optional   bool   `json:"optional"`
}

func TestConnectivity(ctx context.Context, proxyAddr string, isSmartDNS bool) []ConnectivityCheckItem {
	pURL, _ := url.Parse(proxyAddr)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pURL), DisableKeepAlives: true},
		Timeout:   8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	agURL := "https://cloudcode-pa.googleapis.com"
	if isSmartDNS {
		agURL = "https://daily-cloudcode-pa.googleapis.com"
	}

	type target struct {
		name     string
		url      string
		kind     string
		expect   []int
		optional bool
	}
	targets := []target{
		{name: "Внешний IP (ipinfo.io)", url: "https://ipinfo.io/json", kind: "ip", expect: []int{200}, optional: isSmartDNS},
		{name: "Google GeoIP", url: "https://policies.google.com/terms?hl=en", kind: "geo", expect: []int{200}, optional: isSmartDNS},
		{name: "Antigravity 2.0", url: agURL, kind: "http", expect: []int{200, 401, 404}},
		{name: "v0.app", url: "https://v0.app", kind: "http", expect: []int{200, 301, 302, 307, 308}, optional: isSmartDNS},
		{name: "Gemini", url: "https://gemini.google.com/app", kind: "http", expect: []int{200, 301, 302, 307, 308}, optional: isSmartDNS},
		{name: "NotebookLM", url: "https://notebook.google.com/", kind: "http", expect: []int{200}},
		{name: "OpenCode", url: "https://opencode.ai", kind: "http", expect: []int{200, 301, 302, 307, 308}, optional: isSmartDNS},
		{name: "OpenRouter", url: "https://openrouter.ai/api/v1/models", kind: "http", expect: []int{200}, optional: isSmartDNS},
		{name: "Claude / Anthropic API", url: "https://api.anthropic.com", kind: "http", expect: []int{200, 401, 403, 404}},
	}

	items := make([]ConnectivityCheckItem, len(targets))
	var wg sync.WaitGroup
	for i, tg := range targets {
		wg.Add(1)
		go func(idx int, t target) {
			defer wg.Done()
			r := probeURL(ctx, client, http.MethodGet, t.url, "")
			ok := containsInt(t.expect, r.code)
			detail := fmt.Sprintf("HTTP %d", r.code)
			if r.code == 0 {
				detail = r.err
			} else if t.kind == "ip" {
				ipStr, ctry := parseIPInfoJSON(r.body)
				if r.code == 200 && ipStr != "" {
					if ctry != "" {
						detail = fmt.Sprintf("%s (%s)", ipStr, ctry)
					} else {
						detail = ipStr
					}
				} else {
					fb := probeURL(ctx, client, http.MethodGet, "https://api.ipify.org?format=json", "")
					fbIP, _ := parseIPInfoJSON(fb.body)
					if fb.code == 200 && fbIP != "" {
						ok = true
						detail = fmt.Sprintf("%s (ipify)", fbIP)
					} else {
						ok = false
						detail = fmt.Sprintf("ipinfo HTTP %d", r.code)
					}
				}
			} else if t.kind == "geo" {
				if unsupportedGoogle.MatchString(r.body) {
					ok = false
					detail = "region unsupported"
				} else if ok {
					detail = "region allowed"
				}
			} else if t.name == "Gemini" && ok && sorryLocationRe.MatchString(r.loc) {
				ok = false
				if isSmartDNS {
					detail = fmt.Sprintf("HTTP %d (Google /sorry — для Gemini нужен VLESS)", r.code)
				} else {
					detail = fmt.Sprintf("HTTP %d (Google /sorry — смените узел)", r.code)
				}
			}
			items[idx] = ConnectivityCheckItem{
				Name:       t.name,
				OK:         ok,
				StatusCode: r.code,
				Detail:     detail,
				LatencyMs:  r.ms,
				Optional:   t.optional,
			}
		}(i, tg)
	}
	wg.Wait()
	return items
}

type httpProbeOut struct {
	code int
	loc  string
	body string
	ms   int64
	err  string
}

func probeURL(ctx context.Context, client *http.Client, method, targetURL, body string) httpProbeOut {
	start := time.Now()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return httpProbeOut{ms: time.Since(start).Milliseconds(), err: err.Error()}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return httpProbeOut{ms: time.Since(start).Milliseconds(), err: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	return httpProbeOut{
		code: resp.StatusCode,
		loc:  resp.Header.Get("Location"),
		body: string(raw),
		ms:   time.Since(start).Milliseconds(),
	}
}

func waitPortsReady(ports []int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	pending := append([]int(nil), ports...)
	for len(pending) > 0 {
		var next []int
		for _, pt := range pending {
			c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(pt)), 100*time.Millisecond)
			if err != nil {
				next = append(next, pt)
				continue
			}
			_ = c.Close()
		}
		pending = next
		if len(pending) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %d probe ports", len(pending))
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func strAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func parseIPInfoJSON(body string) (ip, country string) {
	if strings.TrimSpace(body) == "" {
		return "", ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(body), &m) != nil {
		return "", ""
	}
	return strings.TrimSpace(strAny(m["ip"])), strings.TrimSpace(strAny(m["country"]))
}

// Keep os imported cleanly only if needed later; ensure zero unused import issues.
var _ = os.PathSeparator
