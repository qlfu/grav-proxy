package tray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/qlfu/grav-proxy/assets"
	"github.com/qlfu/grav-proxy/internal/config"
	"github.com/qlfu/grav-proxy/internal/engine"
	"github.com/qlfu/grav-proxy/internal/happ"
	"github.com/qlfu/grav-proxy/internal/pac"
	"github.com/qlfu/grav-proxy/internal/updater"
)

const (
	wmAppTray       = 0x8000 + 1
	wmCommand       = 0x0111
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205

	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nifMessage    = 0x00000001
	nifIcon       = 0x00000002
	nifTip        = 0x00000004
	nifInfo       = 0x00000010
	niifInfo      = 0x00000001

	mfString     = 0x00000000
	mfSeparator  = 0x00000800
	mfGrayed     = 0x00000001
	tpmRightBtn  = 0x0002
	tpmBottomAln = 0x0020

	imageIcon    = 1
	lrLoadFile   = 0x0010
	lrDefaultSz  = 0x0040

	idStatus     = 100
	idToggle     = 101
	idRepick     = 102
	idOpenUI     = 103
	idLaunchAG   = 104
	idLaunchOC   = 105
	idLaunchCl   = 106
	idOpenGem    = 107
	idOpenNB     = 108
	idCheckUpd   = 110
	idApplyUpd   = 111
	idExit       = 112
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	shell32                 = windows.NewLazySystemDLL("shell32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenuEx    = user32.NewProc("TrackPopupMenuEx")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct {
	x int32
	y int32
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

type Service struct {
	dataDir    string
	selfExe    string
	token      string
	cfg        config.Config
	mgr         *engine.Manager
	pacSrv      *pac.Server
	hwnd        atomic.Uintptr
	hIcon       uintptr
	iconRemoved atomic.Bool
	paused      atomic.Bool
	mu         sync.Mutex
	activeInfo string
	availUpd   *updater.ReleaseInfo
	stopCh     chan struct{}
}

var activeService *Service

func Run(selfExe string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	mutName, _ := windows.UTF16PtrFromString(`Local\GravProxyTrayMutex`)
	hMut, err := windows.CreateMutex(nil, false, mutName)
	if err == nil {
		defer windows.CloseHandle(hMut)
		if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
			return nil
		}
	}

	cfg, dataDir, err := config.Load()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}
	icoPath := filepath.Join(dataDir, "app.ico")
	_ = os.WriteFile(icoPath, assets.AppIconICO, 0644)

	tok, err := config.EnsureControlToken(dataDir)
	if err != nil {
		return err
	}

	svc := &Service{
		dataDir: dataDir,
		selfExe: selfExe,
		token:   tok,
		cfg:     cfg,
		mgr:     engine.NewManager(),
		stopCh:  make(chan struct{}),
	}
	activeService = svc

	if err := svc.startProxyFromConfig(); err != nil {
		svc.setActiveInfo("Ошибка старта: " + err.Error())
	}

	svc.pacSrv = pac.NewServer(cfg.PACPort, "127.0.0.1", cfg.ProxyPort, tok, func() bool {
		return !svc.paused.Load() && svc.mgr.Running()
	}, pac.ControlHooks{
		Status: func() map[string]any {
			ep, pt, running, lastErr := svc.mgr.Active()
			return map[string]any{
				"active":   running && !svc.paused.Load(),
				"running":  running,
				"paused":   svc.paused.Load(),
				"port":     pt,
				"mode":     string(ep.Kind),
				"endpoint": ep.Summary(),
				"lastErr":  lastErr,
				"version":  config.AppVersion,
			}
		},
		Toggle: func() (bool, error) {
			return svc.togglePause()
		},
		Reload: func() error {
			c, _, _ := config.Load()
			svc.mu.Lock()
			svc.cfg = c
			svc.mu.Unlock()
			return svc.startProxyFromConfig()
		},
		Recheck: func() error {
			return svc.failoverOrRepick()
		},
		Stop: func() {
			svc.shutdown()
		},
	})
	_ = svc.pacSrv.Start()
	defer svc.pacSrv.Stop()
	defer svc.mgr.Stop()

	go svc.backgroundLoop()

	return svc.runMessageLoop(icoPath)
}

func (s *Service) startProxyFromConfig() error {
	s.mu.Lock()
	c := s.cfg
	s.mu.Unlock()

	ep, err := s.resolveEndpointForConfig(c, "")
	if err != nil {
		return err
	}
	if err := s.mgr.Start(ep, c.ProxyPort); err != nil {
		return err
	}
	s.paused.Store(false)
	s.setActiveInfo(ep.Summary())
	s.updateTrayTooltip("GravProxy v" + config.AppVersion + " · " + ep.Summary())
	return nil
}

