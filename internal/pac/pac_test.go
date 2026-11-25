package pac

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unsafe"
)

func TestGeneratePACScriptIsolation(t *testing.T) {
	script := GeneratePACScript("127.0.0.1", 28080, nil, true)
	for _, host := range []string{
		"cloudcode-pa.googleapis.com",
		"daily-cloudcode-pa.sandbox.googleapis.com",
		"gemini.google.com",
		"notebooklm.google.com",
		"claude.ai",
		"api.anthropic.com",
		"v0.app",
		"opencode.ai",
	} {
		if !strings.Contains(script, host) {
			t.Fatalf("PAC script missing AI host %s", host)
		}
	}
	inactive := GeneratePACScript("127.0.0.1", 28080, nil, false)
	if strings.Contains(inactive, "28080") {
		t.Fatal("inactive PAC must return only DIRECT")
	}
}

func TestControlAuthAndRoutes(t *testing.T) {
	s := NewServer(0, "127.0.0.1", 28080, "secret-token-123456", func() bool { return true }, ControlHooks{
		Status: func() map[string]any { return map[string]any{"active": true} },
	})
	h := s.Handler()

	// /proxy.pac is open on loopback
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/proxy.pac", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "PROXY 127.0.0.1:28080") {
		t.Fatalf("unexpected /proxy.pac response: %d", w.Code)
	}

	// /_ctl/status requires valid token
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/_ctl/status", nil))
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", w2.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/_ctl/status", nil)
	req.Header.Set("X-GravProxy-Token", "secret-token-123456")
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, req)
	if w3.Code != http.StatusOK || !strings.Contains(w3.Body.String(), `"active":true`) {
		t.Fatalf("unexpected authorized /_ctl/status response: %d %s", w3.Code, w3.Body.String())
	}
}

func TestWinINetStructSizes(t *testing.T) {
	if got := unsafe.Sizeof(internetPerConnOption{}); got != 16 {
		t.Fatalf("unexpected internetPerConnOption size: got %d want 16", got)
	}
	if got := unsafe.Sizeof(internetPerConnOptionList{}); got != 32 {
		t.Fatalf("unexpected internetPerConnOptionList size: got %d want 32", got)
	}
}
