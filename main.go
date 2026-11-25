package main

import (
	"context"
	"embed"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	win "golang.org/x/sys/windows"

	"github.com/qlfu/grav-proxy/internal/config"
	"github.com/qlfu/grav-proxy/internal/engine"
	"github.com/qlfu/grav-proxy/internal/happ"
	"github.com/qlfu/grav-proxy/internal/pac"
	"github.com/qlfu/grav-proxy/internal/patcher"
	"github.com/qlfu/grav-proxy/internal/tray"
)

//go:embed all:frontend/dist
var frontendAssets embed.FS

var kernel32DLL = win.NewLazySystemDLL("kernel32.dll")
var procAttachConsole = kernel32DLL.NewProc("AttachConsole")

func main() {
	selfExe, err := os.Executable()
	if err != nil {
		selfExe = os.Args[0]
	}

	if len(os.Args) > 1 {
		switch strings.ToLower(os.Args[1]) {
		case "--tray":
			if err := tray.Run(selfExe); err != nil {
				os.Exit(1)
			}
			return
		case "--launch":
			target := ""
			if len(os.Args) > 2 {
				target = os.Args[2]
			}
			var forward []string
			if len(os.Args) > 3 {
				forward = os.Args[3:]
			}
			os.Exit(runLauncher(selfExe, target, forward))
		case "--cli":
			attachParentConsole()
			os.Exit(runCLI(selfExe, os.Args[2:]))
		}
	}

	patcher.CloseExistingGUIInstances()
	patcher.WriteGUIPidFile()
	defer patcher.RemoveGUIPidFile()

	app := NewApp(selfExe)
	err = wails.Run(&options.App{
		Title:     "GravProxy",
		Width:     780,
		Height:    640,
		MinWidth:  760,
		MinHeight: 620,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: frontendAssets,
		},
		BackgroundColour: &options.RGBA{R: 30, G: 30, B: 30, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []any{
			app,
		},
		Windows: &windows.Options{
			Theme:               windows.SystemDefault,
			WindowIsTranslucent: false,
		},
	})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "GravProxy GUI error: %v\n", err)
		os.Exit(1)
	}
}

func attachParentConsole() {
	const attachParent = ^uintptr(0)
	_, _, _ = procAttachConsole.Call(attachParent)
}

func runLauncher(selfExe, target string, forward []string) int {
	cfg, dataDir, _ := config.Load()
	proxyPort := cfg.ProxyPort
	if proxyPort <= 0 {
		proxyPort = config.DefaultProxyPt
	}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", proxyPort)

	if !ensureTrayProxyUp(selfExe, dataDir, proxyPort) {
		_, _ = fmt.Fprintf(os.Stderr, "[ERR] Локальный прокси 127.0.0.1:%d недоступен\n", proxyPort)
		return 2
	}

	switch strings.ToLower(strings.TrimSpace(target)) {
	case "antigravity":
		roots := patcher.FindAntigravityRoots()
		mainExe := patcher.FindMainAntigravityExe(roots)
		if mainExe == "" {
			_, _ = fmt.Fprintln(os.Stderr, "[ERR] Antigravity.exe не найден")
			return 3
		}
		_, _, _ = patcher.PatchAntigravityBinaries(dataDir, roots)
		args := append([]string{
			"--proxy-server=" + proxyURL,
			"--proxy-bypass-list=localhost;127.0.0.1;[::1]",
		}, forward...)
		_ = os.Setenv("AG_LS_PROXY", proxyURL)
		_ = os.Setenv("NO_PROXY", "localhost,127.0.0.1,::1")
		_ = os.Unsetenv("no_proxy")
		cmd := exec.Command(mainExe, args...)
		cmd.Dir = filepath.Dir(mainExe)
		if err := cmd.Start(); err != nil {
			return 4
		}
		return 0

	case "opencode":
		_ = os.Setenv("HTTP_PROXY", proxyURL)
		_ = os.Setenv("HTTPS_PROXY", proxyURL)
		_ = os.Setenv("http_proxy", proxyURL)
		_ = os.Setenv("https_proxy", proxyURL)
		_ = os.Setenv("NO_PROXY", "localhost,127.0.0.1,::1")
		if len(forward) == 0 {
			if gui := patcher.FindOpenCodeGUI(); gui != "" {
				cmd := exec.Command(gui)
				cmd.Dir = filepath.Dir(gui)
				if err := cmd.Start(); err == nil {
					return 0
				}
			}
		}
		if cli := patcher.FindOpenCodeCLI(); cli != "" {
			cmd := exec.Command(cli, forward...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return 1
			}
			return 0
		}
		return 3

	case "claude":
		cmd := exec.Command("cmd.exe", "/c", "start", "", "Claude:")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: win.CREATE_NO_WINDOW}
		_ = cmd.Start()
		return 0
	}
	return 1
}

func ensureTrayProxyUp(selfExe, dataDir string, proxyPort int) bool {
	if isLoopbackOpen(proxyPort, 250*time.Millisecond) {
		return true
	}
	trayBin := filepath.Join(dataDir, "GravProxy.exe")
	if _, err := os.Stat(trayBin); err != nil {
		trayBin = selfExe
	}
	cmd := exec.Command(trayBin, "--tray")
	cmd.Dir = filepath.Dir(trayBin)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: win.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		return false
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if isLoopbackOpen(proxyPort, 200*time.Millisecond) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return isLoopbackOpen(proxyPort, 300*time.Millisecond)
}

