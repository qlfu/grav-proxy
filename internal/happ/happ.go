package happ

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

type Kind string

const (
	KindVLESS    Kind = "vless"
	KindUpstream Kind = "upstream"
	KindSmartDNS Kind = "smartdns"
)

type Endpoint struct {
	Kind        Kind   `json:"kind"`
	Raw         string `json:"raw"`
	Name        string `json:"name"`
	Server      string `json:"server"`
	Port        int    `json:"port"`
	UUID        string `json:"uuid,omitempty"`
	Security    string `json:"security,omitempty"`
	SNI         string `json:"sni,omitempty"`
	FP          string `json:"fp,omitempty"`
	PBK         string `json:"pbk,omitempty"`
	SID         string `json:"sid,omitempty"`
	Transport   string `json:"transport,omitempty"`
	ServiceName string `json:"serviceName,omitempty"`
	Path        string `json:"path,omitempty"`
	Host        string `json:"host,omitempty"`
	Flow        string `json:"flow,omitempty"`
	ALPN        string `json:"alpn,omitempty"`
	Protocol    string `json:"protocol,omitempty"` // "socks" | "http"
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	IsAuto      bool   `json:"isAuto,omitempty"`
	DNSType     string `json:"dnsType,omitempty"` // "https" | "tls" | "udp"
	IsIP        bool   `json:"isIp,omitempty"`
}

func (e Endpoint) Summary() string {
	switch e.Kind {
	case KindSmartDNS:
		if e.IsAuto {
			return "SmartDNS (auto)"
		}
		return fmt.Sprintf("SmartDNS: %s (%s)", e.Server, strings.ToUpper(e.DNSType))
	case KindUpstream:
		proto := e.Protocol
		if proto == "socks" {
			proto = "socks5"
		}
		return fmt.Sprintf("%s:%d (%s proxy)", e.Server, e.Port, proto)
	default:
		sec := e.Security
		if sec == "" {
			sec = "none"
		}
		tr := e.Transport
		if tr == "" {
			tr = "tcp"
		}
		return fmt.Sprintf("%s:%d (%s/%s)", e.Server, e.Port, sec, tr)
	}
}

type SmartDNSPreset struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

func SmartDNSPresets() []SmartDNSPreset {
	return []SmartDNSPreset{
		{Key: "dns-ai", Name: "DNS-AI", URL: "smartdns://https://dns.dns-ai.ru/dns-query"},
		{Key: "geohide-eu", Name: "GeoHide EU", URL: "smartdns://https://eu.geohide.ru/dns-query"},
		{Key: "geohide-us", Name: "GeoHide US", URL: "smartdns://https://us.geohide.ru/dns-query"},
		{Key: "malw", Name: "MalwDNS", URL: "smartdns://https://dns.malw.link/dns-query"},
		{Key: "commonnet", Name: "CommonNet", URL: "smartdns://https://dns.commonnet.work/dns-query"},
	}
}