func (s *Service) resolveEndpointForConfig(c config.Config, exclude string) (happ.Endpoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	switch c.Mode {
	case "smartdns":
		raw := strings.TrimSpace(c.SmartDNSURL)
		if raw == "" || strings.EqualFold(raw, "smartdns://auto") {
			ep, _, err := engine.ResolveSmartDNSAuto(ctx, exclude)
			return ep, err
		}
		ep, ok := happ.ParseSmartDNSURL(raw)
		if !ok {
			return happ.Endpoint{}, fmt.Errorf("некорректный адрес SmartDNS: %s", raw)
		}
		if ep.IsAuto {
			ep2, _, err := engine.ResolveSmartDNSAuto(ctx, exclude)
			return ep2, err
		}
		return ep, nil

	case "upstream":
		ep, ok := happ.ParseUpstreamProxyURL(c.EndpointURL)
		if !ok {
			return happ.Endpoint{}, fmt.Errorf("некорректный адрес upstream-прокси")
		}
		return ep, nil

	default:
		if exclude == "" && strings.TrimSpace(c.EndpointURL) != "" {
			if ep, ok := happ.ParseProxyEndpointURL(c.EndpointURL); ok {
				return ep, nil
			}
		}
		if strings.TrimSpace(c.SubscriptionURL) != "" {
			sub := happ.FetchSubscriptionDetailed(ctx, c.SubscriptionURL)
			if len(sub.Nodes) > 0 {
				probes, err := engine.ProbeVlessNodes(ctx, sub.Nodes)
				if err == nil && len(probes) > 0 {
					var chosen *engine.VlessProbeResult
					for i := range probes {
						if !probes[i].Reachable {
							continue
						}
						if exclude == "" || probes[i].Endpoint.Raw != exclude {
							chosen = &probes[i]
							break
						}
					}
					if chosen == nil && probes[0].Reachable {
						chosen = &probes[0]
					}
					if chosen != nil {
						if exclude != "" && chosen.Endpoint.Raw != c.EndpointURL {
							c.EndpointURL = chosen.Endpoint.Raw
							_ = config.SaveToDir(s.dataDir, c)
							s.mu.Lock()
							s.cfg = c
							s.mu.Unlock()
						}
						return chosen.Endpoint, nil
					}
				}
			}
		}
		if exclude != "" && strings.TrimSpace(c.EndpointURL) != "" {
			if ep, ok := happ.ParseProxyEndpointURL(c.EndpointURL); ok {
				return ep, nil
			}
		}
		disc := happ.FindHappConfig()
		if disc.EndpointURL != "" {
			if ep, ok := happ.ParseVlessURL(disc.EndpointURL); ok {
				return ep, nil
			}
		}
		return happ.Endpoint{}, fmt.Errorf("VLESS/SmartDNS конфигурация не задана")
	}
}

func (s *Service) failoverOrRepick() error {
	ep, _, _, _ := s.mgr.Active()
	exclude := ep.Raw
	s.mu.Lock()
	c := s.cfg
	s.mu.Unlock()
	nextEp, err := s.resolveEndpointForConfig(c, exclude)
	if err != nil {
		return err
	}
	if err := s.mgr.Start(nextEp, c.ProxyPort); err != nil {
		return err
	}
	s.setActiveInfo(nextEp.Summary())
	s.updateTrayTooltip("GravProxy · " + nextEp.Summary())
	s.showBalloon("GravProxy переключён", nextEp.Summary())
	return nil
}

func (s *Service) togglePause() (bool, error) {
	if s.paused.Load() {
		err := s.startProxyFromConfig()
		return err == nil, err
	}
	s.paused.Store(true)
	s.mgr.Stop()
	s.setActiveInfo("Приостановлено")
	s.updateTrayTooltip("GravProxy · Приостановлено")
	return false, nil
}

func (s *Service) backgroundLoop() {
	updTimer := time.NewTimer(25 * time.Second)
	healthTicker := time.NewTicker(4 * time.Minute)
	defer updTimer.Stop()
	defer healthTicker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-updTimer.C:
			s.checkUpdates(false)
			updTimer.Reset(6 * time.Hour)
		case <-healthTicker.C:
			if s.paused.Load() || !s.mgr.Running() {
				continue
			}
			ep, pt, _, _ := s.mgr.Active()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			items := engine.TestConnectivity(ctx, fmt.Sprintf("http://127.0.0.1:%d", pt), ep.Kind == happ.KindSmartDNS)
			cancel()
			agOK := false
			for _, it := range items {
				if it.Name == "Antigravity 2.0" {
					agOK = it.OK
				}
			}
			if !agOK {
				_ = s.failoverOrRepick()
			}
		}
	}
}

