package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"

	"github.com/qlfu/grav-proxy/internal/config"
	"github.com/qlfu/grav-proxy/internal/engine"
	"github.com/qlfu/grav-proxy/internal/happ"
	"github.com/qlfu/grav-proxy/internal/pac"
	"github.com/qlfu/grav-proxy/internal/patcher"
	"github.com/qlfu/grav-proxy/internal/updater"
)

type App struct {
	ctx               context.Context
	selfExe           string
	logFn             func(string)
	pendingSelfDelete bool
	cleanupTempExe    string
}

func NewApp(selfExe string) *App {
	return &App{selfExe: selfExe}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(ctx context.Context) {
	patcher.RemoveGUIPidFile()
	if a.cleanupTempExe != "" {
		cmdLine := fmt.Sprintf(`ping 127.0.0.1 -n 2 >nul & del /f /q "%s"`, a.cleanupTempExe)
		c := exec.Command("cmd.exe", "/c", cmdLine)
		c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		_ = c.Start()
		return
	}
	if !a.pendingSelfDelete {
		return
	}
	_, dataDir, err := config.Load()
	if err != nil || dataDir == "" {
		return
	}
	cmdLine := fmt.Sprintf(`ping 127.0.0.1 -n 2 >nul & del /f /q "%s" & rmdir "%s"`, a.selfExe, dataDir)
	c := exec.Command("cmd.exe", "/c", cmdLine)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	_ = c.Start()
}

func (a *App) log(line string) {
	if a.logFn != nil {
		a.logFn(line)
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "log:line", line)
	}
}

func (a *App) setProgress(pct int, header string) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "progress:update", map[string]any{
			"percent": pct,
			"header":  header,
		})
	}
}

func (a *App) setStep(key, state, text string) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "step:update", map[string]any{
			"key":   key,
			"state": state,
			"text":  text,
		})
	}
}

type InitialState struct {
	Version        string                `json:"version"`
	DataDir        string                `json:"dataDir"`
	ShortDataDir   string                `json:"shortDataDir"`
	Installed      bool                  `json:"installed"`
	Config         config.Config         `json:"config"`
	Presets        []happ.SmartDNSPreset `json:"presets"`
	ProxyRunning   bool                  `json:"proxyRunning"`
	PACRunning     bool                  `json:"pacRunning"`
	ActiveEndpoint string                `json:"activeEndpoint"`
	AntigravityOK  bool                  `json:"antigravityOk"`
	OpenCodeOK     bool                  `json:"openCodeOk"`
	BypassWarnings []string              `json:"bypassWarnings"`
	HappDiscovery  happ.HappDiscovery    `json:"happDiscovery"`
}

func isInstalledInDir(dataDir string, proxyOpen, pendingSelfDelete bool) bool {
	if pendingSelfDelete {
		return false
	}
	if proxyOpen {
		return true
	}
	_, errCfg := os.Stat(filepath.Join(dataDir, config.ConfigFileName))
	return errCfg == nil
}

func (a *App) GetInitialState() InitialState {
	cfg, dataDir, _ := config.Load()
	pOpen := isLoopbackOpen(cfg.ProxyPort, 200*time.Millisecond)
	pacOpen := isLoopbackOpen(cfg.PACPort, 200*time.Millisecond)

	installed := isInstalledInDir(dataDir, pOpen, a.pendingSelfDelete)

	shortDir := dataDir
	if la := os.Getenv("LOCALAPPDATA"); la != "" && strings.HasPrefix(strings.ToLower(dataDir), strings.ToLower(la)) {
		shortDir = `AppData\Local` + dataDir[len(la):]
	}

	activeSummary := ""
	if pacOpen {
		if st := queryTrayStatus(dataDir, cfg.PACPort); st != nil {
			if s, ok := st["endpoint"].(string); ok {
				activeSummary = s
			}
		}
	}
	var disc happ.HappDiscovery
	if cfg.EndpointURL == "" && cfg.SubscriptionURL == "" {
		disc = happ.FindHappConfig()
	}

	return InitialState{
		Version:        config.AppVersion,
		DataDir:        dataDir,
		ShortDataDir:   shortDir,
		Installed:      installed,
		Config:         cfg,
		Presets:        happ.SmartDNSPresets(),
		ProxyRunning:   pOpen,
		PACRunning:     pacOpen,
		ActiveEndpoint: activeSummary,
		AntigravityOK:  len(patcher.FindAntigravityRoots()) > 0,
		OpenCodeOK:     patcher.FindOpenCodeCLI() != "" || patcher.FindOpenCodeGUI() != "",
		BypassWarnings: patcher.DetectConflictingBypass(),
		HappDiscovery:  disc,
	}
}