func ParseVlessURL(raw string) (Endpoint, bool) {
	clean := cleanEndpointRaw(raw)
	if len(clean) < 8 || !strings.HasPrefix(strings.ToLower(clean), "vless://") {
		return Endpoint{}, false
	}
	u, err := url.Parse(clean)
	if err != nil {
		return Endpoint{}, false
	}
	uuid := ""
	if u.User != nil {
		uuid = u.User.Username()
	}
	server := u.Hostname()
	port := 443
	if ps := u.Port(); ps != "" {
		p, err := strconv.Atoi(ps)
		if err != nil || p < 1 || p > 65535 {
			return Endpoint{}, false
		}
		port = p
	}
	if strings.TrimSpace(uuid) == "" || strings.TrimSpace(server) == "" {
		return Endpoint{}, false
	}
	if strings.HasPrefix(uuid, "${") || strings.HasPrefix(server, "${") {
		return Endpoint{}, false
	}

	q := firstQueryValues(u.RawQuery)
	sec := strings.ToLower(q["security"])
	pbk := q["pbk"]
	if sec == "" && pbk != "" {
		sec = "reality"
	}
	if sec == "" {
		sec = "none"
	}
	tr := strings.ToLower(q["type"])
	if tr == "" {
		tr = "tcp"
	}

	rawNoFrag := clean
	if idx := strings.IndexByte(clean, '#'); idx >= 0 {
		rawNoFrag = clean[:idx]
	}

	name := u.Fragment
	if unesc, err := url.PathUnescape(name); err == nil && unesc != "" {
		name = unesc
	}

	return Endpoint{
		Kind:        KindVLESS,
		Raw:         rawNoFrag,
		Name:        strings.TrimSpace(name),
		Server:      server,
		Port:        port,
		UUID:        uuid,
		Security:    sec,
		SNI:         q["sni"],
		FP:          q["fp"],
		PBK:         pbk,
		SID:         q["sid"],
		Transport:   tr,
		ServiceName: q["servicename"],
		Path:        q["path"],
		Host:        q["host"],
		Flow:        q["flow"],
		ALPN:        q["alpn"],
	}, true
}

func ParseUpstreamProxyURL(raw string) (Endpoint, bool) {
	clean := cleanEndpointRaw(raw)
	low := strings.ToLower(clean)
	if !strings.HasPrefix(low, "socks5://") && !strings.HasPrefix(low, "socks://") && !strings.HasPrefix(low, "http://") {
		return Endpoint{}, false
	}
	u, err := url.Parse(clean)
	if err != nil {
		return Endpoint{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.Trim(u.Hostname(), "[]")
	if host == "" {
		return Endpoint{}, false
	}
	portStr := u.Port()
	port := 0
	if portStr != "" {
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return Endpoint{}, false
		}
	} else if scheme == "http" {
		port = 80
	}
	if port < 1 || port > 65535 {
		return Endpoint{}, false
	}

	ip := net.ParseIP(host)
	isLoop := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if scheme == "http" {
		if u.Path != "" && u.Path != "/" {
			return Endpoint{}, false
		}
		if u.RawQuery != "" {
			return Endpoint{}, false
		}
		hasExplicitNon80 := portStr != "" && port != 80
		if !isLoop && ip == nil && !hasExplicitNon80 && u.User == nil {
			return Endpoint{}, false
		}
	}
	if isLoop && (port == 28080 || port == 28081) {
		return Endpoint{}, false
	}

	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}

	normScheme := "socks5"
	proto := "socks"
	if scheme == "http" {
		normScheme = "http"
		proto = "http"
	}
	hostURI := host
	if strings.Contains(host, ":") {
		hostURI = "[" + host + "]"
	}
	authPart := ""
	if user != "" || pass != "" {
		authPart = url.QueryEscape(user) + ":" + url.QueryEscape(pass) + "@"
	}
	normRaw := fmt.Sprintf("%s://%s%s:%d", normScheme, authPart, hostURI, port)
	return Endpoint{
		Kind:     KindUpstream,
		Raw:      normRaw,
		Name:     normScheme + " proxy",
		Server:   host,
		Port:     port,
		Protocol: proto,
		Security: "proxy",
		Username: user,
		Password: pass,
		IsIP:     ip != nil,
	}, true
}

