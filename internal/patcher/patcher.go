package patcher

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	ineligibleFrom = []byte("ineligible")
	ineligibleTo   = []byte("inexigible")
	proxyVarFrom   = []byte("https_proxy")
	proxyVarTo     = []byte("AG_LS_PROXY")

	uuidRe                       = regexp.MustCompile(`^[a-f0-9-]{36}$`)
	targetExeRe                  = regexp.MustCompile(`(?i)^(agy|agy-.+|language_server.*)\.exe$`)
	user32DLL                    = windows.NewLazySystemDLL("user32.dll")
	procSendMsgTO                = user32DLL.NewProc("SendMessageTimeoutW")
	procEnumWindows              = user32DLL.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32DLL.NewProc("GetWindowThreadProcessId")
	procGetClassNameW            = user32DLL.NewProc("GetClassNameW")
	procGetWindowTextW           = user32DLL.NewProc("GetWindowTextW")
	procIsWindowVisible          = user32DLL.NewProc("IsWindowVisible")
	procPostMessageW             = user32DLL.NewProc("PostMessageW")
)

const (
	hwndBroadcast = 0xffff
	wmSettingChg  = 0x001A
	wmClose       = 0x0010
	smtoAbortHung = 0x0002
)

func BroadcastEnvChange() {
	ptr, _ := windows.UTF16PtrFromString("Environment")
	_, _, _ = procSendMsgTO.Call(
		uintptr(hwndBroadcast),
		uintptr(wmSettingChg),
		0,
		uintptr(unsafe.Pointer(ptr)),
		uintptr(smtoAbortHung),
		2000,
		0,
	)
}

func WriteFileAtomicWithRenameFallback(path string, data []byte) error {
	pid := os.Getpid()
	tmp := fmt.Sprintf("%s.tmp.%d", path, pid)
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	tmpPtr, _ := windows.UTF16PtrFromString(tmp)
	dstPtr, _ := windows.UTF16PtrFromString(path)
	flags := uint32(windows.MOVEFILE_REPLACE_EXISTING | windows.MOVEFILE_WRITE_THROUGH)
	if err := windows.MoveFileEx(tmpPtr, dstPtr, flags); err == nil {
		cleanupOldFiles(path)
		return nil
	} else {
		var errno windows.Errno
		if errors.As(err, &errno) && (errno == windows.ERROR_ACCESS_DENIED || errno == windows.ERROR_SHARING_VIOLATION) {
			oldPath := fmt.Sprintf("%s.old.%d", path, pid)
			_ = os.Remove(oldPath)
			oldPtr, _ := windows.UTF16PtrFromString(oldPath)
			if mvErr := windows.MoveFileEx(dstPtr, oldPtr, windows.MOVEFILE_REPLACE_EXISTING); mvErr == nil {
				if mv2Err := windows.MoveFileEx(tmpPtr, dstPtr, flags); mv2Err == nil {
					_ = os.Remove(oldPath)
					return nil
				} else {
					_ = windows.MoveFileEx(oldPtr, dstPtr, windows.MOVEFILE_REPLACE_EXISTING)
					_ = os.Remove(tmp)
					return fmt.Errorf("MoveFileExW failed (%v) for %s", mv2Err, path)
				}
			}
		}
		_ = os.Remove(tmp)
		return fmt.Errorf("MoveFileExW failed (%v) for %s", err, path)
	}
}

func cleanupOldFiles(targetPath string) {
	dir := filepath.Dir(targetPath)
	base := filepath.Base(targetPath)
	matches, err := filepath.Glob(filepath.Join(dir, base+".old.*"))
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

func FindAntigravityRoots() []string {
	var cands []string
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		cands = append(cands, filepath.Join(la, "Programs", "Antigravity"))
	}
	for _, envName := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if pf := os.Getenv(envName); pf != "" {
			cands = append(cands, filepath.Join(pf, "Antigravity"))
		}
	}
	var out []string
	seen := make(map[string]struct{})
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			low := strings.ToLower(c)
			if _, ok := seen[low]; !ok {
				seen[low] = struct{}{}
				out = append(out, c)
			}
		}
	}
	return out
}

