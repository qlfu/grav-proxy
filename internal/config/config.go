package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	AppVersion      = "2.0.2"
	GitHubRepo      = "qlfu/grav-proxy"
	DefaultProxy    = "http://127.0.0.1:28080"
	DefaultProxyPt  = 28080
	DefaultPACPt    = 28081
	DirName         = "GravProxy"
	LegacyDirName   = "ag-proxy"
	ConfigFileName  = "config.json"
	TokenFileName   = "control.token"
)

type Components struct {
	Tray          bool `json:"tray"`
	Antigravity   bool `json:"antigravity"`
	BrowserPAC    bool `json:"browserPac"`
	OpenCode      bool `json:"openCode"`
	ClaudeDesktop bool `json:"claudeDesktop"`
}

type Config struct {
	Mode             string     `json:"mode"` // "vless" | "smartdns" | "upstream"
	EndpointURL      string     `json:"endpointUrl"`
	SubscriptionURL  string     `json:"subscriptionUrl"`
	SmartDNSURL      string     `json:"smartDnsUrl"`
	ProxyPort        int        `json:"proxyPort"`
	PACPort          int        `json:"pacPort"`
	Theme            string     `json:"theme"` // "auto" | "dark" | "light"
	Components       Components `json:"components"`
	ClaudeBaseURL    string     `json:"claudeBaseUrl,omitempty"`
	ClaudeAuthScheme string     `json:"claudeAuthScheme,omitempty"` // "bearer" | "x-api-key"
}

func Default() Config {
	return Config{
		Mode:        "vless",
		SmartDNSURL: "smartdns://auto",
		ProxyPort:   DefaultProxyPt,
		PACPort:     DefaultPACPt,
		Theme:       "auto",
		Components: Components{
			Tray:          true,
			Antigravity:   true,
			BrowserPAC:    true,
			OpenCode:      true,
			ClaudeDesktop: false,
		},
		ClaudeAuthScheme: "bearer",
	}
}

func DefaultDataDir() (string, error) {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("LOCALAPPDATA is not set")
		}
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, DirName), nil
}

func LegacyDataDir() string {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		return ""
	}
	return filepath.Join(base, LegacyDirName)
}

func Load() (Config, string, error) {
	dir, err := DefaultDataDir()
	if err != nil {
		return Default(), "", err
	}
	cfg, err := LoadFromDir(dir)
	return cfg, dir, err
}

func LoadFromDir(dir string) (Config, error) {
	cfg := Default()
	path := filepath.Join(dir, ConfigFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.applyLegacyEnvMigration()
			return cfg, nil
		}
		return cfg, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		cfg.applyLegacyEnvMigration()
		return cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Default(), err
	}
	cfg.normalize()
	return cfg, nil
}

func (c *Config) normalize() {
	switch strings.ToLower(strings.TrimSpace(c.Mode)) {
	case "smartdns", "upstream", "vless":
		c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	default:
		c.Mode = "vless"
	}
	if strings.TrimSpace(c.SmartDNSURL) == "" {
		c.SmartDNSURL = "smartdns://auto"
	}
	if c.ProxyPort <= 0 || c.ProxyPort > 65535 {
		c.ProxyPort = DefaultProxyPt
	}
	if c.PACPort <= 0 || c.PACPort > 65535 {
		c.PACPort = DefaultPACPt
	}
	switch strings.ToLower(strings.TrimSpace(c.Theme)) {
	case "dark", "light", "auto":
		c.Theme = strings.ToLower(strings.TrimSpace(c.Theme))
	default:
		c.Theme = "auto"
	}
	if !strings.EqualFold(c.ClaudeAuthScheme, "x-api-key") {
		c.ClaudeAuthScheme = "bearer"
	} else {
		c.ClaudeAuthScheme = "x-api-key"
	}
}

func (c *Config) applyLegacyEnvMigration() {
	ep, sub := ReadLegacyUserEnv()
	if sub != "" {
		c.SubscriptionURL = sub
	}
	if ep != "" {
		low := strings.ToLower(ep)
		switch {
		case strings.HasPrefix(low, "smartdns://"):
			c.Mode = "smartdns"
			c.SmartDNSURL = ep
		case strings.HasPrefix(low, "socks5://"), strings.HasPrefix(low, "socks://"), strings.HasPrefix(low, "http://"):
			c.Mode = "upstream"
			c.EndpointURL = ep
		default:
			c.Mode = "vless"
			c.EndpointURL = ep
		}
	}
	c.normalize()
}

func ReadLegacyUserEnv() (endpoint, subURL string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		if v, _, err := k.GetStringValue("AG_PROXY_VLESS"); err == nil {
			endpoint = strings.TrimSpace(v)
		}
		if v, _, err := k.GetStringValue("AG_PROXY_SUB_URL"); err == nil {
			subURL = strings.TrimSpace(v)
		}
	}
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("AG_PROXY_VLESS"))
	}
	if subURL == "" {
		subURL = strings.TrimSpace(os.Getenv("AG_PROXY_SUB_URL"))
	}
	return endpoint, subURL
}

func SaveToDir(dir string, cfg Config) error {
	cfg.normalize()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, ConfigFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func EnsureControlToken(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, TokenFileName)
	if raw, err := os.ReadFile(path); err == nil {
		tok := strings.TrimSpace(string(raw))
		if len(tok) >= 32 {
			return tok, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(tok), 0600); err != nil {
		return "", err
	}
	return tok, nil
}

func ReadControlToken(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, TokenFileName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