func (a *App) DetectHapp() happ.HappDiscovery {
	return happ.FindHappConfig()
}

type SmartDNSTestSummary struct {
	OK          bool                         `json:"ok"`
	SelectedURL string                       `json:"selectedUrl"`
	Summary     string                       `json:"summary"`
	Results     []engine.SmartDNSProbeResult `json:"results"`
	Error       string                       `json:"error,omitempty"`
}

func (a *App) TestSmartDNS(customInput string) SmartDNSTestSummary {
	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Second)
	defer cancel()

	in := strings.TrimSpace(customInput)
	var cands []happ.Endpoint
	if in == "" || strings.EqualFold(in, "smartdns://auto") || strings.EqualFold(in, "auto") {
		for _, pr := range happ.SmartDNSPresets() {
			if ep, ok := happ.ParseSmartDNSURL(pr.URL); ok {
				cands = append(cands, ep)
			}
		}
	} else {
		ep, ok := happ.ParseSmartDNSURL(in)
		if !ok {
			return SmartDNSTestSummary{OK: false, Error: "Некорректный адрес SmartDNS (нужен https://.../dns-query, tls://... или IP)"}
		}
		if ep.IsAuto {
			for _, pr := range happ.SmartDNSPresets() {
				if p, ok := happ.ParseSmartDNSURL(pr.URL); ok {
					cands = append(cands, p)
				}
			}
		} else {
			cands = append(cands, ep)
		}
	}

	res, err := engine.ProbeSmartDNS(ctx, cands)
	if err != nil {
		return SmartDNSTestSummary{OK: false, Error: err.Error()}
	}
	var best *engine.SmartDNSProbeResult
	working := 0
	for i := range res {
		r := &res[i]
		if r.Score >= 2 {
			working++
		}
		if r.Score > 0 && (best == nil || r.Score > best.Score || (r.Score == best.Score && r.LatencyMs < best.LatencyMs)) {
			best = r
		}
	}
	if best == nil {
		return SmartDNSTestSummary{
			OK:      false,
			Results: res,
			Error:   "Ни один из проверенных SmartDNS не открывает AI-сервисы",
		}
	}
	return SmartDNSTestSummary{
		OK:          true,
		SelectedURL: best.URL,
		Summary:     fmt.Sprintf("%s (%d ms, рабочих %d/%d)", best.Name, best.LatencyMs, working, len(res)),
		Results:     res,
	}
}

type InstallRequest struct {
	Mode            string `json:"mode"` // "vless" | "smartdns" | "upstream"
	EndpointInput   string `json:"endpointInput"`
	SubscriptionURL string `json:"subscriptionUrl"`
	SmartDNSURL     string `json:"smartDnsUrl"`
	Tray            bool   `json:"tray"`
	Antigravity     bool   `json:"antigravity"`
	BrowserPAC      bool   `json:"browserPac"`
	OpenCode        bool   `json:"openCode"`
	ClaudeDesktop   bool   `json:"claudeDesktop"`
	ClaudeAPIKey    string `json:"claudeApiKey"`
	ClaudeBaseURL   string `json:"claudeBaseUrl"`
	ClaudeScheme    string `json:"claudeScheme"`
}

type ActionResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Issues  int    `json:"issues"`
}

