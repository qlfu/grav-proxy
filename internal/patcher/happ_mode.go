package patcher

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	happPrefsRegPath = `Software\Happ\OrganizationDefaults\Preferences`
	happAdvRegPath   = `Software\Happ\OrganizationDefaults\Preferences\AdvancedSettings`
	happBackupFile   = "happ-backup.json"
	happWMClose      = 0x0010
)

type happModeBackup struct {
	HadSystemProxy bool   `json:"hadSystemProxy"`
	SystemProxy    string `json:"systemProxy"`
	HadTun         bool   `json:"hadTun"`
	Tun            string `json:"tun"`
}

func IsHappTunAvailable() bool {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\HappService`, registry.QUERY_VALUE); err == nil {
		_ = k.Close()
		return true
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if base := strings.TrimSpace(os.Getenv(env)); base != "" {
			if _, err := os.Stat(filepath.Join(base, "FlyFrogLLC", "Happ", "happd.exe")); err == nil {
				return true
			}
		}
	}
	return false
}

func IsHappSystemProxyEnabled() bool {
	pk, err := registry.OpenKey(registry.CURRENT_USER, happPrefsRegPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	_ = pk.Close()

	ak, err := registry.OpenKey(registry.CURRENT_USER, happAdvRegPath, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer ak.Close()

	curSys, _, errSys := ak.GetStringValue("systemProxy")
	if errSys != nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(curSys), "false")
}

func ConfigureHappTunOnly(dataDir string) (bool, string, error) {
	if !IsHappTunAvailable() || !IsHappSystemProxyEnabled() {
		return false, "", nil
	}
	happExe, pids := findRunningHappExe()
	if len(pids) > 0 {
		stopHappGUI(pids)
	}
	changed, msg, err := configureHappTunOnlyAtKey(happPrefsRegPath, happAdvRegPath, dataDir, true)
	if err != nil {
		if len(pids) > 0 && happExe != "" {
			relaunchHappGUI(happExe)
		}
		return false, "", err
	}
	if changed && len(pids) > 0 {
		clearStaleLoopbackProxy()
		if happExe != "" {
			relaunchHappGUI(happExe)
			msg += " (Happ перезапущен)"
		}
	}
	return changed, msg, nil
}

func RestoreHappSettings(dataDir string) (bool, error) {
	if strings.TrimSpace(dataDir) == "" {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(dataDir, happBackupFile)); err != nil {
		return false, nil
	}
	happExe, pids := findRunningHappExe()
	if len(pids) > 0 {
		stopHappGUI(pids)
	}
	restored, err := restoreHappSettingsAtKey(happAdvRegPath, dataDir)
	if len(pids) > 0 && happExe != "" {
		relaunchHappGUI(happExe)
	}
	return restored, err
}

func configureHappTunOnlyAtKey(prefsKeyPath, advKeyPath, dataDir string, tunAvailable bool) (bool, string, error) {
	if !tunAvailable {
		return false, "", nil
	}
	pk, err := registry.OpenKey(registry.CURRENT_USER, prefsKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false, "", nil
	}
	_ = pk.Close()

	ak, _, err := registry.CreateKey(registry.CURRENT_USER, advKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, "", err
	}
	defer ak.Close()

	curSys, _, errSys := ak.GetStringValue("systemProxy")
	curTun, _, errTun := ak.GetStringValue("tun")

	needSys := errSys != nil || !strings.EqualFold(strings.TrimSpace(curSys), "false")
	needTun := errTun == nil && strings.EqualFold(strings.TrimSpace(curTun), "false")
	if !needSys && !needTun {
		return false, "Happ уже в режиме TUN (System Proxy отключён)", nil
	}

	if strings.TrimSpace(dataDir) != "" {
		bakPath := filepath.Join(dataDir, happBackupFile)
		if _, statErr := os.Stat(bakPath); os.IsNotExist(statErr) {
			if err := os.MkdirAll(dataDir, 0755); err == nil {
				bak := happModeBackup{
					HadSystemProxy: errSys == nil,
					SystemProxy:    curSys,
					HadTun:         errTun == nil,
					Tun:            curTun,
				}
				if raw, mErr := json.MarshalIndent(bak, "", "  "); mErr == nil {
					_ = os.WriteFile(bakPath, raw, 0644)
				}
			}
		}
	}

	if err := ak.SetStringValue("systemProxy", "false"); err != nil {
		return false, "", err
	}
	if needTun {
		if err := ak.SetStringValue("tun", "true"); err != nil {
			return false, "", err
		}
	}
	return true, "Happ переведён в режим TUN (System Proxy отключён)", nil
}

func restoreHappSettingsAtKey(advKeyPath, dataDir string) (bool, error) {
	bakPath := filepath.Join(dataDir, happBackupFile)
	raw, err := os.ReadFile(bakPath)
	if err != nil {
		return false, nil
	}
	var bak happModeBackup
	if err := json.Unmarshal(raw, &bak); err != nil {
		_ = os.Remove(bakPath)
		return false, err
	}

	ak, err := registry.OpenKey(registry.CURRENT_USER, advKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		_ = os.Remove(bakPath)
		return false, nil
	}
	defer ak.Close()

	if bak.HadSystemProxy {
		_ = ak.SetStringValue("systemProxy", bak.SystemProxy)
	} else {
		_ = ak.DeleteValue("systemProxy")
	}
	if bak.HadTun {
		_ = ak.SetStringValue("tun", bak.Tun)
	} else {
		_ = ak.DeleteValue("tun")
	}

	_ = os.Remove(bakPath)
	return true, nil
}

func findRunningHappExe() (string, []uint32) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", nil
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return "", nil
	}

	var exePath string
	var pids []uint32
	for {
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		if name == "happ.exe" && pe.ProcessID != 0 {
			if exePath == "" {
				exePath = queryProcessImagePath(pe.ProcessID)
			}
			pids = append(pids, pe.ProcessID)
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return exePath, pids
}

func stopHappGUI(pids []uint32) {
	if len(pids) == 0 {
		return
	}
	pidSet := make(map[uint32]struct{}, len(pids))
	for _, p := range pids {
		pidSet[p] = struct{}{}
	}
	var hwnds []uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		_, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if _, ok := pidSet[pid]; ok {
			hwnds = append(hwnds, hwnd)
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(cb, 0)
	for _, h := range hwnds {
		_, _, _ = procPostMessageW.Call(h, happWMClose, 0, 0)
	}
	for _, pid := range pids {
		if !waitProcessExit(pid, 1500*time.Millisecond) {
			_ = terminatePID(pid)
			_ = waitProcessExit(pid, 1000*time.Millisecond)
		}
	}
	time.Sleep(250 * time.Millisecond)
}

func relaunchHappGUI(exePath string) {
	if strings.TrimSpace(exePath) == "" {
		return
	}
	cmd := exec.Command(exePath, "--autostart")
	cmd.Dir = filepath.Dir(exePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	_ = cmd.Start()
}

func clearStaleLoopbackProxy() {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	en, _, errEn := k.GetIntegerValue("ProxyEnable")
	srv, _, errSrv := k.GetStringValue("ProxyServer")
	if errEn == nil && en == 1 && errSrv == nil {
		low := strings.ToLower(strings.TrimSpace(srv))
		if strings.HasPrefix(low, "127.0.0.1:") || strings.HasPrefix(low, "localhost:") {
			_ = k.SetDWordValue("ProxyEnable", 0)
		}
	}
}