func ParseSmartDNSURL(raw string) (Endpoint, bool) {
	clean := cleanEndpointRaw(raw)
	if clean == "" {
		return Endpoint{}, false
	}
	if strings.EqualFold(clean, "smartdns://auto") || strings.EqualFold(clean, "auto") {
		return Endpoint{
			Kind:     KindSmartDNS,
			Raw:      "smartdns://auto",
			Name:     "Автоподбор SmartDNS",
			Server:   "auto",
			Port:     443,
			DNSType:  "https",
			Path:     "/dns-query",
			Security: "doh",
			IsAuto:   true,
		}, true
	}

	inner := clean
	if len(inner) > len("smartdns://") && strings.EqualFold(inner[:len("smartdns://")], "smartdns://") {
		inner = strings.TrimSpace(inner[len("smartdns://"):])
	}
	if inner == "" {
		return Endpoint{}, false
	}

	var dnsType, server, path, normRaw, sec string
	var port int

	lowInner := strings.ToLower(inner)
	switch {
	case strings.HasPrefix(lowInner, "https://"):
		u, err := url.Parse(inner)
		if err != nil || strings.TrimSpace(u.Hostname()) == "" {
			return Endpoint{}, false
		}
		server = strings.Trim(u.Hostname(), "[]")
		port = 443
		if ps := u.Port(); ps != "" {
			p, err := strconv.Atoi(ps)
			if err != nil || p < 1 || p > 65535 {
				return Endpoint{}, false
			}
			port = p
		}
		path = u.EscapedPath()
		if path == "" || path == "/" {
			path = "/dns-query"
		}
		if u.RawQuery != "" {
			path += "?" + u.RawQuery
		}
		dnsType = "https"
		sec = "doh"
		hostURI := server
		if strings.Contains(server, ":") {
			hostURI = "[" + server + "]"
		}
		portPart := ""
		if port != 443 {
			portPart = fmt.Sprintf(":%d", port)
		}
		normRaw = fmt.Sprintf("smartdns://https://%s%s%s", hostURI, portPart, path)

	case strings.HasPrefix(lowInner, "tls://"):
		u, err := url.Parse("http://" + strings.TrimSpace(inner[len("tls://"):]))
		if err != nil || strings.TrimSpace(u.Hostname()) == "" {
			return Endpoint{}, false
		}
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return Endpoint{}, false
		}
		server = strings.Trim(u.Hostname(), "[]")
		port = 853
		if ps := u.Port(); ps != "" {
			p, err := strconv.Atoi(ps)
			if err != nil || p < 1 || p > 65535 {
				return Endpoint{}, false
			}
			port = p
		}
		dnsType = "tls"
		sec = "dot"
		hostURI := server
		if strings.Contains(server, ":") {
			hostURI = "[" + server + "]"
		}
		normRaw = fmt.Sprintf("smartdns://tls://%s:%d", hostURI, port)

	default:
		rest := inner
		hadUDP := false
		if strings.HasPrefix(lowInner, "udp://") {
			hadUDP = true
			rest = strings.TrimSpace(rest[len("udp://"):])
		}
		if strings.Contains(rest, "://") {
			return Endpoint{}, false
		}
		u, err := url.Parse("http://" + rest)
		if err != nil || strings.TrimSpace(u.Hostname()) == "" {
			return Endpoint{}, false
		}
		if u.Path != "" && u.Path != "/" {
			return Endpoint{}, false
		}
		server = strings.Trim(u.Hostname(), "[]")
		if !hadUDP && net.ParseIP(server) == nil {
			return Endpoint{}, false
		}
		port = 53
		if ps := u.Port(); ps != "" {
			p, err := strconv.Atoi(ps)
			if err != nil || p < 1 || p > 65535 {
				return Endpoint{}, false
			}
			port = p
		}
		dnsType = "udp"
		sec = "udp"
		hostURI := server
		if strings.Contains(server, ":") {
			hostURI = "[" + server + "]"
		}
		normRaw = fmt.Sprintf("smartdns://udp://%s:%d", hostURI, port)
	}

	ip := net.ParseIP(server)
	if strings.EqualFold(server, "localhost") || (ip != nil && (ip.IsLoopback() || ip.IsUnspecified())) {
		return Endpoint{}, false
	}

	name := server
	for _, pr := range SmartDNSPresets() {
		if strings.EqualFold(pr.URL, normRaw) {
			name = pr.Name
			break
		}
	}

	return Endpoint{
		Kind:     KindSmartDNS,
		Raw:      normRaw,
		Name:     name,
		Server:   server,
		Port:     port,
		DNSType:  dnsType,
		Path:     path,
		Security: sec,
		IsIP:     ip != nil,
	}, true
}