func (a *App) Install(req InstallRequest) ActionResult {
	a.pendingSelfDelete = false
	cfg, dataDir, err := config.Load()
	if err != nil {
		return ActionResult{OK: false, Message: err.Error(), Issues: 1}
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return ActionResult{OK: false, Message: err.Error(), Issues: 1}
	}

	a.log("== Установка GravProxy v" + config.AppVersion)
	a.log("  [INFO] Каталог данных: " + dataDir)
	for _, sk := range []string{"files", "tray", "patch", "opencode", "claude", "pac", "tests"} {
		a.setStep(sk, "idle", "")
	}
	a.setProgress(10, "Подготовка...")
	a.setStep("files", "active", "...")

	for _, warn := range patcher.DetectConflictingBypass() {
		a.log("  [WARN] " + warn)
	}

	// Resolve endpoint according to selected mode.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cfg.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if cfg.Mode == "" {
		cfg.Mode = "vless"
	}
	cfg.Components = config.Components{
		Tray:          req.Tray,
		Antigravity:   req.Antigravity,
		BrowserPAC:    req.BrowserPAC,
		OpenCode:      req.OpenCode,
		ClaudeDesktop: req.ClaudeDesktop,
	}
	cfg.ClaudeBaseURL = strings.TrimSpace(req.ClaudeBaseURL)
	cfg.ClaudeAuthScheme = req.ClaudeScheme

	switch cfg.Mode {
	case "smartdns":
		raw := strings.TrimSpace(req.SmartDNSURL)
		if raw == "" {
			raw = "smartdns://auto"
		}
		a.log("== Подбор SmartDNS (" + raw + ")")
		a.setProgress(25, "Проверка SmartDNS...")
		if strings.EqualFold(raw, "smartdns://auto") || strings.EqualFold(raw, "auto") {
			ep, _, err := engine.ResolveSmartDNSAuto(ctx, "")
			if err != nil {
				a.log("  [FAIL] " + err.Error())
				a.setStep("files", "fail", "ошибка")
				return ActionResult{OK: false, Message: err.Error(), Issues: 1}
			}
			cfg.SmartDNSURL = ep.Raw
			a.log("  [OK] Выбран SmartDNS: " + ep.Summary())
		} else {
			ep, ok := happ.ParseSmartDNSURL(raw)
			if !ok {
				msg := "Некорректный адрес SmartDNS"
				a.log("  [FAIL] " + msg)
				a.setStep("files", "fail", "ошибка")
				return ActionResult{OK: false, Message: msg, Issues: 1}
			}
			cfg.SmartDNSURL = ep.Raw
			a.log("  [OK] SmartDNS: " + ep.Summary())
		}

	case "upstream":
		ep, ok := happ.ParseUpstreamProxyURL(req.EndpointInput)
		if !ok {
			msg := "Нужна ссылка socks5://... или http://..."
			a.log("  [FAIL] " + msg)
			a.setStep("files", "fail", "ошибка")
			return ActionResult{OK: false, Message: msg, Issues: 1}
		}
		cfg.EndpointURL = ep.Raw
		a.log("  [OK] Upstream прокси: " + ep.Summary())

	default:
		cfg.Mode = "vless"
		a.log("== Поиск и проверка VLESS / Happ")
		a.setProgress(25, "Проверка VLESS-узлов...")
		in := strings.TrimSpace(req.EndpointInput)
		subIn := strings.TrimSpace(req.SubscriptionURL)

		if ep, ok := happ.ParseProxyEndpointURL(in); ok {
			cfg.EndpointURL = ep.Raw
			if ep.Kind == happ.KindUpstream {
				cfg.Mode = "upstream"
			}
			a.log("  [OK] Выбран узел: " + ep.Summary())
		} else {
			if subIn == "" && strings.HasPrefix(strings.ToLower(in), "http") {
				subIn = in
			}
			if subIn == "" {
				disc := happ.FindHappConfig()
				if disc.EndpointURL != "" {
					cfg.EndpointURL = disc.EndpointURL
					a.log("  [OK] Найдено в " + disc.Source)
				} else if disc.SubURL != "" {
					subIn = disc.SubURL
					a.log("  [INFO] Найдена подписка в " + disc.Source)
				} else if disc.EncryptedOnly {
					a.log("  [WARN] Конфиг Happ зашифрован (subs.db); вставьте vless:// или ссылку подписки вручную")
				}
			}
			if subIn != "" {
				cfg.SubscriptionURL = subIn
				sub := happ.FetchSubscriptionDetailed(ctx, subIn)
				if len(sub.Nodes) == 0 {
					msg := "Не удалось получить узлы подписки: " + sub.Error
					a.log("  [FAIL] " + msg)
					a.setStep("files", "fail", "ошибка")
					return ActionResult{OK: false, Message: msg, Issues: 1}
				}
				a.log(fmt.Sprintf("  [INFO] Найдено узлов: %d (%s), проверяю Google GeoIP...", len(sub.Nodes), sub.Via))
				probes, err := engine.ProbeVlessNodes(ctx, sub.Nodes)
				if err != nil || len(probes) == 0 {
					msg := "Не удалось проверить узлы подписки"
					if err != nil {
						msg = err.Error()
					}
					a.log("  [FAIL] " + msg)
					a.setStep("files", "fail", "ошибка")
					return ActionResult{OK: false, Message: msg, Issues: 1}
				}
				chosen := probes[0]
				if !chosen.Reachable {
					msg := fmt.Sprintf("Не удалось подключиться ни к одному из %d VLESS-узлов подписки (%s)", len(sub.Nodes), chosen.Diagnostics)
					a.log("  [FAIL] " + msg)
					a.setStep("files", "fail", "ошибка")
					return ActionResult{OK: false, Message: msg, Issues: 1}
				}
				cleanCnt, gemCnt := 0, 0
				for _, p := range probes {
					if p.GoogleClean {
						cleanCnt++
					}
					if p.GeminiClean {
						gemCnt++
					}
				}
				cfg.EndpointURL = chosen.Endpoint.Raw
				a.log(fmt.Sprintf("  [OK] Выбран узел: %s (%s, %d ms, clean=%v, gemini=%v, nb=%v; чистых=%d, Gemini=%d)",
					chosen.Endpoint.Summary(), chosen.Country, chosen.LatencyMs, chosen.GoogleClean, chosen.GeminiClean, chosen.NotebookOk, cleanCnt, gemCnt))
			}
		}
		if cfg.EndpointURL == "" {
			msg := "Не найдена конфигурация VLESS / Happ. Вставьте ключ vless://, подписку https:// или выберите SmartDNS."
			a.log("  [FAIL] " + msg)
			a.setStep("files", "fail", "не задан")
			return ActionResult{OK: false, Message: msg, Issues: 1}
		}
	}

	if err := config.SaveToDir(dataDir, cfg); err != nil {
		a.log("  [FAIL] Ошибка сохранения config.json: " + err.Error())
		a.setStep("files", "fail", "ошибка")
		return ActionResult{OK: false, Message: err.Error(), Issues: 1}
	}

	// Stop running tray & legacy processes before updating GravProxy.exe in dataDir.
	a.setProgress(42, "Остановка предыдущего прокси...")
	if isLoopbackOpen(cfg.PACPort, 150*time.Millisecond) {
		_ = sendTrayControl(dataDir, cfg.PACPort, "/_ctl/stop")
		time.Sleep(350 * time.Millisecond)
	}
	if n := patcher.StopGravProxyTrayProcesses(dataDir); n > 0 {
		a.log(fmt.Sprintf("  [OK] Остановлен предыдущий процесс трея (%d)", n))
	}
	if n := patcher.StopLegacyAgProxyProcesses(); n > 0 {
		a.log(fmt.Sprintf("  [OK] Остановлены процессы v1.x (%d)", n))
	}
	waitPortsClosed(cfg.ProxyPort, cfg.PACPort, 1500*time.Millisecond)
	patcher.CleanupLegacyStartupEntries()

	// Deploy single GravProxy.exe into dataDir.
	a.setProgress(48, "Копирование GravProxy.exe...")
	installedExe := filepath.Join(dataDir, "GravProxy.exe")
	if err := copySelfExe(a.selfExe, installedExe); err != nil {
		a.log("  [WARN] Не удалось обновить копию в каталоге данных: " + err.Error())
	} else {
		a.log("  [OK] Установлен " + installedExe)
	}
	a.setStep("files", "ok", "готово")

	issues := 0
	if req.Tray {
		a.setProgress(55, "Запуск встроенного прокси и трея...")
		a.setStep("tray", "active", "...")
		if err := patcher.SetTrayAutostart(installedExe); err != nil {
			a.log("  [WARN] Автозапуск: " + err.Error())
		} else {
			a.log("  [OK] Настроен автозапуск GravProxy (--tray)")
		}
		if ensureTrayProxyUp(installedExe, dataDir, cfg.ProxyPort) {
			a.log(fmt.Sprintf("  [OK] Локальный прокси запущен: 127.0.0.1:%d", cfg.ProxyPort))
			a.setStep("tray", "ok", "готово")
		} else {
			a.log(fmt.Sprintf("  [FAIL] Не удалось запустить локальный прокси на :%d", cfg.ProxyPort))
			a.setStep("tray", "fail", "ошибка")
			issues++
		}
	} else {
		a.setStep("tray", "skip", "пропущено")
	}

	if closed := patcher.StopTargetApps(req.Antigravity, req.OpenCode, req.ClaudeDesktop); len(closed) > 0 {
		a.log("  [OK] Закрыты приложения перед патчем: " + strings.Join(closed, ", "))
	}

	if req.Antigravity {
		a.setProgress(65, "Патч Antigravity 2.0...")
		a.setStep("patch", "active", "...")
		p, alr, errs := patcher.PatchAntigravityBinaries(dataDir, nil)
		for _, e := range errs {
			a.log("  [FAIL] Antigravity: " + e)
			issues++
		}
		if len(errs) > 0 {
			a.setStep("patch", "fail", "ошибка")
		} else if p > 0 || alr > 0 {
			a.log(fmt.Sprintf("  [OK] Antigravity: пропатчено=%d, уже пропатчено=%d", p, alr))
			a.setStep("patch", "ok", "готово")
		} else {
			a.log("  [SKIP] Antigravity не найден")
			a.setStep("patch", "skip", "не найден")
		}
	} else {
		a.setStep("patch", "skip", "пропущено")
	}

	if req.OpenCode {
		a.setProgress(75, "Настройка OpenCode...")
		a.setStep("opencode", "active", "...")
		msg, err := patcher.ConfigureOpenCode(dataDir, installedExe, fmt.Sprintf("http://127.0.0.1:%d", cfg.ProxyPort))
		if err != nil {
			a.log("  [WARN] OpenCode: " + err.Error())
			a.setStep("opencode", "skip", "предупр.")
		} else {
			a.log("  [OK] " + msg)
			a.setStep("opencode", "ok", "готово")
		}
	} else {
		a.setStep("opencode", "skip", "пропущено")
	}

	if req.ClaudeDesktop {
		a.setProgress(82, "Настройка Claude Desktop...")
		a.setStep("claude", "active", "...")
		msg, err := patcher.ConfigureClaudeDesktop(dataDir, "", patcher.ClaudeOptions{
			ProxyURL:   fmt.Sprintf("http://127.0.0.1:%d", cfg.ProxyPort),
			APIKey:     req.ClaudeAPIKey,
			BaseURL:    req.ClaudeBaseURL,
			AuthScheme: req.ClaudeScheme,
		})
		if err != nil {
			a.log("  [FAIL] Claude Desktop: " + err.Error())
			a.setStep("claude", "fail", "ошибка")
			issues++
		} else {
			a.log("  [OK] " + msg)
			a.setStep("claude", "ok", "готово")
		}
	} else {
		a.setStep("claude", "skip", "пропущено")
	}

	if req.BrowserPAC || req.Antigravity || req.OpenCode {
		if changed, msg, err := patcher.ConfigureHappTunOnly(dataDir); err != nil {
			a.log("  [WARN] Happ: " + err.Error())
		} else if changed {
			a.log("  [OK] " + msg)
		}
	}

	pacStepOk := false
	if req.BrowserPAC {
		a.setProgress(88, "Регистрация Browser PAC...")
		a.setStep("pac", "active", "...")
		pacURL := pac.DefaultPACURL(cfg.PACPort)
		if err := pac.EnableSystemPAC(pacURL, dataDir, cfg.PACPort); err != nil {
			a.log("  [FAIL] PAC: " + err.Error())
			a.setStep("pac", "fail", "ошибка")
			issues++
		} else {
			a.log("  [OK] Browser PAC включён: " + pacURL)
			pacStepOk = true
		}
	}
	if req.Antigravity || req.OpenCode {
		cnt := patcher.PatchShortcuts(dataDir, installedExe, req.Antigravity, req.OpenCode)
		if cnt > 0 {
			a.log(fmt.Sprintf("  [OK] Обновлено ярлыков: %d", cnt))
			pacStepOk = true
		}
	}
	if req.BrowserPAC || req.Antigravity || req.OpenCode {
		if pacStepOk {
			a.setStep("pac", "ok", "готово")
		}
	} else {
		a.setStep("pac", "skip", "пропущено")
	}

	a.setProgress(94, "Проверка подключения...")
	a.setStep("tests", "active", "...")
	if isLoopbackOpen(cfg.ProxyPort, 300*time.Millisecond) {
		items := a.RunConnectivityTest()
		reqOk, reqTot := 0, 0
		for _, it := range items {
			if !it.Optional {
				reqTot++
				if it.OK {
					reqOk++
				}
			}
		}
		if reqTot > 0 && reqOk == reqTot {
			a.setStep("tests", "ok", fmt.Sprintf("%d/%d OK", reqOk, reqTot))
		} else if reqOk > 0 {
			a.setStep("tests", "ok", fmt.Sprintf("%d/%d OK", reqOk, reqTot))
		} else {
			a.setStep("tests", "fail", "ошибка")
		}
	} else {
		a.setStep("tests", "skip", "пропущено")
	}

	a.setProgress(100, "Готово")
	if issues > 0 {
		return ActionResult{OK: false, Message: fmt.Sprintf("Завершено с замечаниями (%d)", issues), Issues: issues}
	}
	return ActionResult{OK: true, Message: "Установка завершена успешно"}
}