func (s *Service) checkUpdates(interactive bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", s.cfg.ProxyPort)
	info, err := updater.CheckLatest(ctx, config.GitHubRepo, config.AppVersion, proxyURL)
	if err != nil {
		if interactive {
			s.showBalloon("Проверка обновлений", "Ошибка: "+err.Error())
		}
		return
	}
	if info.IsNewer {
		s.mu.Lock()
		s.availUpd = info
		s.mu.Unlock()
		s.showBalloon("Доступно обновление GravProxy "+info.Tag, "Откройте меню трея → «Установить обновление»")
	} else if interactive {
		s.showBalloon("GravProxy актуален", fmt.Sprintf("Установлена последняя версия v%s", config.AppVersion))
	}
}

func (s *Service) runAndInstallUpdate() {
	s.mu.Lock()
	info := s.availUpd
	s.mu.Unlock()
	if info == nil {
		s.checkUpdates(true)
		return
	}
	s.showBalloon("Обновление GravProxy", "Скачивание "+info.Tag+"...")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", s.cfg.ProxyPort)
	dlPath, err := updater.DownloadAndVerify(ctx, info, proxyURL, 1048576, nil)
	if err != nil {
		s.showBalloon("Ошибка обновления", err.Error())
		return
	}
	if err := updater.ApplyUpdateAndRestart(dlPath, "--tray"); err == nil {
		s.shutdown()
	}
}

func (s *Service) setActiveInfo(info string) {
	s.mu.Lock()
	s.activeInfo = info
	s.mu.Unlock()
}

func (s *Service) runMessageLoop(icoPath string) error {
	hInst, _, _ := procGetModuleHandleW.Call(0)
	clsName, _ := windows.UTF16PtrFromString("GravProxyTrayWnd")
	wndProc := syscall.NewCallback(trayWndProc)

	icoPtr, _ := windows.UTF16PtrFromString(icoPath)
	hIcon, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(icoPtr)), imageIcon, 0, 0, lrLoadFile|lrDefaultSz)
	s.hIcon = hIcon
	defer func() {
		if hIcon != 0 {
			_, _, _ = procDestroyIcon.Call(hIcon)
		}
	}()

	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   wndProc,
		hInstance:     hInst,
		lpszClassName: clsName,
	}
	_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(clsName)),
		0, 0, 0, 0, 0, 0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return err
	}
	s.hwnd.Store(hwnd)
	s.addTrayIcon("GravProxy v" + config.AppVersion)
	defer s.removeTrayIcon()

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func trayWndProc(hwnd uintptr, umsg uint32, wParam, lParam uintptr) uintptr {
	s := activeService
	switch umsg {
	case wmAppTray:
		switch uint32(lParam) {
		case wmLButtonDblClk:
			if s != nil {
				_ = exec.Command(s.selfExe).Start()
			}
		case wmRButtonUp:
			if s != nil {
				s.showContextMenu()
			}
		}
		return 0
	case wmCommand:
		if s != nil {
			s.handleMenuCommand(int(wParam & 0xffff))
		}
		return 0
	case wmClose:
		if s != nil {
			s.removeTrayIcon()
		}
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		if s != nil {
			s.removeTrayIcon()
		}
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(umsg), wParam, lParam)
	return ret
}

func (s *Service) handleMenuCommand(id int) {
	switch id {
	case idToggle:
		_, _ = s.togglePause()
	case idRepick:
		go func() { _ = s.failoverOrRepick() }()
	case idOpenUI:
		_ = exec.Command(s.selfExe).Start()
	case idLaunchAG:
		_ = exec.Command(s.selfExe, "--launch", "antigravity").Start()
	case idLaunchOC:
		_ = exec.Command(s.selfExe, "--launch", "opencode").Start()
	case idLaunchCl:
		cmd := exec.Command("cmd.exe", "/c", "start", "", "Claude:")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		_ = cmd.Start()
	case idOpenGem:
		_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", "https://gemini.google.com/app").Start()
	case idOpenNB:
		_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", "https://notebooklm.google.com/").Start()
	case idCheckUpd:
		go s.checkUpdates(true)
	case idApplyUpd:
		go s.runAndInstallUpdate()
	case idExit:
		s.shutdown()
	}
}