func ParseProxyEndpointURL(raw string) (Endpoint, bool) {
	if ep, ok := ParseVlessURL(raw); ok {
		return ep, true
	}
	if ep, ok := ParseUpstreamProxyURL(raw); ok {
		return ep, true
	}
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(trimmed, "smartdns://") || strings.HasPrefix(trimmed, "tls://") {
		return ParseSmartDNSURL(raw)
	}
	return Endpoint{}, false
}

var (
	vlessRe      = regexp.MustCompile("(?i)vless://[^\\s\"'<>\\\\`)\\]]+")
	httpURLRe    = regexp.MustCompile("(?i)https?://[^\\s\"'<>\\\\`)\\]]+")
	fakeStubRe   = regexp.MustCompile(`(?i)только\s+tg|tg\s+бот|истек|expired|лимит|limit|используйте\s+happ|use\s+happ`)
	happNameRe    = regexp.MustCompile(`(?i)(^|[^a-z])happ`)
	happCryptoRe  = regexp.MustCompile(`(?i)happ://crypto`)
)

func IsFakeIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 198 && (v4[1] == 18 || v4[1] == 19)
}

func IsUsableServer(server string) bool {
	s := strings.TrimSpace(server)
	if s == "" || strings.EqualFold(s, "localhost") {
		return false
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback() && !ip.IsUnspecified() && !IsFakeIP(ip)
}

func IsUsableNode(ep Endpoint) bool {
	if !IsUsableServer(ep.Server) {
		return false
	}
	if ep.Name != "" && fakeStubRe.MatchString(ep.Name) {
		return false
	}
	return true
}

func ExtractAllVless(payload string) []Endpoint {
	if strings.TrimSpace(payload) == "" {
		return nil
	}
	var out []Endpoint
	seen := make(map[string]struct{})
	addURI := func(u string) {
		u = strings.TrimRight(u, ",;.")
		ep, ok := ParseVlessURL(u)
		if !ok {
			return
		}
		if _, dup := seen[ep.Raw]; dup {
			return
		}
		seen[ep.Raw] = struct{}{}
		out = append(out, ep)
	}
	scanText := func(t string) {
		norm := strings.ReplaceAll(strings.ReplaceAll(t, `\u0026`, "&"), `\/`, `/`)
		for _, m := range vlessRe.FindAllString(norm, -1) {
			addURI(m)
		}
	}

	scanText(payload)
	if len(out) == 0 {
		if decoded, ok := tryDecodeBase64(payload); ok {
			scanText(decoded)
			if len(out) == 0 {
				trimmed := strings.TrimSpace(decoded)
				if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
					payload = decoded
				}
			}
		}
	}
	if len(out) == 0 {
		trimmed := strings.TrimSpace(payload)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			for _, u := range extractVlessFromJSON(trimmed) {
				addURI(u)
			}
		}
	}
	return out
}

func MeasurePayload(payload string) (total, usable int) {
	all := ExtractAllVless(payload)
	total = len(all)
	for _, ep := range all {
		if IsUsableNode(ep) {
			usable++
		}
	}
	return total, usable
}

type SubscriptionResult struct {
	Body  string
	Via   string
	Error string
	Nodes []Endpoint
}