func (a *App) Repatch(req InstallRequest) ActionResult {
	cfg, dataDir, _ := config.Load()
	installedExe := filepath.Join(dataDir, "GravProxy.exe")
	if _, err := os.Stat(installedExe); err != nil {
		installedExe = a.selfExe
	}
	issues := 0
	a.log("== Перепатч приложений")
	if closed := patcher.StopTargetApps(req.Antigravity, req.OpenCode, req.ClaudeDesktop); len(closed) > 0 {
		a.log("  [OK] Закрыты приложения перед патчем: " + strings.Join(closed, ", "))
	}

	if req.Antigravity {
		p, alr, errs := patcher.PatchAntigravityBinaries(dataDir, nil)
		for _, e := range errs {
			a.log("  [FAIL] " + e)
			issues++
		}
		a.log(fmt.Sprintf("  [OK] Antigravity: пропатчено=%d, уже=%d", p, alr))
	}
	if req.OpenCode {
		if msg, err := patcher.ConfigureOpenCode(dataDir, installedExe, fmt.Sprintf("http://127.0.0.1:%d", cfg.ProxyPort)); err == nil {
			a.log("  [OK] " + msg)
		}
	}
	if req.ClaudeDesktop {
		if msg, err := patcher.ConfigureClaudeDesktop(dataDir, "", patcher.ClaudeOptions{
			ProxyURL:   fmt.Sprintf("http://127.0.0.1:%d", cfg.ProxyPort),
			APIKey:     req.ClaudeAPIKey,
			BaseURL:    req.ClaudeBaseURL,
			AuthScheme: req.ClaudeScheme,
		}); err != nil {
			a.log("  [FAIL] Claude Desktop: " + err.Error())
			issues++
		} else {
			a.log("  [OK] " + msg)
		}
	}
	if req.BrowserPAC || req.Antigravity || req.OpenCode {
		if changed, msg, err := patcher.ConfigureHappTunOnly(dataDir); err != nil {
			a.log("  [WARN] Happ: " + err.Error())
		} else if changed {
			a.log("  [OK] " + msg)
		}
	}
	if req.BrowserPAC {
		pacURL := pac.DefaultPACURL(cfg.PACPort)
		if err := pac.EnableSystemPAC(pacURL, dataDir, cfg.PACPort); err != nil {
			a.log("  [FAIL] PAC: " + err.Error())
			issues++
		} else {
			a.log("  [OK] Browser PAC включён: " + pacURL)
		}
	}
	if cnt := patcher.PatchShortcuts(dataDir, installedExe, req.Antigravity, req.OpenCode); cnt > 0 {
		a.log(fmt.Sprintf("  [OK] Ярлыков обновлено: %d", cnt))
	}
	return ActionResult{OK: issues == 0, Issues: issues, Message: "Перепатч завершён"}
}