func (s *Service) showContextMenu() {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	s.mu.Lock()
	info := s.activeInfo
	upd := s.availUpd
	s.mu.Unlock()
	if info == "" {
		info = "Активен"
	}
	toggleText := "Приостановить прокси"
	if s.paused.Load() {
		toggleText = "Включить прокси"
	}

	appendMenuItem(hMenu, mfString|mfGrayed, idStatus, "GravProxy v"+config.AppVersion+" · "+info)
	appendMenuItem(hMenu, mfSeparator, 0, "")
	appendMenuItem(hMenu, mfString, idToggle, toggleText)
	appendMenuItem(hMenu, mfString, idRepick, "Переподключить / подобрать узел")
	appendMenuItem(hMenu, mfString, idOpenUI, "Открыть окно GravProxy")
	appendMenuItem(hMenu, mfSeparator, 0, "")
	appendMenuItem(hMenu, mfString, idLaunchAG, "Открыть Antigravity 2.0")
	appendMenuItem(hMenu, mfString, idLaunchOC, "Открыть OpenCode")
	appendMenuItem(hMenu, mfString, idLaunchCl, "Открыть Claude Desktop")
	appendMenuItem(hMenu, mfString, idOpenGem, "Открыть Gemini")
	appendMenuItem(hMenu, mfString, idOpenNB, "Открыть NotebookLM")
	appendMenuItem(hMenu, mfSeparator, 0, "")
	if upd != nil {
		appendMenuItem(hMenu, mfString, idApplyUpd, "Установить обновление "+upd.Tag)
	} else {
		appendMenuItem(hMenu, mfString, idCheckUpd, "Проверить обновления")
	}
	appendMenuItem(hMenu, mfString, idExit, "Выход")

	h := s.hwnd.Load()
	var pt point
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	_, _, _ = procSetForegroundWindow.Call(h)
	_, _, _ = procTrackPopupMenuEx.Call(hMenu, tpmRightBtn|tpmBottomAln, uintptr(pt.x), uintptr(pt.y), h, 0)
}

func appendMenuItem(hMenu uintptr, flags, id uint32, text string) {
	if flags == mfSeparator {
		_, _, _ = procAppendMenuW.Call(hMenu, uintptr(flags), 0, 0)
		return
	}
	ptr, _ := windows.UTF16PtrFromString(text)
	_, _, _ = procAppendMenuW.Call(hMenu, uintptr(flags), uintptr(id), uintptr(unsafe.Pointer(ptr)))
}

func (s *Service) addTrayIcon(tip string) {
	nid := notifyIconDataW{
		cbSize:           uint32(unsafe.Sizeof(notifyIconDataW{})),
		hWnd:             s.hwnd.Load(),
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmAppTray,
		hIcon:            s.hIcon,
	}
	copyUTF16(nid.szTip[:], tip)
	_, _, _ = procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
}

func (s *Service) updateTrayTooltip(tip string) {
	h := s.hwnd.Load()
	if h == 0 {
		return
	}
	nid := notifyIconDataW{
		cbSize: uint32(unsafe.Sizeof(notifyIconDataW{})),
		hWnd:   h,
		uID:    1,
		uFlags: nifTip,
	}
	copyUTF16(nid.szTip[:], tip)
	_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (s *Service) showBalloon(title, body string) {
	h := s.hwnd.Load()
	if h == 0 {
		return
	}
	nid := notifyIconDataW{
		cbSize:      uint32(unsafe.Sizeof(notifyIconDataW{})),
		hWnd:        h,
		uID:         1,
		uFlags:      nifInfo,
		dwInfoFlags: niifInfo,
	}
	copyUTF16(nid.szInfoTitle[:], title)
	copyUTF16(nid.szInfo[:], body)
	_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (s *Service) removeTrayIcon() {
	h := s.hwnd.Load()
	if h == 0 || !s.iconRemoved.CompareAndSwap(false, true) {
		return
	}
	nid := notifyIconDataW{
		cbSize: uint32(unsafe.Sizeof(notifyIconDataW{})),
		hWnd:   h,
		uID:    1,
	}
	_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (s *Service) shutdown() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	if h := s.hwnd.Load(); h != 0 {
		_, _, _ = procPostMessageW.Call(h, wmClose, 0, 0)
	}
}

func copyUTF16(dst []uint16, s string) {
	u16, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}
	n := len(u16)
	if n > len(dst) {
		n = len(dst)
		u16[n-1] = 0
	}
	copy(dst, u16[:n])
}