func FetchSubscriptionDetailed(ctx context.Context, subURL string) SubscriptionResult {
	subURL = strings.TrimSpace(subURL)
	if subURL == "" {
		return SubscriptionResult{Error: "empty subscription URL"}
	}

	type routeSpec struct {
		label string
		proxy func(*http.Request) (*url.URL, error)
	}
	specs := []routeSpec{
		{label: "direct", proxy: nil},
		{label: "system proxy", proxy: http.ProxyFromEnvironment},
	}
	for _, pt := range []int{10809, 10808, 2080, 2081, 7890, 28080} {
		if isTCPPortOpen("127.0.0.1", pt, 180*time.Millisecond) {
			pURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", pt))
			specs = append(specs, routeSpec{
				label: fmt.Sprintf("local proxy :%d", pt),
				proxy: http.ProxyURL(pURL),
			})
		}
	}

	uas := []string{"v2rayN/6.45", "Happ/2.0"}
	var bestBody, bestVia, lastErr string
	bestCount, bestReal := 0, -1

	for _, sp := range specs {
		for _, ua := range uas {
			body, err := httpGetText(ctx, subURL, ua, sp.proxy)
			if err != nil {
				lastErr = err.Error()
				continue
			}
			if strings.TrimSpace(body) == "" {
				continue
			}
			all := ExtractAllVless(body)
			usable := 0
			for _, n := range all {
				if IsUsableNode(n) {
					usable++
				}
			}
			via := sp.label + " " + ua
			if usable >= 2 {
				return SubscriptionResult{Body: body, Via: via, Nodes: all}
			}
			if len(all) > 0 && (usable > bestReal || (usable == bestReal && len(all) > bestCount)) {
				bestBody = body
				bestVia = via
				bestCount = len(all)
				bestReal = usable
			}
		}
		if bestCount > 0 {
			return SubscriptionResult{Body: bestBody, Via: bestVia, Nodes: ExtractAllVless(bestBody)}
		}
	}

	if lastErr == "" {
		lastErr = "подписка не содержит валидных VLESS-узлов"
	}
	return SubscriptionResult{Error: lastErr}
}

type HappDiscovery struct {
	EndpointURL   string   `json:"endpointUrl"`
	SubURL        string   `json:"subUrl"`
	SubURLs       []string `json:"subUrls"`
	Source        string   `json:"source"`
	EncryptedOnly bool     `json:"encryptedOnly"`
}