func runCLI(selfExe string, args []string) int {
	sub := "status"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	cfg, dataDir, _ := config.Load()

	switch sub {
	case "status":
		pOpen := isLoopbackOpen(cfg.ProxyPort, 250*time.Millisecond)
		pacOpen := isLoopbackOpen(cfg.PACPort, 250*time.Millisecond)
		fmt.Printf("GravProxy v%s\n", config.AppVersion)
		fmt.Printf("DataDir:   %s\n", dataDir)
		fmt.Printf("Mode:      %s\n", cfg.Mode)
		fmt.Printf("Port %d: %v | PAC %d: %v\n", cfg.ProxyPort, pOpen, cfg.PACPort, pacOpen)
		fmt.Printf("AG roots:  %v\n", patcher.FindAntigravityRoots())
		fmt.Printf("OC CLI:    %s\n", patcher.FindOpenCodeCLI())
		return 0

	case "smartdns-test":
		ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
		defer cancel()
		cands := make([]happ.Endpoint, 0, len(happ.SmartDNSPresets()))
		for _, pr := range happ.SmartDNSPresets() {
			if ep, ok := happ.ParseSmartDNSURL(pr.URL); ok {
				cands = append(cands, ep)
			}
		}
		res, err := engine.ProbeSmartDNS(ctx, cands)
		if err != nil {
			fmt.Printf("FAIL: %v\n", err)
			return 1
		}
		working := 0
		for _, r := range res {
			if r.Score >= 1 {
				working++
			}
			fmt.Printf("%-12s score=%d ag=%v nb=%v gem=%v anth=%v ms=%d err=%s\n",
				r.Name, r.Score, r.AgOk, r.NotebookOk, r.GeminiOk, r.AnthOk, r.LatencyMs, r.Error)
		}
		fmt.Printf("Working=%d/%d\n", working, len(res))
		if working == 0 {
			return 1
		}
		return 0

	case "test":
		if !isLoopbackOpen(cfg.ProxyPort, 300*time.Millisecond) {
			fmt.Printf("FAIL: port 127.0.0.1:%d is closed\n", cfg.ProxyPort)
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		items := engine.TestConnectivity(ctx, fmt.Sprintf("http://127.0.0.1:%d", cfg.ProxyPort), cfg.Mode == "smartdns")
		for _, it := range items {
			st := "FAIL"
			if it.OK {
				st = "OK"
			} else if it.Optional {
				st = "SKIP"
			}
			fmt.Printf("[%s] %-24s %s (%d ms)\n", st, it.Name, it.Detail, it.LatencyMs)
		}
		return 0

	case "repatch":
		app := NewApp(selfExe)
		app.logFn = func(s string) { fmt.Println(s) }
		res := app.Repatch(InstallRequest{
			Antigravity:   true,
			BrowserPAC:    cfg.Components.BrowserPAC,
			OpenCode:      true,
			ClaudeDesktop: cfg.Components.ClaudeDesktop,
			ClaudeBaseURL: cfg.ClaudeBaseURL,
			ClaudeScheme:  cfg.ClaudeAuthScheme,
		})
		if !res.OK {
			return 1
		}
		return 0

	case "install":
		smart := false
		sdURL := cfg.SmartDNSURL
		for _, a := range args[1:] {
			if a == "--smartdns" {
				smart = true
			} else if strings.HasPrefix(a, "--smartdns-url=") {
				smart = true
				sdURL = strings.TrimPrefix(a, "--smartdns-url=")
			}
		}
		app := NewApp(selfExe)
		app.logFn = func(s string) { fmt.Println(s) }
		mode := cfg.Mode
		if smart {
			mode = "smartdns"
		}
		res := app.Install(InstallRequest{
			Mode:            mode,
			EndpointInput:   cfg.EndpointURL,
			SubscriptionURL: cfg.SubscriptionURL,
			SmartDNSURL:     sdURL,
			Tray:            true,
			Antigravity:     true,
			BrowserPAC:      true,
			OpenCode:        true,
			ClaudeDesktop:   cfg.Components.ClaudeDesktop,
			ClaudeBaseURL:   cfg.ClaudeBaseURL,
			ClaudeScheme:    cfg.ClaudeAuthScheme,
		})
		if !res.OK {
			return 1
		}
		return 0

	case "uninstall":
		app := NewApp(selfExe)
		app.logFn = func(s string) { fmt.Println(s) }
		res := app.Uninstall()
		if !res.OK {
			return 1
		}
		return 0

	default:
		_, _ = io.WriteString(os.Stdout, "Usage: GravProxy.exe [--tray | --launch antigravity|opencode|claude | --cli status|smartdns-test|test|install|repatch|uninstall]\n")
		return 0
	}
}

func isLoopbackOpen(port int, timeout time.Duration) bool {
	if port <= 0 {
		return false
	}
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// Keep unused pac import satisfied cleanly if needed via CLI/app.
var _ = pac.DefaultDomains