func FindMainAntigravityExe(roots []string) string {
	for _, r := range roots {
		for _, name := range []string{"Antigravity.exe", "Antigravity IDE.exe"} {
			p := filepath.Join(r, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	return ""
}

func CollectAntigravityTargets(roots []string) []string {
	var files []string
	seen := make(map[string]struct{})
	for _, r := range roots {
		_ = filepath.WalkDir(r, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(r, p)
			if strings.Count(rel, string(filepath.Separator)) > 6 {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if targetExeRe.MatchString(d.Name()) {
				low := strings.ToLower(p)
				if _, ok := seen[low]; !ok {
					seen[low] = struct{}{}
					files = append(files, p)
				}
			}
			return nil
		})
	}
	return files
}

type BackupManifestEntry struct {
	OriginalPath   string `json:"OriginalPath"`
	BackupFile     string `json:"BackupFile"`
	OriginalSHA256 string `json:"OriginalSha256"`
}

func PatchAntigravityBinaries(dataDir string, roots []string) (patched int, already int, errList []string) {
	if len(roots) == 0 {
		roots = FindAntigravityRoots()
	}
	if len(roots) == 0 {
		return 0, 0, nil
	}
	targets := CollectAntigravityTargets(roots)
	backupsDir := filepath.Join(dataDir, "backups")
	_ = os.MkdirAll(backupsDir, 0755)
	manifestPath := filepath.Join(backupsDir, "manifest.json")
	manifest := loadBackupManifest(manifestPath)

	for _, path := range targets {
		cleanupOldFiles(path)
		raw, err := os.ReadFile(path)
		if err != nil {
			errList = append(errList, fmt.Sprintf("%s: %v", filepath.Base(path), err))
			continue
		}
		inFrom := bytes.Count(raw, ineligibleFrom)
		pvFrom := bytes.Count(raw, proxyVarFrom)
		if inFrom == 0 && pvFrom == 0 {
			if bytes.Count(raw, ineligibleTo) > 0 || bytes.Count(raw, proxyVarTo) > 0 {
				already++
			}
			continue
		}

		origSum := sha256Hex(raw)
		manifest = upsertBackup(backupsDir, manifest, path, raw, origSum)
		_ = saveBackupManifest(manifestPath, manifest)

		patchedBytes := append([]byte(nil), raw...)
		patchedBytes = bytes.ReplaceAll(patchedBytes, ineligibleFrom, ineligibleTo)
		patchedBytes = bytes.ReplaceAll(patchedBytes, proxyVarFrom, proxyVarTo)
		if err := WriteFileAtomicWithRenameFallback(path, patchedBytes); err != nil {
			errList = append(errList, fmt.Sprintf("%s: %v", filepath.Base(path), err))
			continue
		}
		patched++
	}
	return patched, already, errList
}

func RestoreAntigravityBinaries(dataDir string) (restored int, errList []string) {
	backupsDir := filepath.Join(dataDir, "backups")
	manifestPath := filepath.Join(backupsDir, "manifest.json")
	manifest := loadBackupManifest(manifestPath)
	for _, ent := range manifest {
		if ent.OriginalPath == "" || ent.BackupFile == "" {
			continue
		}
		bakPath := filepath.Join(backupsDir, ent.BackupFile)
		bakBytes, err := os.ReadFile(bakPath)
		if err != nil {
			continue
		}
		if _, err := os.Stat(ent.OriginalPath); os.IsNotExist(err) {
			continue
		}
		if err := WriteFileAtomicWithRenameFallback(ent.OriginalPath, bakBytes); err != nil {
			errList = append(errList, fmt.Sprintf("%s: %v", filepath.Base(ent.OriginalPath), err))
			continue
		}
		restored++
	}
	return restored, errList
}

func loadBackupManifest(path string) []BackupManifestEntry {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	var list []BackupManifestEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil
	}
	return list
}

func saveBackupManifest(path string, list []BackupManifestEntry) error {
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomicWithRenameFallback(path, append(b, '\n'))
}

func upsertBackup(backupsDir string, list []BackupManifestEntry, origPath string, origBytes []byte, shaHex string) []BackupManifestEntry {
	var found *BackupManifestEntry
	for i := range list {
		if strings.EqualFold(list[i].OriginalPath, origPath) {
			found = &list[i]
			break
		}
	}
	bakName := ""
	if found != nil && found.BackupFile != "" {
		bakName = found.BackupFile
	} else {
		bakName = fmt.Sprintf("%s-%s.orig.bak", filepath.Base(origPath), sha256Hex([]byte(strings.ToLower(origPath)))[:10])
	}
	bakPath := filepath.Join(backupsDir, bakName)
	_ = WriteFileAtomicWithRenameFallback(bakPath, origBytes)
	if found != nil {
		found.OriginalSHA256 = shaHex
	} else {
		list = append(list, BackupManifestEntry{
			OriginalPath:   origPath,
			BackupFile:     bakName,
			OriginalSHA256: shaHex,
		})
	}
	return list
}

func ClaudeDesktopDataRoots(localAppData string) []string {
	if strings.TrimSpace(localAppData) == "" {
		localAppData = os.Getenv("LOCALAPPDATA")
	}
	if strings.TrimSpace(localAppData) == "" {
		return nil
	}
	var roots []string
	primary := filepath.Join(localAppData, "Claude-3p")
	roots = append(roots, primary)

	pkgDir := filepath.Join(localAppData, "Packages")
	if entries, err := os.ReadDir(pkgDir); err == nil {
		for _, ent := range entries {
			if ent.IsDir() && strings.HasPrefix(ent.Name(), "Claude_") {
				vfs := filepath.Join(pkgDir, ent.Name(), "LocalCache", "Local", "Claude-3p")
				if st, err := os.Stat(vfs); err == nil && st.IsDir() {
					roots = append(roots, vfs)
				}
			}
		}
	}
	if appData := os.Getenv("APPDATA"); appData != "" && strings.EqualFold(localAppData, os.Getenv("LOCALAPPDATA")) {
		roam := filepath.Join(appData, "Claude-3p")
		if st, err := os.Stat(roam); err == nil && st.IsDir() && !strings.EqualFold(roam, primary) {
			roots = append(roots, roam)
		}
	}
	return roots
}

type ClaudeOptions struct {
	ProxyURL   string
	APIKey     string
	BaseURL    string
	AuthScheme string // "bearer" | "x-api-key"
}

func ConfigureClaudeDesktop(dataDir, localAppData string, opts ClaudeOptions) (string, error) {
	if opts.ProxyURL == "" {
		opts.ProxyURL = "http://127.0.0.1:28080"
	}
	if strings.TrimSpace(opts.BaseURL) != "" {
		low := strings.ToLower(strings.TrimSpace(opts.BaseURL))
		if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
			return "", fmt.Errorf("некорректный Base URL для Claude Desktop")
		}
	}
	roots := ClaudeDesktopDataRoots(localAppData)
	if len(roots) == 0 {
		return "", fmt.Errorf("не найден каталог LOCALAPPDATA")
	}

	isRealLocal := localAppData == "" || strings.EqualFold(localAppData, os.Getenv("LOCALAPPDATA"))
	if isRealLocal {
		stopClaudeProcesses()
	}

	scheme := "bearer"
	if strings.EqualFold(strings.TrimSpace(opts.AuthScheme), "x-api-key") {
		scheme = "x-api-key"
	}
	key := strings.TrimSpace(opts.APIKey)
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")

	var primaryAppliedID string
	var updated int
	var lastProviderSummary = "proxy only"

	for idx, rootDir := range roots {
		libDir := filepath.Join(rootDir, "configLibrary")
		if err := os.MkdirAll(libDir, 0755); err != nil {
			return "", err
		}
		metaPath := filepath.Join(libDir, "_meta.json")
		meta, err := readJSONMap(metaPath)
		if err != nil {
			return "", err
		}
		metaChanged := false
		if _, err := os.Stat(metaPath); os.IsNotExist(err) {
			metaChanged = true
		}
		if _, hasHybrid := meta["hybridPointer"]; hasHybrid {
			delete(meta, "hybridPointer")
			metaChanged = true
		}

		entries := readEntriesList(meta["entries"])
		appliedID := strings.ToLower(strings.TrimSpace(strAny(meta["appliedId"])))
		if !uuidRe.MatchString(appliedID) && primaryAppliedID != "" {
			appliedID = primaryAppliedID
		}
		inEntries := false
		if uuidRe.MatchString(appliedID) {
			for _, en := range entries {
				if strings.EqualFold(strAny(en["id"]), appliedID) {
					inEntries = true
					break
				}
			}
		}
		if !uuidRe.MatchString(appliedID) || !inEntries {
			if !uuidRe.MatchString(appliedID) {
				appliedID = newUUIDv4()
			}
			if !inEntries {
				entries = append(entries, map[string]any{"id": appliedID, "name": "GravProxy"})
			}
			meta["appliedId"] = appliedID
			meta["entries"] = entries
			metaChanged = true
		}
		if idx == 0 || primaryAppliedID == "" {
			primaryAppliedID = appliedID
		}
		if metaChanged {
			if err := writeJSONMapAtomic(metaPath, meta); err != nil {
				return "", err
			}
		}

		profPath := filepath.Join(libDir, appliedID+".json")
		prof, err := readJSONMap(profPath)
		if err != nil {
			return "", err
		}
		prof["egressProxyUrl"] = opts.ProxyURL

		if key != "" {
			isGW := base != "" || strings.HasPrefix(strings.ToLower(key), "sk-or-")
			if isGW {
				gw := base
				if gw == "" {
					gw = "https://openrouter.ai/api"
				}
				prof["inferenceProvider"] = "gateway"
				prof["inferenceCredentialKind"] = "static"
				prof["inferenceGatewayBaseUrl"] = gw
				prof["inferenceGatewayApiKey"] = key
				prof["inferenceGatewayAuthScheme"] = scheme
				delete(prof, "inferenceAnthropicApiKey")
			} else {
				prof["inferenceProvider"] = "anthropic"
				prof["inferenceCredentialKind"] = "static"
				prof["inferenceAnthropicApiKey"] = key
				delete(prof, "inferenceGatewayBaseUrl")
				delete(prof, "inferenceGatewayApiKey")
				delete(prof, "inferenceGatewayAuthScheme")
			}
		} else if base != "" {
			prof["inferenceGatewayBaseUrl"] = base
			if strAny(prof["inferenceProvider"]) == "gateway" {
				prof["inferenceGatewayAuthScheme"] = scheme
			}
		}

		if err := writeJSONMapAtomic(profPath, prof); err != nil {
			return "", err
		}

		activeProv := strAny(prof["inferenceProvider"])
		if activeProv != "" {
			desktopCfgPath := filepath.Join(rootDir, "claude_desktop_config.json")
			dCfg, dErr := readJSONMap(desktopCfgPath)
			if dErr == nil {
				dCfg["deploymentMode"] = "3p"
				_ = writeJSONMapAtomic(desktopCfgPath, dCfg)
			}
		}
		if idx == 0 && activeProv != "" {
			lastProviderSummary = activeProv
		}
		updated++
	}

	if updated == 0 {
		return "", fmt.Errorf("не удалось обновить конфигурацию Claude Desktop")
	}
	return fmt.Sprintf("Claude Desktop configured (%s, %s)", opts.ProxyURL, lastProviderSummary), nil
}

func RemoveClaudeDesktopProxy(localAppData string) (int, error) {
	roots := ClaudeDesktopDataRoots(localAppData)
	removed := 0
	for _, rootDir := range roots {
		libDir := filepath.Join(rootDir, "configLibrary")
		metaPath := filepath.Join(libDir, "_meta.json")
		meta, err := readJSONMap(metaPath)
		if err != nil || len(meta) == 0 {
			continue
		}
		appliedID := strings.ToLower(strings.TrimSpace(strAny(meta["appliedId"])))
		if !uuidRe.MatchString(appliedID) {
			continue
		}
		profPath := filepath.Join(libDir, appliedID+".json")
		prof, err := readJSONMap(profPath)
		if err != nil {
			continue
		}
		pv := strings.TrimSpace(strAny(prof["egressProxyUrl"]))
		if !strings.EqualFold(pv, "http://127.0.0.1:28080") && pv != "127.0.0.1:28080" {
			continue
		}
		delete(prof, "egressProxyUrl")
		entries := readEntriesList(meta["entries"])
		isOnlyGP := len(prof) == 0 && len(entries) == 1 && strAny(entries[0]["name"]) == "GravProxy"
		if isOnlyGP {
			_ = os.Remove(profPath)
			_ = os.Remove(metaPath)
		} else {
			_ = writeJSONMapAtomic(profPath, prof)
		}
		removed++
	}
	return removed, nil
}

func FindOpenCodeCLI() string {
	if p, err := exec.LookPath("opencode.exe"); err == nil {
		return p
	}
	var cands []string
	if up := os.Getenv("USERPROFILE"); up != "" {
		cands = append(cands,
			filepath.Join(up, ".opencode", "bin", "opencode.exe"),
			filepath.Join(up, ".bun", "bin", "opencode.exe"),
		)
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		cands = append(cands,
			filepath.Join(la, "Programs", "@opencode-aidesktop", "resources", "opencode-cli.exe"),
			filepath.Join(la, "Programs", "@opencode-aidesktop", "opencode-cli.exe"),
			filepath.Join(la, "Programs", "OpenCode", "resources", "opencode-cli.exe"),
			filepath.Join(la, "Programs", "OpenCode", "resources", "opencode.exe"),
			filepath.Join(la, "Programs", "ai.opencode.desktop", "opencode-cli.exe"),
		)
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func FindOpenCodeGUI() string {
	var cands []string
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		cands = append(cands,
			filepath.Join(la, "Programs", "@opencode-aidesktop", "OpenCode.exe"),
			filepath.Join(la, "Programs", "OpenCode", "OpenCode.exe"),
			filepath.Join(la, "Programs", "ai.opencode.desktop", "OpenCode.exe"),
		)
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func ConfigureOpenCode(dataDir, selfExe, proxyURL string) (string, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return "", err
	}
	wrapper := fmt.Sprintf("@echo off\r\n\"%s\" --launch opencode %%*\r\nexit /b %%errorlevel%%\r\n", selfExe)
	for _, name := range []string{"opencode.cmd", "oc.cmd"} {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte(wrapper), 0644); err != nil {
			return "", err
		}
	}
	_ = addToUserPath(dataDir)

	if cli := FindOpenCodeCLI(); cli != "" {
		_ = runHidden(cli, "service", "set", "env", "HTTP_PROXY", proxyURL)
		_ = runHidden(cli, "service", "set", "env", "HTTPS_PROXY", proxyURL)
		_ = runHidden(cli, "service", "set", "env", "NO_PROXY", "localhost,127.0.0.1,::1")
		return "OpenCode CLI + wrappers configured (" + cli + ")", nil
	}
	return "OpenCode CLI wrappers configured in " + dataDir, nil
}

func RemoveOpenCode(dataDir string) {
	for _, name := range []string{"opencode.cmd", "oc.cmd", "opencode-proxy.cmd"} {
		_ = os.Remove(filepath.Join(dataDir, name))
	}
	_ = removeFromUserPath(dataDir)
	if cli := FindOpenCodeCLI(); cli != "" {
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
			_ = runHidden(cli, "service", "unset", "env", k)
		}
	}
}

type ShortcutBackup struct {
	ShortcutPath string `json:"shortcutPath"`
	TargetPath   string `json:"targetPath"`
	Arguments    string `json:"arguments"`
	WorkingDir   string `json:"workingDir"`
	IconLocation string `json:"iconLocation"`
}

func PatchShortcuts(dataDir, selfExe string, doAntigravity, doOpenCode bool) int {
	var roots []string
	if appData := os.Getenv("APPDATA"); appData != "" {
		roots = append(roots, filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs"))
	}
	if up := os.Getenv("USERPROFILE"); up != "" {
		roots = append(roots, filepath.Join(up, "Desktop"))
	}
	return PatchShortcutsInRoots(dataDir, selfExe, roots, doAntigravity, doOpenCode)
}

func PatchShortcutsInRoots(dataDir, selfExe string, roots []string, doAntigravity, doOpenCode bool) int {
	var paths []string
	for _, r := range roots {
		_ = filepath.WalkDir(r, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.EqualFold(filepath.Ext(d.Name()), ".lnk") {
				return nil
			}
			low := strings.ToLower(d.Name())
			if (doAntigravity && strings.Contains(low, "antigravity")) || (doOpenCode && strings.Contains(low, "opencode")) {
				paths = append(paths, p)
			}
			return nil
		})
	}
	if len(paths) == 0 {
		return 0
	}

	patched := 0
	err := withWScriptShell(func(wshell *ole.IDispatch) error {
		bakPath := filepath.Join(dataDir, "shortcuts-backup.json")
		backups := loadShortcutBackups(bakPath)
		bakMap := make(map[string]ShortcutBackup)
		for _, b := range backups {
			bakMap[strings.ToLower(b.ShortcutPath)] = b
		}

		for _, lnkPath := range paths {
			scVar, err := oleutil.CallMethod(wshell, "CreateShortcut", lnkPath)
			if err != nil {
				continue
			}
			sc := scVar.ToIDispatch()
			target := oleStrProp(sc, "TargetPath")
			args := oleStrProp(sc, "Arguments")
			workDir := oleStrProp(sc, "WorkingDirectory")
			icon := oleStrProp(sc, "IconLocation")

			lowName := strings.ToLower(filepath.Base(lnkPath))
			modeFlag := ""
			if strings.Contains(lowName, "antigravity") {
				modeFlag = "--launch antigravity"
			} else if strings.Contains(lowName, "opencode") {
				modeFlag = "--launch opencode"
			}
			if modeFlag == "" {
				sc.Release()
				continue
			}
			if _, exists := bakMap[strings.ToLower(lnkPath)]; !exists && !strings.EqualFold(target, selfExe) {
				bakMap[strings.ToLower(lnkPath)] = ShortcutBackup{
					ShortcutPath: lnkPath,
					TargetPath:   target,
					Arguments:    args,
					WorkingDir:   workDir,
					IconLocation: icon,
				}
			}

			_, _ = oleutil.PutProperty(sc, "TargetPath", selfExe)
			_, _ = oleutil.PutProperty(sc, "Arguments", modeFlag)
			_, _ = oleutil.PutProperty(sc, "WorkingDirectory", dataDir)
			if (icon == "" || strings.EqualFold(icon, ",0")) && target != "" {
				_, _ = oleutil.PutProperty(sc, "IconLocation", target+",0")
			}
			_, _ = oleutil.CallMethod(sc, "Save")
			sc.Release()
			patched++
		}

		var list []ShortcutBackup
		for _, b := range bakMap {
			list = append(list, b)
		}
		if raw, err := json.MarshalIndent(list, "", "  "); err == nil {
			_ = WriteFileAtomicWithRenameFallback(bakPath, append(raw, '\n'))
		}
		return nil
	})
	if err != nil {
		return 0
	}
	return patched
}

func RestoreShortcuts(dataDir string) int {
	bakPath := filepath.Join(dataDir, "shortcuts-backup.json")
	backups := loadShortcutBackups(bakPath)
	if len(backups) == 0 {
		return 0
	}
	restored := 0
	err := withWScriptShell(func(wshell *ole.IDispatch) error {
		for _, b := range backups {
			if _, err := os.Stat(b.ShortcutPath); os.IsNotExist(err) {
				continue
			}
			scVar, err := oleutil.CallMethod(wshell, "CreateShortcut", b.ShortcutPath)
			if err != nil {
				continue
			}
			sc := scVar.ToIDispatch()
			_, _ = oleutil.PutProperty(sc, "TargetPath", b.TargetPath)
			_, _ = oleutil.PutProperty(sc, "Arguments", b.Arguments)
			_, _ = oleutil.PutProperty(sc, "WorkingDirectory", b.WorkingDir)
			_, _ = oleutil.PutProperty(sc, "IconLocation", b.IconLocation)
			_, _ = oleutil.CallMethod(sc, "Save")
			sc.Release()
			restored++
		}
		return nil
	})
	if err != nil {
		return 0
	}
	_ = os.Remove(bakPath)
	return restored
}

func withWScriptShell(fn func(wshell *ole.IDispatch) error) error {
	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		needUninit := false
		if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err == nil {
			needUninit = true
		} else {
			var oleErr *ole.OleError
			if errors.As(err, &oleErr) {
				switch uint32(oleErr.Code()) {
				case 1:
					needUninit = true
				case 0x80010106:
					needUninit = false
				default:
					errCh <- err
					return
				}
			} else {
				errCh <- err
				return
			}
		}
		if needUninit {
			defer ole.CoUninitialize()
		}

		unk, err := oleutil.CreateObject("WScript.Shell")
		if err != nil {
			errCh <- err
			return
		}
		defer unk.Release()
		wshell, err := unk.QueryInterface(ole.IID_IDispatch)
		if err != nil {
			errCh <- err
			return
		}
		defer wshell.Release()
		errCh <- fn(wshell)
	}()
	return <-errCh
}

