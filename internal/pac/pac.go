package pac

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var DefaultDomains = []string{
	"v0.app",
	"v0.dev",
	"v0.chat",
	"v0.build",
	"v0sdk.com",
	"vusercontent.net",
	"vercel.com",
	"vercel.app",
	"vercel.live",
	"vercel.run",
	"vercel.sh",
	"vercel.dev",
	"vercel-storage.com",
	"vercel-scripts.com",
	"playground-vercel.tools",
	"playground-vercel.apps",
	"pusher.com",
	"ethyca.com",
	"challenges.cloudflare.com",
	"hcaptcha.com",
	"clerk.accounts.dev",
	"auth0.com",
	"workos.com",
	"v0chat-agent-data-prod.s3.us-east-1.amazonaws.com",
	"v0chat-agent-data-prod-amazon.s3.us-east-1.amazonaws.com",
	"v0chat-agent-data-prod-noinvite-vtest314.s3.us-east-1.amazonaws.com",
	"cloudcode-pa.googleapis.com",
	"daily-cloudcode-pa.sandbox.googleapis.com",
	"autopush-cloudcode-pa.sandbox.googleapis.com",
	"openrouter.ai",
	"opencode.ai",
	"models.dev",
	"api.openai.com",
	"api.anthropic.com",
	"anthropic.com",
	"claude.ai",
	"claude.com",
	"claude.app",
	"claudeusercontent.com",
	"claudemcpcontent.com",
	"gemini.google.com",
	"gemini.google",
	"gemini.app.google",
	"gemini.gstatic.com",
	"geminiweb-pa.googleapis.com",
	"geminiweb-pa.clients6.google.com",
	"robinfrontend-pa.googleapis.com",
	"robinfrontend-pa.clients6.google.com",
	"proactivebackend-pa.googleapis.com",
	"geller-pa.googleapis.com",
	"signaler-pa.googleapis.com",
	"signaler-pa.clients6.google.com",
	"waa-pa.clients6.google.com",
	"ogads-pa.clients6.google.com",
	"push.clients6.google.com",
	"generativelanguage.googleapis.com",
	"alkalicore-pa.clients6.google.com",
	"alkalimakersuite-pa.clients6.google.com",
	"webchannel-alkalimakersuite-pa.clients6.google.com",
	"aistudio.google.com",
	"ai.google.dev",
	"ai.google",
	"usercontent.goog",
	"notebooklm.google.com",
	"notebooklm.google",
	"notebook.google.com",
	"notebook.google",
	"notebooklm-pa.googleapis.com",
	"notebooklm-pa.clients6.google.com",
	"notebooklm.googleapis.com",
	"labstailwind.pa.googleapis.com",
	"labs.google",
	"labs.google.com",
	"aisandbox-pa.googleapis.com",
	"contribution.usercontent.google.com",
}

var domainValidRe = regexp.MustCompile(`^[a-z0-9.-]+$`)

func MatchDomain(host, domain string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	d := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "."))
	if h == "" || d == "" {
		return false
	}
	return h == d || strings.HasSuffix(h, "."+d)
}

func GeneratePACScript(proxyHost string, proxyPort int, customDomains []string, active bool) string {
	if !active {
		return "function FindProxyForURL(url, host) {\n  return \"DIRECT\";\n}\n"
	}
	seen := make(map[string]struct{}, len(DefaultDomains)+len(customDomains))
	var exact []string
	var wildcard []string
	addDomain := func(d string) {
		d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "."))
		if d == "" || !domainValidRe.MatchString(d) {
			return
		}
		if _, ok := seen[d]; ok {
			return
		}
		seen[d] = struct{}{}
		exact = append(exact, fmt.Sprintf("%q:1", d))
		wildcard = append(wildcard, fmt.Sprintf("%q", "."+d))
	}
	for _, d := range DefaultDomains {
		addDomain(d)
	}
	for _, d := range customDomains {
		addDomain(d)
	}

	return fmt.Sprintf(`var GRAV_DOMAINS = {%s};
var GRAV_SUFFIXES = [%s];
function FindProxyForURL(url, host) {
  if (!host) return "DIRECT";
  host = host.toLowerCase();
  if (isPlainHostName(host) || host === "localhost" || host === "127.0.0.1" || host === "::1") return "DIRECT";
  if (GRAV_DOMAINS[host] === 1) return "PROXY %s:%d; DIRECT";
  for (var i = 0; i < GRAV_SUFFIXES.length; i++) {
    var s = GRAV_SUFFIXES[i];
    if (host.length > s.length && host.indexOf(s, host.length - s.length) !== -1) {
      return "PROXY %s:%d; DIRECT";
    }
  }
  return "DIRECT";
}
`, strings.Join(exact, ","), strings.Join(wildcard, ","), proxyHost, proxyPort, proxyHost, proxyPort)
}

type ControlHooks struct {
	Status  func() map[string]any
	Toggle  func() (bool, error)
	Reload  func() error
	Recheck func() error
	Stop    func()
}