func FindHappConfig(extraRoots ...string) HappDiscovery {
	var roots []string
	seen := make(map[string]struct{})
	addRoot := func(dir string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		low := strings.ToLower(abs)
		if _, ok := seen[low]; ok {
			return
		}
		seen[low] = struct{}{}
		roots = append(roots, abs)
	}

	for _, er := range extraRoots {
		addRoot(er)
	}
	for _, envName := range []string{"LOCALAPPDATA", "APPDATA"} {
		base := os.Getenv(envName)
		if base == "" {
			continue
		}
		for _, sub := range []string{"Happ", "happ-desktop", "com.happ.app", filepath.Join("Programs", "Happ")} {
			addRoot(filepath.Join(base, sub))
		}
		if entries, err := os.ReadDir(base); err == nil {
			for _, ent := range entries {
				if ent.IsDir() && happNameRe.MatchString(ent.Name()) {
					addRoot(filepath.Join(base, ent.Name()))
				}
			}
		}
	}

	type fileEntry struct {
		path  string
		name  string
		ext   string
		mtime time.Time
	}
	allowedExt := map[string]bool{
		".json": true, ".ini": true, ".conf": true, ".cfg": true,
		".txt": true, ".db": true, ".sqlite": true, ".sqlite3": true, ".log": true,
	}
	var files []fileEntry
	for _, r := range roots {
		collectFilesMaxDepth(r, 0, 3, 5*1024*1024, allowedExt, func(p string, info os.FileInfo) {
			files = append(files, fileEntry{
				path:  p,
				name:  info.Name(),
				ext:   strings.ToLower(filepath.Ext(info.Name())),
				mtime: info.ModTime(),
			})
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.After(files[j].mtime) })

	sawEncrypted := false
	for _, f := range files {
		if strings.EqualFold(f.name, "subs.db") {
			sawEncrypted = true
		}
		raw, err := os.ReadFile(f.path)
		if err != nil || len(raw) == 0 {
			continue
		}
		text := bytesToPrintableUTF8(raw)
		if happCryptoRe.MatchString(text) {
			sawEncrypted = true
		}
		if nodes := ExtractAllVless(text); len(nodes) > 0 {
			for _, n := range nodes {
				if IsUsableNode(n) {
					return HappDiscovery{EndpointURL: n.Raw, Source: fmt.Sprintf("Happ (%s)", f.name)}
				}
			}
			return HappDiscovery{EndpointURL: nodes[0].Raw, Source: fmt.Sprintf("Happ (%s)", f.name)}
		}
	}

	if sub := findHappRegistryVLESS(&sawEncrypted); sub != "" {
		return HappDiscovery{EndpointURL: sub, Source: "Happ (Registry)"}
	}

	subURLs := findHappSubscriptionURLs(roots)
	if len(subURLs) > 0 {
		return HappDiscovery{
			SubURL:  subURLs[0],
			SubURLs: subURLs,
			Source:  "Happ (subscription_log.txt)",
		}
	}

	return HappDiscovery{EncryptedOnly: sawEncrypted}
}

func findHappRegistryVLESS(sawEncrypted *bool) string {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Software", registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return ""
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return ""
	}
	for _, n := range names {
		if !happNameRe.MatchString(n) {
			continue
		}
		if u := scanRegKeyForVless(registry.CURRENT_USER, `Software\`+n, 0, sawEncrypted); u != "" {
			return u
		}
	}
	return ""
}

func scanRegKeyForVless(root registry.Key, path string, depth int, sawEncrypted *bool) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return ""
	}
	defer k.Close()
	if valNames, err := k.ReadValueNames(-1); err == nil {
		for _, vn := range valNames {
			if s, _, err := k.GetStringValue(vn); err == nil && s != "" {
				if happCryptoRe.MatchString(s) {
					*sawEncrypted = true
				}
				if nodes := ExtractAllVless(s); len(nodes) > 0 {
					return nodes[0].Raw
				}
			}
		}
	}
	if depth < 2 {
		if subs, err := k.ReadSubKeyNames(-1); err == nil {
			for _, sub := range subs {
				if u := scanRegKeyForVless(root, path+`\`+sub, depth+1, sawEncrypted); u != "" {
					return u
				}
			}
		}
	}
	return ""
}

func findHappSubscriptionURLs(roots []string) []string {
	var logPaths []string
	addLog := func(p string) {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			logPaths = append(logPaths, p)
		}
	}
	for _, r := range roots {
		addLog(filepath.Join(r, "subscription_log.txt"))
		addLog(filepath.Join(r, "logs", "subscription_log.txt"))
	}
	for _, envName := range []string{"LOCALAPPDATA", "APPDATA"} {
		if b := os.Getenv(envName); b != "" {
			addLog(filepath.Join(b, "Happ", "logs", "subscription_log.txt"))
		}
	}

	var urls []string
	seen := make(map[string]struct{})
	for _, lp := range logPaths {
		raw, err := os.ReadFile(lp)
		if err != nil {
			continue
		}
		lines := strings.Split(string(raw), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			for _, m := range httpURLRe.FindAllString(lines[i], -1) {
				u := strings.TrimRight(m, ",;.")
				if _, ok := seen[u]; !ok {
					seen[u] = struct{}{}
					urls = append(urls, u)
				}
			}
		}
	}
	return urls
}

func collectFilesMaxDepth(dir string, depth, maxDepth int, maxBytes int64, exts map[string]bool, fn func(string, os.FileInfo)) {
	if depth > maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		p := filepath.Join(dir, ent.Name())
		if ent.IsDir() {
			collectFilesMaxDepth(p, depth+1, maxDepth, maxBytes, exts, fn)
			continue
		}
		ext := strings.ToLower(filepath.Ext(ent.Name()))
		if !exts[ext] {
			continue
		}
		info, err := ent.Info()
		if err != nil || info.Size() <= 0 || info.Size() > maxBytes {
			continue
		}
		fn(p, info)
	}
}

func bytesToPrintableUTF8(b []byte) string {
	cp := make([]byte, len(b))
	for i, c := range b {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			cp[i] = ' '
		} else {
			cp[i] = c
		}
	}
	s := string(cp)
	return strings.TrimPrefix(s, "\uFEFF")
}

func cleanEndpointRaw(raw string) string {
	s := strings.Trim(strings.TrimSpace(raw), `"'`)
	s = strings.ReplaceAll(s, `\u0026`, "&")
	s = strings.ReplaceAll(s, `\/`, "/")
	return s
}

func firstQueryValues(rawQuery string) map[string]string {
	out := make(map[string]string)
	if rawQuery == "" {
		return out
	}
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		k, err := url.QueryUnescape(kv[0])
		if err != nil {
			k = kv[0]
		}
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		if _, exists := out[k]; exists {
			continue
		}
		v := ""
		if len(kv) > 1 {
			if uv, err := url.QueryUnescape(kv[1]); err == nil {
				v = uv
			} else {
				v = kv[1]
			}
		}
		out[k] = v
	}
	return out
}

func tryDecodeBase64(s string) (string, bool) {
	b := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
	if len(b) <= 8 {
		return "", false
	}
	b = strings.ReplaceAll(strings.ReplaceAll(b, "-", "+"), "_", "/")
	if rem := len(b) % 4; rem != 0 {
		b += strings.Repeat("=", 4-rem)
	}
	raw, err := base64.StdEncoding.DecodeString(b)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func extractVlessFromJSON(raw string) []string {
	var root map[string]any
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		var arr []map[string]any
		if err2 := json.Unmarshal([]byte(raw), &arr); err2 != nil {
			return nil
		}
		root = map[string]any{"outbounds": arr}
	}
	var obs []map[string]any
	if list, ok := root["outbounds"].([]any); ok {
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				obs = append(obs, m)
			}
		}
	} else if list, ok := root["outbounds"].([]map[string]any); ok {
		obs = append(obs, list...)
	}
	if strVal(root["protocol"]) == "vless" || strVal(root["type"]) == "vless" {
		obs = append(obs, root)
	}

	var uris []string
	for _, ob := range obs {
		if strVal(ob["type"]) == "vless" {
			if u := vlessFromSingBoxMap(ob); u != "" {
				uris = append(uris, u)
			}
		} else if strVal(ob["protocol"]) == "vless" {
			if u := vlessFromXrayMap(ob); u != "" {
				uris = append(uris, u)
			}
		}
	}
	return uris
}