func SetTrayAutostart(selfExe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("GravProxy", fmt.Sprintf(`"%s" --tray`, selfExe))
}

func RemoveTrayAutostart() {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	for _, name := range []string{"GravProxy", "AntigravityLocalProxy", "AntigravityProxyTray", "ag-proxy"} {
		_ = k.DeleteValue(name)
	}
}

func CleanupLegacyStartupEntries() {
	if appData := os.Getenv("APPDATA"); appData != "" {
		startup := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
		for _, pat := range []string{"Run-Proxy*", "Update-Egress*", "Antigravity Proxy*", "proxy-tray*"} {
			if matches, err := filepath.Glob(filepath.Join(startup, pat)); err == nil {
				for _, m := range matches {
					_ = os.Remove(m)
				}
			}
		}
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err == nil {
		defer k.Close()
		for _, legacy := range []string{"AntigravityLocalProxy", "AntigravityProxyTray", "ag-proxy", "Run-Proxy"} {
			_ = k.DeleteValue(legacy)
		}
	}
}

func StopLegacyAgProxyProcesses() int {
	localApp := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localApp == "" {
		return 0
	}
	legacyDir := filepath.Join(localApp, "ag-proxy")
	if st, err := os.Stat(legacyDir); err != nil || !st.IsDir() {
		return 0
	}
	legacyPrefix := strings.ToLower(filepath.Clean(legacyDir)) + `\`
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return 0
	}

	selfPID := uint32(os.Getpid())
	killed := 0
	for {
		if pe.ProcessID != 0 && pe.ProcessID != selfPID {
			exeName := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
			if exeName == "proxy-tray.exe" || exeName == "sing-box-bg.exe" || exeName == "sing-box.exe" {
				h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, pe.ProcessID)
				if err == nil {
					buf := make([]uint16, 1024)
					n := uint32(len(buf))
					if qErr := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); qErr == nil {
						imgPath := windows.UTF16ToString(buf[:n])
						if strings.HasPrefix(strings.ToLower(imgPath), legacyPrefix) {
							if windows.TerminateProcess(h, 0) == nil {
								killed++
							}
						}
					}
					_ = windows.CloseHandle(h)
				}
			}
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return killed
}

type windowMatch struct {
	hwnd uintptr
	pid  uint32
}

func guiPidFilePath() string {
	return filepath.Join(os.TempDir(), "gravproxy-gui.pid")
}

func WriteGUIPidFile() {
	_ = os.WriteFile(guiPidFilePath(), []byte(strconv.Itoa(os.Getpid())), 0644)
}

func RemoveGUIPidFile() {
	p := guiPidFilePath()
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	if strings.TrimSpace(string(raw)) == strconv.Itoa(os.Getpid()) {
		_ = os.Remove(p)
	}
}

func CloseExistingGUIInstances() int {
	selfPID := uint32(os.Getpid())

	trayPIDs := make(map[uint32]struct{})
	var allMatches []windowMatch
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		_, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == 0 || pid == selfPID {
			return 1
		}
		cls := getWindowClass(hwnd)
		if strings.EqualFold(cls, "GravProxyTrayWnd") {
			trayPIDs[pid] = struct{}{}
			return 1
		}
		allMatches = append(allMatches, windowMatch{hwnd: hwnd, pid: pid})
		return 1
	})
	_, _, _ = procEnumWindows.Call(cb, 0)

	pids := make(map[uint32]struct{})
	hwndsByPID := make(map[uint32][]uintptr)
	for _, m := range allMatches {
		if _, isTray := trayPIDs[m.pid]; isTray {
			continue
		}
		img := queryProcessImagePath(m.pid)
		lowBase := strings.ToLower(filepath.Base(img))
		cls := strings.ToLower(getWindowClass(m.hwnd))
		title := strings.TrimSpace(getWindowText(m.hwnd))
		vis, _, _ := procIsWindowVisible.Call(m.hwnd)

		isGPName := strings.HasPrefix(lowBase, "gravproxy")
		isWailsGP := strings.EqualFold(title, "GravProxy") && strings.Contains(cls, "wails")
		if isWailsGP || (isGPName && (vis != 0 || strings.EqualFold(title, "GravProxy") || strings.Contains(cls, "wails"))) {
			pids[m.pid] = struct{}{}
			hwndsByPID[m.pid] = append(hwndsByPID[m.pid], m.hwnd)
		}
	}

	if raw, err := os.ReadFile(guiPidFilePath()); err == nil {
		if n, pErr := strconv.Atoi(strings.TrimSpace(string(raw))); pErr == nil && n > 0 {
			prevPID := uint32(n)
			if _, isTray := trayPIDs[prevPID]; prevPID != selfPID && !isTray {
				lowBase := strings.ToLower(filepath.Base(queryProcessImagePath(prevPID)))
				if strings.HasPrefix(lowBase, "gravproxy") {
					pids[prevPID] = struct{}{}
				}
			}
		}
	}

	if len(pids) == 0 {
		return 0
	}

	for pid := range pids {
		for _, h := range hwndsByPID[pid] {
			_, _, _ = procPostMessageW.Call(h, wmClose, 0, 0)
		}
	}

	deadline := time.Now().Add(1500 * time.Millisecond)
	closed := 0
	for pid := range pids {
		rem := time.Until(deadline)
		if rem > 0 && waitProcessExit(pid, rem) {
			closed++
			continue
		}
		if terminatePID(pid) {
			_ = waitProcessExit(pid, 500*time.Millisecond)
			closed++
		}
	}
	return closed
}

func StopGravProxyTrayProcesses(dataDir string) int {
	selfPID := uint32(os.Getpid())
	var trayMatches []windowMatch
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		_, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == 0 || pid == selfPID {
			return 1
		}
		if strings.EqualFold(getWindowClass(hwnd), "GravProxyTrayWnd") {
			trayMatches = append(trayMatches, windowMatch{hwnd: hwnd, pid: pid})
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(cb, 0)

	trayPIDs := make(map[uint32]struct{})
	for _, m := range trayMatches {
		trayPIDs[m.pid] = struct{}{}
		_, _, _ = procPostMessageW.Call(m.hwnd, wmClose, 0, 0)
	}

	stopped := 0
	aliveTray := make(map[uint32]struct{})
	if len(trayPIDs) > 0 {
		deadline := time.Now().Add(1500 * time.Millisecond)
		for pid := range trayPIDs {
			rem := time.Until(deadline)
			if rem > 0 && waitProcessExit(pid, rem) {
				stopped++
			} else {
				aliveTray[pid] = struct{}{}
			}
		}
	}

	targetExe := ""
	if strings.TrimSpace(dataDir) != "" {
		targetExe = filepath.Clean(filepath.Join(dataDir, "GravProxy.exe"))
	}

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err == nil {
		defer windows.CloseHandle(snap)
		var pe windows.ProcessEntry32
		pe.Size = uint32(unsafe.Sizeof(pe))
		if windows.Process32First(snap, &pe) == nil {
			for {
				if pe.ProcessID != 0 && pe.ProcessID != selfPID {
					_, inTray := aliveTray[pe.ProcessID]
					matchDataExe := false
					if !inTray && targetExe != "" && strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), "GravProxy.exe") {
						img := queryProcessImagePath(pe.ProcessID)
						if img != "" && strings.EqualFold(filepath.Clean(img), targetExe) {
							matchDataExe = true
						}
					}
					if inTray || matchDataExe {
						if terminatePID(pe.ProcessID) {
							_ = waitProcessExit(pe.ProcessID, 500*time.Millisecond)
							stopped++
						}
					}
				}
				if windows.Process32Next(snap, &pe) != nil {
					break
				}
			}
		}
	}
	return stopped
}

func CleanupEnvVars() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	removed := false
	for _, name := range []string{"AG_PROXY_VLESS", "AG_PROXY_SUB_URL"} {
		if err := k.DeleteValue(name); err == nil {
			removed = true
		}
	}
	if removed {
		BroadcastEnvChange()
	}
	return removed
}

func queryProcessImagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func getWindowClass(hwnd uintptr) string {
	var buf [256]uint16
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func getWindowText(hwnd uintptr) string {
	var buf [256]uint16
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func waitProcessExit(pid uint32, timeout time.Duration) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return true
	}
	defer windows.CloseHandle(h)
	ms := uint32(timeout / time.Millisecond)
	res, _ := windows.WaitForSingleObject(h, ms)
	return res == windows.WAIT_OBJECT_0
}

func terminatePID(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 0) == nil
}

func DetectConflictingBypass() []string {
	var warnings []string
	if pd := os.Getenv("ProgramData"); pd != "" {
		if _, err := os.Stat(filepath.Join(pd, "AGUnlocker")); err == nil {
			warnings = append(warnings, "Обнаружен AGUnlocker в %ProgramData%\\AGUnlocker (может конфликтовать с локальным прокси)")
		}
	}
	if sr := os.Getenv("SystemRoot"); sr != "" {
		hostsPath := filepath.Join(sr, "System32", "drivers", "etc", "hosts")
		if raw, err := os.ReadFile(hostsPath); err == nil {
			txt := strings.ToLower(string(raw))
			if strings.Contains(txt, "cloudcode-pa.googleapis.com") || strings.Contains(txt, "notebook.google.com") {
				warnings = append(warnings, "В файле hosts есть ручные записи AI-доменов Google (могут переопределять прокси/SmartDNS)")
			}
		}
	}
	return warnings
}

func addToUserPath(dir string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, valType, err := k.GetStringValue("Path")
	if err != nil {
		valType = registry.EXPAND_SZ
	}
	var parts []string
	parts = append(parts, dir)
	for _, p := range strings.Split(cur, ";") {
		t := strings.TrimSpace(p)
		if t != "" && !strings.EqualFold(t, dir) {
			parts = append(parts, t)
		}
	}
	newVal := strings.Join(parts, ";")
	if valType == registry.EXPAND_SZ {
		err = k.SetExpandStringValue("Path", newVal)
	} else {
		err = k.SetStringValue("Path", newVal)
	}
	if err == nil {
		BroadcastEnvChange()
	}
	return err
}

func removeFromUserPath(dir string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	cur, valType, err := k.GetStringValue("Path")
	if err != nil {
		return nil
	}
	var next []string
	changed := false
	for _, p := range strings.Split(cur, ";") {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		if strings.EqualFold(t, dir) {
			changed = true
			continue
		}
		next = append(next, t)
	}
	if !changed {
		return nil
	}
	val := strings.Join(next, ";")
	if valType == registry.EXPAND_SZ {
		err = k.SetExpandStringValue("Path", val)
	} else {
		err = k.SetStringValue("Path", val)
	}
	if err == nil {
		BroadcastEnvChange()
	}
	return err
}

func stopClaudeProcesses() {
	_ = StopTargetApps(false, false, true)
}

func runHidden(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return cmd.Run()
}

func ancestorPIDSet() map[uint32]struct{} {
	selfPID := uint32(os.Getpid())
	anc := map[uint32]struct{}{selfPID: {}}

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return anc
	}
	defer windows.CloseHandle(snap)

	parentMap := make(map[uint32]uint32)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if windows.Process32First(snap, &pe) == nil {
		for {
			parentMap[pe.ProcessID] = pe.ParentProcessID
			if windows.Process32Next(snap, &pe) != nil {
				break
			}
		}
	}

	cur := selfPID
	for i := 0; i < 32; i++ {
		p, ok := parentMap[cur]
		if !ok || p == 0 {
			break
		}
		if _, seen := anc[p]; seen {
			break
		}
		anc[p] = struct{}{}
		cur = p
	}
	return anc
}

func matchTargetApp(exeName string, doAntigravity, doOpenCode, doClaude bool) string {
	low := strings.ToLower(strings.TrimSpace(exeName))
	if doAntigravity {
		if low == "antigravity.exe" || low == "antigravity ide.exe" || low == "antigravity cli.exe" || targetExeRe.MatchString(low) {
			return "Antigravity"
		}
	}
	if doOpenCode && (low == "opencode.exe" || low == "opencode-cli.exe") {
		return "OpenCode"
	}
	if doClaude && low == "claude.exe" {
		return "Claude"
	}
	return ""
}

func StopTargetApps(doAntigravity, doOpenCode, doClaude bool) []string {
	if !doAntigravity && !doOpenCode && !doClaude {
		return nil
	}
	ancestors := ancestorPIDSet()

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	matchedPIDs := make(map[uint32]string)
	labelOrder := make([]string, 0, 3)
	labelSeen := make(map[string]struct{})
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if windows.Process32First(snap, &pe) == nil {
		for {
			if pe.ProcessID != 0 {
				if _, isAnc := ancestors[pe.ProcessID]; !isAnc {
					if label := matchTargetApp(windows.UTF16ToString(pe.ExeFile[:]), doAntigravity, doOpenCode, doClaude); label != "" {
						matchedPIDs[pe.ProcessID] = label
						if _, ok := labelSeen[label]; !ok {
							labelSeen[label] = struct{}{}
							labelOrder = append(labelOrder, label)
						}
					}
				}
			}
			if windows.Process32Next(snap, &pe) != nil {
				break
			}
		}
	}
	_ = windows.CloseHandle(snap)

	if len(matchedPIDs) == 0 {
		return nil
	}

	var hwnds []uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		_, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if _, ok := matchedPIDs[pid]; ok {
			hwnds = append(hwnds, hwnd)
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(cb, 0)
	for _, h := range hwnds {
		_, _, _ = procPostMessageW.Call(h, wmClose, 0, 0)
	}

	deadline := time.Now().Add(1500 * time.Millisecond)
	for pid := range matchedPIDs {
		rem := time.Until(deadline)
		if rem > 0 && waitProcessExit(pid, rem) {
			continue
		}
		if terminatePID(pid) {
			_ = waitProcessExit(pid, 500*time.Millisecond)
		}
	}
	return labelOrder
}

func readJSONMap(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	raw = bytes.TrimSpace(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}))
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func writeJSONMapAtomic(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomicWithRenameFallback(path, append(b, '\n'))
}

func readEntriesList(v any) []map[string]any {
	rawList, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []map[string]any
	for _, item := range rawList {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func loadShortcutBackups(path string) []ShortcutBackup {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var list []ShortcutBackup
	_ = json.Unmarshal(raw, &list)
	return list
}

func oleStrProp(d *ole.IDispatch, name string) string {
	v, err := oleutil.GetProperty(d, name)
	if err != nil {
		return ""
	}
	defer v.Clear()
	return v.ToString()
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func strAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