func (a *App) RunConnectivityTest() []engine.ConnectivityCheckItem {
	cfg, dataDir, _ := config.Load()
	if !isLoopbackOpen(cfg.ProxyPort, 250*time.Millisecond) {
		a.log(fmt.Sprintf("  [FAIL] Порт 127.0.0.1:%d не отвечает", cfg.ProxyPort))
		return nil
	}
	a.log("== Проверка подключения через 127.0.0.1:" + fmt.Sprintf("%d", cfg.ProxyPort))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	items := engine.TestConnectivity(ctx, fmt.Sprintf("http://127.0.0.1:%d", cfg.ProxyPort), cfg.Mode == "smartdns")
	for _, it := range items {
		tag := "FAIL"
		if it.OK {
			tag = "OK"
		} else if it.Optional {
			tag = "SKIP"
		}
		a.log(fmt.Sprintf("  [%s] %-24s %s (%d ms)", tag, it.Name, it.Detail, it.LatencyMs))
	}
	if cfg.Components.BrowserPAC {
		pacURL := pac.DefaultPACURL(cfg.PACPort)
		st := pac.QuerySystemPACState(cfg.PACPort)
		if st.OurPACActive {
			a.log(fmt.Sprintf("  [OK]   %-24s активен (%s)", "Browser PAC", pacURL))
		} else if st.ExternalProxyActive {
			msg := "переопределён внешним VPN/прокси"
			if patcher.IsHappTunAvailable() && patcher.IsHappSystemProxyEnabled() {
				msg = "переопределён Happ System Proxy — нажмите «Перепатчить» для перевода Happ в TUN"
			}
			a.log(fmt.Sprintf("  [WARN] %-24s %s", "Browser PAC", msg))
		} else if restored, err := pac.EnsureSystemPAC(pacURL, dataDir, cfg.PACPort); err != nil {
			a.log(fmt.Sprintf("  [FAIL] %-24s %v", "Browser PAC", err))
		} else if restored {
			a.log(fmt.Sprintf("  [OK]   %-24s восстановлен (был сброшен VPN/системой)", "Browser PAC"))
		}
	}
	return items
}