func vlessFromSingBoxMap(ob map[string]any) string {
	server := strVal(ob["server"])
	uuid := strVal(ob["uuid"])
	port := intVal(ob["server_port"], 443)
	if server == "" || uuid == "" || port < 1 || port > 65535 {
		return ""
	}
	params := url.Values{}
	if f := strVal(ob["flow"]); f != "" {
		params.Set("flow", f)
	}
	if tlsMap, ok := ob["tls"].(map[string]any); ok && boolVal(tlsMap["enabled"]) {
		if realMap, ok := tlsMap["reality"].(map[string]any); ok && boolVal(realMap["enabled"]) {
			params.Set("security", "reality")
			if v := strVal(realMap["public_key"]); v != "" {
				params.Set("pbk", v)
			}
			if v := strVal(realMap["short_id"]); v != "" {
				params.Set("sid", v)
			}
		} else {
			params.Set("security", "tls")
		}
		if v := strVal(tlsMap["server_name"]); v != "" {
			params.Set("sni", v)
		}
		if utlsMap, ok := tlsMap["utls"].(map[string]any); ok {
			if v := strVal(utlsMap["fingerprint"]); v != "" {
				params.Set("fp", v)
			}
		}
	}
	if trMap, ok := ob["transport"].(map[string]any); ok {
		if t := strVal(trMap["type"]); t != "" {
			params.Set("type", t)
		}
		if v := strVal(trMap["service_name"]); v != "" {
			params.Set("serviceName", v)
		}
		if v := strVal(trMap["path"]); v != "" {
			params.Set("path", v)
		}
	}
	return buildVlessURI(uuid, server, port, params)
}