type Server struct {
	mu           sync.RWMutex
	port         int
	proxyHost    string
	proxyPort    int
	activeFn     func() bool
	token        string
	hooks        ControlHooks
	ln           net.Listener
	srv          *http.Server
	pacCustomDom []string
}

func NewServer(port int, proxyHost string, proxyPort int, token string, activeFn func() bool, hooks ControlHooks) *Server {
	if port <= 0 {
		port = 28081
	}
	if proxyHost == "" {
		proxyHost = "127.0.0.1"
	}
	if proxyPort <= 0 {
		proxyPort = 28080
	}
	return &Server{
		port:      port,
		proxyHost: proxyHost,
		proxyPort: proxyPort,
		token:     token,
		activeFn:  activeFn,
		hooks:     hooks,
	}
}

func (s *Server) SetCustomDomains(domains []string) {
	s.mu.Lock()
	s.pacCustomDom = append([]string(nil), domains...)
	s.mu.Unlock()
}

func (s *Server) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ln != nil {
		if addr, ok := s.ln.Addr().(*net.TCPAddr); ok {
			return addr.Port
		}
	}
	return s.port
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil {
		return err
	}
	mux := s.Handler()
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.mu.Lock()
	s.ln = ln
	s.srv = srv
	s.mu.Unlock()

	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	s.ln = nil
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy.pac", func(w http.ResponseWriter, r *http.Request) {
		active := true
		if s.activeFn != nil {
			active = s.activeFn()
		}
		s.mu.RLock()
		custom := append([]string(nil), s.pacCustomDom...)
		s.mu.RUnlock()
		script := GeneratePACScript(s.proxyHost, s.proxyPort, custom, active)
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(script))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	requireAuth := func(w http.ResponseWriter, r *http.Request) bool {
		if s.token == "" {
			http.Error(w, "control token not configured", http.StatusForbidden)
			return false
		}
		got := strings.TrimSpace(r.Header.Get("X-GravProxy-Token"))
		if len(got) != len(s.token) || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}

	mux.HandleFunc("/_ctl/status", func(w http.ResponseWriter, r *http.Request) {
		if !requireAuth(w, r) {
			return
		}
		data := map[string]any{"ok": true}
		if s.hooks.Status != nil {
			data = s.hooks.Status()
		}
		writeJSON(w, http.StatusOK, data)
	})
	mux.HandleFunc("/_ctl/toggle", func(w http.ResponseWriter, r *http.Request) {
		if !requireAuth(w, r) {
			return
		}
		if s.hooks.Toggle == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		active, err := s.hooks.Toggle()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "active": active})
	})
	mux.HandleFunc("/_ctl/reload", func(w http.ResponseWriter, r *http.Request) {
		if !requireAuth(w, r) {
			return
		}
		if s.hooks.Reload != nil {
			if err := s.hooks.Reload(); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("/_ctl/recheck", func(w http.ResponseWriter, r *http.Request) {
		if !requireAuth(w, r) {
			return
		}
		if s.hooks.Recheck != nil {
			if err := s.hooks.Recheck(); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("/_ctl/stop", func(w http.ResponseWriter, r *http.Request) {
		if !requireAuth(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		if s.hooks.Stop != nil {
			go func() {
				time.Sleep(150 * time.Millisecond)
				s.hooks.Stop()
			}()
		}
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

var (
	modWininet              = windows.NewLazySystemDLL("wininet.dll")
	modKernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procInternetSetOption   = modWininet.NewProc("InternetSetOptionW")
	procInternetQueryOption = modWininet.NewProc("InternetQueryOptionW")
	procGlobalFree          = modKernel32.NewProc("GlobalFree")
)

const (
	internetOptionRefresh             = 37
	internetOptionSettingsChanged     = 39
	internetOptionPerConnectionOption = 75
	internetPerConnFlags              = 1
	internetPerConnAutoConfigURL      = 4
	proxyTypeDirect                   = 0x00000001
	proxyTypeAutoProxyURL             = 0x00000004
	inetSettingsRegPath               = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	pacBackupFile                     = "pac-backup.json"
)

type internetPerConnOption struct {
	dwOption uint32
	_        uint32
	value    uintptr
}

type internetPerConnOptionList struct {
	dwSize        uint32
	_             uint32
	pszConnection uintptr
	dwOptionCount uint32
	dwOptionError uint32
	pOptions      uintptr
}

type pacBackup struct {
	HadAutoConfigURL bool   `json:"hadAutoConfigUrl"`
	AutoConfigURL    string `json:"autoConfigUrl"`
}

func IsOurPACURL(u string, pacPort int) bool {
	low := strings.ToLower(strings.TrimSpace(u))
	prefix := fmt.Sprintf("http://127.0.0.1:%d/", pacPort)
	return strings.HasPrefix(low, prefix) || strings.HasPrefix(low, "http://127.0.0.1:28081/")
}

func queryWinINetPAC() (flags uint32, autoURL string, ok bool) {
	opts := [2]internetPerConnOption{
		{dwOption: internetPerConnFlags},
		{dwOption: internetPerConnAutoConfigURL},
	}
	list := internetPerConnOptionList{
		dwSize:        uint32(unsafe.Sizeof(internetPerConnOptionList{})),
		dwOptionCount: 2,
		pOptions:      uintptr(unsafe.Pointer(&opts[0])),
	}
	bufLen := list.dwSize
	r, _, _ := procInternetQueryOption.Call(0, internetOptionPerConnectionOption, uintptr(unsafe.Pointer(&list)), uintptr(unsafe.Pointer(&bufLen)))
	if r == 0 {
		return 0, "", false
	}
	flags = uint32(opts[0].value)
	if opts[1].value != 0 {
		autoURL = windows.UTF16PtrToString((*uint16)(unsafe.Pointer(opts[1].value)))
		_, _, _ = procGlobalFree.Call(opts[1].value)
	}
	if flags&proxyTypeAutoProxyURL == 0 {
		autoURL = ""
	}
	return flags, autoURL, true
}

func applyWinINetPAC(flags uint32, pacURL string) bool {
	var urlPtr uintptr
	if pacURL != "" {
		u16, err := windows.UTF16PtrFromString(pacURL)
		if err == nil {
			urlPtr = uintptr(unsafe.Pointer(u16))
		}
	}
	opts := [2]internetPerConnOption{
		{dwOption: internetPerConnFlags, value: uintptr(flags)},
		{dwOption: internetPerConnAutoConfigURL, value: urlPtr},
	}
	list := internetPerConnOptionList{
		dwSize:        uint32(unsafe.Sizeof(internetPerConnOptionList{})),
		dwOptionCount: 2,
		pOptions:      uintptr(unsafe.Pointer(&opts[0])),
	}
	r, _, _ := procInternetSetOption.Call(0, internetOptionPerConnectionOption, uintptr(unsafe.Pointer(&list)), uintptr(list.dwSize))
	if r != 0 {
		notifyWinINetRefresh()
		return true
	}
	return false
}

func EnableSystemPAC(pacURL, dataDir string, pacPort int) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, inetSettingsRegPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	flags, curWinINet, _ := queryWinINetPAC()
	curReg, _, getErr := k.GetStringValue("AutoConfigURL")
	bakPath := filepath.Join(dataDir, pacBackupFile)
	if _, err := os.Stat(bakPath); os.IsNotExist(err) {
		cur := curWinINet
		had := curWinINet != ""
		if cur == "" && getErr == nil && curReg != "" {
			cur = curReg
			had = true
		}
		if !IsOurPACURL(cur, pacPort) {
			bak := pacBackup{
				HadAutoConfigURL: had && cur != "",
				AutoConfigURL:    cur,
			}
			if raw, mErr := json.MarshalIndent(bak, "", "  "); mErr == nil {
				_ = os.WriteFile(bakPath, raw, 0644)
			}
		}
	}

	_ = k.SetStringValue("AutoConfigURL", pacURL)
	if !applyWinINetPAC(flags|proxyTypeDirect|proxyTypeAutoProxyURL, pacURL) {
		notifyWinINetRefresh()
	}
	return nil
}

func RestoreSystemPAC(dataDir string, pacPort int) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetSettingsRegPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()

	flags, curWinINet, _ := queryWinINetPAC()
	curReg, _, _ := k.GetStringValue("AutoConfigURL")
	bakPath := filepath.Join(dataDir, pacBackupFile)
	if raw, rErr := os.ReadFile(bakPath); rErr == nil {
		var bak pacBackup
		if json.Unmarshal(raw, &bak) == nil {
			if bak.HadAutoConfigURL && bak.AutoConfigURL != "" && !IsOurPACURL(bak.AutoConfigURL, pacPort) {
				_ = k.SetStringValue("AutoConfigURL", bak.AutoConfigURL)
				_ = applyWinINetPAC(flags|proxyTypeDirect|proxyTypeAutoProxyURL, bak.AutoConfigURL)
			} else if IsOurPACURL(curReg, pacPort) || IsOurPACURL(curWinINet, pacPort) {
				_ = k.DeleteValue("AutoConfigURL")
				_ = applyWinINetPAC((flags&^proxyTypeAutoProxyURL)|proxyTypeDirect, "")
			}
			_ = os.Remove(bakPath)
			return nil
		}
	}
	if IsOurPACURL(curReg, pacPort) || IsOurPACURL(curWinINet, pacPort) {
		_ = k.DeleteValue("AutoConfigURL")
		_ = applyWinINetPAC((flags&^proxyTypeAutoProxyURL)|proxyTypeDirect, "")
	}
	return nil
}

func notifyWinINetRefresh() {
	_, _, _ = procInternetSetOption.Call(0, internetOptionSettingsChanged, 0, 0)
	_, _, _ = procInternetSetOption.Call(0, internetOptionRefresh, 0, 0)
}
