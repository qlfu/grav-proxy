package happ

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestParseVlessRealityGRPC(t *testing.T) {
	raw := `"vless://11111111-2222-3333-4444-555555555555@nl.example.com:8443?type=grpc&security=reality&pbk=PUBKEY123&fp=chrome&sni=google.com&sid=abcd&serviceName=grpc.svc#NL%20Node"`
	ep, ok := ParseVlessURL(raw)
	if !ok {
		t.Fatalf("expected valid VLESS URL")
	}
	if ep.Kind != KindVLESS || ep.Server != "nl.example.com" || ep.Port != 8443 {
		t.Fatalf("unexpected parsed endpoint: %+v", ep)
	}
	if ep.Security != "reality" || ep.PBK != "PUBKEY123" || ep.Transport != "grpc" || ep.ServiceName != "grpc.svc" {
		t.Fatalf("missing reality/grpc fields: %+v", ep)
	}
	if ep.Name != "NL Node" {
		t.Fatalf("expected fragment 'NL Node', got %q", ep.Name)
	}
}

func TestParseSmartDNSVariants(t *testing.T) {
	cases := []struct {
		in      string
		wantOK  bool
		isAuto  bool
		dnsType string
		server  string
		port    int
	}{
		{"smartdns://auto", true, true, "https", "auto", 443},
		{"smartdns://https://eu.geohide.ru/dns-query", true, false, "https", "eu.geohide.ru", 443},
		{"smartdns://tls://dns.malw.link:853", true, false, "tls", "dns.malw.link", 853},
		{"1.1.1.1", true, false, "udp", "1.1.1.1", 53},
		{"smartdns://127.0.0.1", false, false, "", "", 0},
		{"smartdns://localhost", false, false, "", "", 0},
	}
	for _, tc := range cases {
		ep, ok := ParseSmartDNSURL(tc.in)
		if ok != tc.wantOK {
			t.Fatalf("ParseSmartDNSURL(%q): ok=%v want %v", tc.in, ok, tc.wantOK)
		}
		if !ok {
			continue
		}
		if ep.IsAuto != tc.isAuto || ep.DNSType != tc.dnsType || ep.Server != tc.server || ep.Port != tc.port {
			t.Fatalf("ParseSmartDNSURL(%q) = %+v", tc.in, ep)
		}
	}
}

func TestParseUpstreamGuards(t *testing.T) {
	if _, ok := ParseUpstreamProxyURL("socks5://127.0.0.1:28080"); ok {
		t.Fatal("must reject self-loop :28080")
	}
	ep, ok := ParseUpstreamProxyURL("socks5://127.0.0.1:10808")
	if !ok || ep.Protocol != "socks" || ep.Port != 10808 {
		t.Fatalf("unexpected socks5 parse: %v %+v", ok, ep)
	}
}

func TestExtractBase64AndFilterFakeIP(t *testing.T) {
	raw := "vless://11111111-2222-3333-4444-555555555555@198.18.0.1:443?security=tls#Fake\n" +
		"vless://11111111-2222-3333-4444-555555555555@45.10.20.30:443?security=tls#Real"
	b64 := base64.RawURLEncoding.EncodeToString([]byte(raw))
	nodes := ExtractAllVless(b64)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 extracted nodes, got %d", len(nodes))
	}
	if !IsFakeIP(net.ParseIP("198.18.0.1")) {
		t.Fatal("198.18.0.1 must be detected as FakeIP")
	}
	if IsUsableNode(nodes[0]) {
		t.Fatal("198.18.0.1 node must not be usable")
	}
	if !IsUsableNode(nodes[1]) {
		t.Fatal("45.10.20.30 node must be usable")
	}
}

func TestFetchSubscriptionDetailed(t *testing.T) {
	raw := "vless://11111111-2222-3333-4444-555555555555@de.example.com:443?security=tls#DE\n" +
		"vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?security=tls#NL"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(raw))))
	}))
	defer srv.Close()

	res := FetchSubscriptionDetailed(context.Background(), srv.URL)
	if res.Error != "" || len(res.Nodes) != 2 {
		t.Fatalf("unexpected subscription result: %+v", res)
	}
}

func TestFetchSubscriptionCookieRedirect(t *testing.T) {
	raw := "vless://11111111-2222-3333-4444-555555555555@de.example.com:443?security=tls#DE\n" +
		"vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?security=tls#NL"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("__check")
		if err != nil || c.Value != "ok" {
			http.SetCookie(w, &http.Cookie{Name: "__check", Value: "ok", Path: "/"})
			http.Redirect(w, r, srv.URL+"/sub", http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(raw))))
	}))
	defer srv.Close()

	res := FetchSubscriptionDetailed(context.Background(), srv.URL+"/sub")
	if res.Error != "" || len(res.Nodes) != 2 {
		t.Fatalf("cookie redirect fetch failed: %+v", res)
	}

	if liveURL := os.Getenv("GRAV_LIVE_SUB_URL"); liveURL != "" {
		liveRes := FetchSubscriptionDetailed(context.Background(), liveURL)
		if liveRes.Error != "" || len(liveRes.Nodes) == 0 {
			t.Fatalf("live subscription fetch failed: %+v", liveRes)
		}
		t.Logf("live subscription via=%s nodes=%d", liveRes.Via, len(liveRes.Nodes))
	}
}