func vlessFromXrayMap(ob map[string]any) string {
	settings, _ := ob["settings"].(map[string]any)
	vnextList, _ := settings["vnext"].([]any)
	if len(vnextList) == 0 {
		return ""
	}
	vnext, _ := vnextList[0].(map[string]any)
	users, _ := vnext["users"].([]any)
	if len(users) == 0 {
		return ""
	}
	user, _ := users[0].(map[string]any)
	server := strVal(vnext["address"])
	uuid := strVal(user["id"])
	port := intVal(vnext["port"], 443)
	if server == "" || uuid == "" || port < 1 || port > 65535 {
		return ""
	}
	params := url.Values{}
	if f := strVal(user["flow"]); f != "" {
		params.Set("flow", f)
	}
	if stream, ok := ob["streamSettings"].(map[string]any); ok {
		if t := strVal(stream["network"]); t != "" {
			params.Set("type", t)
		}
		if s := strVal(stream["security"]); s != "" {
			params.Set("security", s)
		}
		if rs, ok := stream["realitySettings"].(map[string]any); ok {
			if v := strVal(rs["serverName"]); v != "" {
				params.Set("sni", v)
			}
			if v := strVal(rs["fingerprint"]); v != "" {
				params.Set("fp", v)
			}
			if v := strVal(rs["publicKey"]); v != "" {
				params.Set("pbk", v)
			}
			if v := strVal(rs["shortId"]); v != "" {
				params.Set("sid", v)
			}
		} else if ts, ok := stream["tlsSettings"].(map[string]any); ok {
			if v := strVal(ts["serverName"]); v != "" {
				params.Set("sni", v)
			}
			if v := strVal(ts["fingerprint"]); v != "" {
				params.Set("fp", v)
			}
		}
		if gs, ok := stream["grpcSettings"].(map[string]any); ok {
			if v := strVal(gs["serviceName"]); v != "" {
				params.Set("serviceName", v)
			}
		}
		if ws, ok := stream["wsSettings"].(map[string]any); ok {
			if v := strVal(ws["path"]); v != "" {
				params.Set("path", v)
			}
		}
	}
	return buildVlessURI(uuid, server, port, params)
}

func buildVlessURI(uuid, server string, port int, params url.Values) string {
	hostPart := server
	if strings.Contains(server, ":") && !strings.HasPrefix(server, "[") {
		hostPart = "[" + server + "]"
	}
	q := params.Encode()
	if q != "" {
		return fmt.Sprintf("vless://%s@%s:%d?%s", url.PathEscape(uuid), hostPart, port, q)
	}
	return fmt.Sprintf("vless://%s@%s:%d", url.PathEscape(uuid), hostPart, port)
}

func httpGetText(ctx context.Context, targetURL, ua string, proxyFn func(*http.Request) (*url.URL, error)) (string, error) {
	jar, _ := cookiejar.New(nil)
	tr := &http.Transport{Proxy: proxyFn}
	client := &http.Client{
		Transport: tr,
		Timeout:   15 * time.Second,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			req.Header.Set("User-Agent", ua)
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	return string(b), err
}

func isTCPPortOpen(host string, port int, timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func strVal(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func boolVal(v any) bool {
	b, _ := v.(bool)
	return b
}

func intVal(v any, def int) int {
	switch x := v.(type) {
	case float64:
		if int(x) > 0 {
			return int(x)
		}
	case int:
		if x > 0 {
			return x
		}
	}
	return def
}