func (a *App) Uninstall() ActionResult {
	cfg, dataDir, _ := config.Load()
	a.log("== Удаление GravProxy")
	if isLoopbackOpen(cfg.PACPort, 150*time.Millisecond) {
		_ = sendTrayControl(dataDir, cfg.PACPort, "/_ctl/stop")
		time.Sleep(350 * time.Millisecond)
	}
	_ = patcher.StopGravProxyTrayProcesses(dataDir)
	if n := patcher.StopLegacyAgProxyProcesses(); n > 0 {
		a.log(fmt.Sprintf("  [OK] Остановлены процессы v1.x (%d)", n))
	}
	if waitPortsClosed(cfg.ProxyPort, cfg.PACPort, 2*time.Second) {
		a.log("  [OK] Локальный прокси и значок в трее остановлены")
	} else {
		a.log("  [WARN] Порты локального прокси не освободились полностью")
	}

	patcher.RemoveTrayAutostart()
	patcher.CleanupLegacyStartupEntries()
	_ = pac.RestoreSystemPAC(dataDir, cfg.PACPort)
	a.log("  [OK] Системный Browser PAC снят")

	if closed := patcher.StopTargetApps(true, true, true); len(closed) > 0 {
		a.log("  [OK] Закрыты приложения перед откатом: " + strings.Join(closed, ", "))
	}

	resCnt, errs := patcher.RestoreAntigravityBinaries(dataDir)
	for _, e := range errs {
		a.log("  [WARN] Откат Antigravity: " + e)
	}
	a.log(fmt.Sprintf("  [OK] Восстановлено бинарников Antigravity: %d", resCnt))

	scCnt := patcher.RestoreShortcuts(dataDir)
	a.log(fmt.Sprintf("  [OK] Восстановлено ярлыков: %d", scCnt))

	patcher.RemoveOpenCode(dataDir)
	_, _ = patcher.RemoveClaudeDesktopProxy("")
	if patcher.CleanupEnvVars() {
		a.log("  [OK] Очищены переменные среды AG_PROXY_*")
	}
	a.log("  [OK] Настройки OpenCode и Claude Desktop очищены")
	if restored, err := patcher.RestoreHappSettings(dataDir); err == nil && restored {
		a.log("  [OK] Настройки Happ (System Proxy / TUN) восстановлены")
	}

	issues := len(errs)
	if dataDir != "" {
		newSelf, moved := relocateSelfForUninstall(a.selfExe, dataDir)
		if moved {
			a.selfExe = newSelf
			a.cleanupTempExe = newSelf
		}
		if wd, wdErr := os.Getwd(); wdErr == nil && isInsideDir(wd, dataDir) {
			_ = os.Chdir(os.TempDir())
		}

		if isInsideDir(a.selfExe, dataDir) {
			if entries, err := os.ReadDir(dataDir); err == nil {
				for _, ent := range entries {
					p := filepath.Join(dataDir, ent.Name())
					if strings.EqualFold(filepath.Clean(p), filepath.Clean(a.selfExe)) {
						continue
					}
					_ = os.RemoveAll(p)
				}
			}
			a.pendingSelfDelete = true
			a.log("  [OK] Данные удалены; GravProxy.exe будет удалён после закрытия окна")
		} else {
			var rmErr error
			for i := 0; i < 5; i++ {
				rmErr = os.RemoveAll(dataDir)
				if rmErr == nil {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			if rmErr != nil {
				a.log("  [WARN] Не удалось полностью удалить каталог данных: " + rmErr.Error())
				issues++
			} else {
				a.log("  [OK] Каталог данных удалён: " + dataDir)
			}
		}
	}

	a.setProgress(100, "Удалено")
	if issues > 0 {
		return ActionResult{OK: false, Issues: issues, Message: "Откат завершён с замечаниями"}
	}
	return ActionResult{OK: true, Issues: 0, Message: "Откат завершён"}
}

func waitPortsClosed(proxyPort, pacPort int, maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	for {
		pOpen := isLoopbackOpen(proxyPort, 120*time.Millisecond)
		pacOpen := isLoopbackOpen(pacPort, 120*time.Millisecond)
		if !pOpen && !pacOpen {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(120 * time.Millisecond)
	}
}

func relocateSelfForUninstall(selfExe, dataDir string) (string, bool) {
	if !isInsideDir(selfExe, dataDir) {
		return selfExe, false
	}
	tmpDel := filepath.Join(os.TempDir(), fmt.Sprintf("gravproxy-uninstalled-%d-%d.exe", os.Getpid(), time.Now().UnixNano()))
	if err := os.Rename(selfExe, tmpDel); err == nil {
		return tmpDel, true
	}
	return selfExe, false
}

func isInsideDir(child, parent string) bool {
	if strings.TrimSpace(child) == "" || strings.TrimSpace(parent) == "" {
		return false
	}
	c := strings.ToLower(filepath.Clean(child))
	p := strings.ToLower(filepath.Clean(parent))
	return c == p || strings.HasPrefix(c, p+string(filepath.Separator))
}

func (a *App) SaveTheme(theme string) {
	cfg, dataDir, err := config.Load()
	if err != nil {
		return
	}
	cfg.Theme = theme
	_ = config.SaveToDir(dataDir, cfg)
}

func (a *App) CheckUpdates() (*updater.ReleaseInfo, error) {
	cfg, _, _ := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return updater.CheckLatest(ctx, config.GitHubRepo, config.AppVersion, fmt.Sprintf("http://127.0.0.1:%d", cfg.ProxyPort))
}

func (a *App) DownloadAndApplyUpdate(info updater.ReleaseInfo) error {
	cfg, _, _ := config.Load()
	a.log("== Загрузка обновления " + info.Tag)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.ProxyPort)
	dlPath, err := updater.DownloadAndVerify(ctx, &info, proxyURL, 1048576, func(p updater.DownloadProgress) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "update:progress", p)
		}
	})
	if err != nil {
		a.log("  [FAIL] " + err.Error())
		return err
	}
	a.log("  [OK] SHA-256 подтверждён, перезапуск...")
	if err := updater.ApplyUpdateAndRestart(dlPath); err != nil {
		return err
	}
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
	return nil
}

func (a *App) OpenDataFolder() {
	_, dataDir, _ := config.Load()
	if dataDir != "" {
		_ = exec.Command("explorer.exe", dataDir).Start()
	}
}

func (a *App) OpenExternalURL(u string) {
	low := strings.ToLower(strings.TrimSpace(u))
	if strings.HasPrefix(low, "https://") {
		_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", u).Start()
	}
}

func copySelfExe(src, dst string) error {
	if strings.EqualFold(filepath.Clean(src), filepath.Clean(dst)) {
		return nil
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return patcher.WriteFileAtomicWithRenameFallback(dst, raw)
}

func sendTrayControl(dataDir string, pacPort int, path string) error {
	tok := config.ReadControlToken(dataDir)
	if tok == "" {
		return fmt.Errorf("missing token")
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", pacPort, path), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-GravProxy-Token", tok)
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func queryTrayStatus(dataDir string, pacPort int) map[string]any {
	tok := config.ReadControlToken(dataDir)
	if tok == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/_ctl/status", pacPort), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("X-GravProxy-Token", tok)
	c := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := c.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return m
}
