package patcher

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
)

func longPath(p string) string {
	ptr, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return filepath.Clean(p)
	}
	buf := make([]uint16, 1024)
	n, err := windows.GetLongPathName(ptr, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		return filepath.Clean(p)
	}
	return windows.UTF16ToString(buf[:n])
}

func TestAntigravityPatchAndRestoreInTemp(t *testing.T) {
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	appDir := filepath.Join(tmp, "app")
	binDir := filepath.Join(appDir, "resources", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	targetExe := filepath.Join(binDir, "language_server.exe")
	orig := []byte("HEADER__ineligible__MID__https_proxy__TAIL")
	if err := os.WriteFile(targetExe, orig, 0644); err != nil {
		t.Fatal(err)
	}

	patched, already, errs := PatchAntigravityBinaries(dataDir, []string{appDir})
	if len(errs) > 0 || patched != 1 || already != 0 {
		t.Fatalf("unexpected patch result: patched=%d already=%d errs=%v", patched, already, errs)
	}
	after, err := os.ReadFile(targetExe)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(orig) || !bytes.Contains(after, []byte("inexigible")) || !bytes.Contains(after, []byte("AG_LS_PROXY")) {
		t.Fatalf("unexpected patched bytes: %q", after)
	}

	// Second run detects already patched
	patched2, already2, errs2 := PatchAntigravityBinaries(dataDir, []string{appDir})
	if len(errs2) > 0 || patched2 != 0 || already2 != 1 {
		t.Fatalf("expected already patched on 2nd run, got p=%d a=%d errs=%v", patched2, already2, errs2)
	}

	restored, rErrs := RestoreAntigravityBinaries(dataDir)
	if len(rErrs) > 0 || restored != 1 {
		t.Fatalf("unexpected restore result: restored=%d errs=%v", restored, rErrs)
	}
	back, _ := os.ReadFile(targetExe)
	if !bytes.Equal(back, orig) {
		t.Fatalf("restored bytes mismatch: got %q want %q", back, orig)
	}
}

func TestWriteAtomicOnLockedRunningExecutable(t *testing.T) {
	tmp := t.TempDir()
	src1 := filepath.Join(tmp, "v1.go")
	src2 := filepath.Join(tmp, "v2.go")
	exeTarget := filepath.Join(tmp, "dummy_running.exe")
	exeV2 := filepath.Join(tmp, "dummy_v2.exe")

	_ = os.WriteFile(src1, []byte("package main\nimport \"time\"\nfunc main(){ time.Sleep(30*time.Second) }\n"), 0644)
	_ = os.WriteFile(src2, []byte("package main\nfunc main(){}\n"), 0644)

	if out, err := exec.Command("go", "build", "-o", exeTarget, src1).CombinedOutput(); err != nil {
		t.Fatalf("failed building dummy v1: %v (%s)", err, string(out))
	}
	if out, err := exec.Command("go", "build", "-o", exeV2, src2).CombinedOutput(); err != nil {
		t.Fatalf("failed building dummy v2: %v (%s)", err, string(out))
	}
	v2Bytes, err := os.ReadFile(exeV2)
	if err != nil {
		t.Fatal(err)
	}

	proc := exec.Command(exeTarget)
	if err := proc.Start(); err != nil {
		t.Fatalf("failed starting dummy exe: %v", err)
	}
	defer func() {
		if proc.Process != nil {
			_ = proc.Process.Kill()
			_, _ = proc.Process.Wait()
		}
	}()
	time.Sleep(250 * time.Millisecond)

	if err := WriteFileAtomicWithRenameFallback(exeTarget, v2Bytes); err != nil {
		t.Fatalf("WriteFileAtomicWithRenameFallback failed on running exe: %v", err)
	}
	diskBytes, err := os.ReadFile(exeTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(diskBytes, v2Bytes) {
		t.Fatal("disk bytes do not match v2 replacement")
	}
}

func TestClaudeDesktopConfigAndRollbackInTemp(t *testing.T) {
	tmp := t.TempDir()
	localApp := filepath.Join(tmp, "Local")
	dataDir := filepath.Join(tmp, "Data")
	_ = os.MkdirAll(localApp, 0755)

	summary, err := ConfigureClaudeDesktop(dataDir, localApp, ClaudeOptions{
		ProxyURL:   "http://127.0.0.1:28080",
		APIKey:     "sk-or-v1-testtoken123456",
		BaseURL:    "https://openrouter.ai/api",
		AuthScheme: "bearer",
	})
	if err != nil || summary == "" {
		t.Fatalf("ConfigureClaudeDesktop failed: %v", err)
	}

	metaBytes, err := os.ReadFile(filepath.Join(localApp, "Claude-3p", "configLibrary", "_meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatal(err)
	}
	id := strAny(meta["appliedId"])
	if !uuidRe.MatchString(id) {
		t.Fatalf("invalid appliedId: %q", id)
	}
	profBytes, err := os.ReadFile(filepath.Join(localApp, "Claude-3p", "configLibrary", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var prof map[string]any
	_ = json.Unmarshal(profBytes, &prof)
	if strAny(prof["egressProxyUrl"]) != "http://127.0.0.1:28080" || strAny(prof["inferenceProvider"]) != "gateway" {
		t.Fatalf("unexpected profile contents: %+v", prof)
	}

	removed, err := RemoveClaudeDesktopProxy(localApp)
	if err != nil || removed != 1 {
		t.Fatalf("expected 1 removed profile, got %d err=%v", removed, err)
	}
}

func TestPatchAndRestoreShortcuts(t *testing.T) {
	tmp := longPath(t.TempDir())
	dataDir := filepath.Join(tmp, "data")
	scDir := filepath.Join(tmp, "shortcuts")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(scDir, 0755); err != nil {
		t.Fatal(err)
	}

	lnkPath := filepath.Join(scDir, "Antigravity.lnk")
	origTarget := filepath.Join(tmp, "OriginalAntigravity.exe")
	selfExe := filepath.Join(dataDir, "GravProxy.exe")
	_ = os.WriteFile(origTarget, []byte("stub"), 0644)
	_ = os.WriteFile(selfExe, []byte("stub"), 0644)

	if err := withWScriptShell(func(wshell *ole.IDispatch) error {
		scVar, err := oleutil.CallMethod(wshell, "CreateShortcut", lnkPath)
		if err != nil {
			return err
		}
		sc := scVar.ToIDispatch()
		defer sc.Release()
		_, _ = oleutil.PutProperty(sc, "TargetPath", origTarget)
		_, _ = oleutil.PutProperty(sc, "Arguments", "--orig-flag")
		_, _ = oleutil.PutProperty(sc, "WorkingDirectory", tmp)
		_, err = oleutil.CallMethod(sc, "Save")
		return err
	}); err != nil {
		t.Fatalf("failed to create test shortcut: %v", err)
	}

	// Simulate an MTA-initialized caller thread (like Wails WebView2 worker threads).
	runtime.LockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err == nil {
		defer ole.CoUninitialize()
	}
	defer runtime.UnlockOSThread()

	patched := PatchShortcutsInRoots(dataDir, selfExe, []string{scDir}, true, false)
	if patched != 1 {
		t.Fatalf("PatchShortcutsInRoots = %d, want 1", patched)
	}

	var curTarget, curArgs string
	if err := withWScriptShell(func(wshell *ole.IDispatch) error {
		scVar, err := oleutil.CallMethod(wshell, "CreateShortcut", lnkPath)
		if err != nil {
			return err
		}
		sc := scVar.ToIDispatch()
		defer sc.Release()
		curTarget = oleStrProp(sc, "TargetPath")
		curArgs = oleStrProp(sc, "Arguments")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(longPath(curTarget), selfExe) || curArgs != "--launch antigravity" {
		t.Fatalf("unexpected patched shortcut: target=%q (want %q) args=%q", curTarget, selfExe, curArgs)
	}

	bakPath := filepath.Join(dataDir, "shortcuts-backup.json")
	if _, err := os.Stat(bakPath); err != nil {
		t.Fatalf("expected shortcuts-backup.json to exist: %v", err)
	}

	restored := RestoreShortcuts(dataDir)
	if restored != 1 {
		t.Fatalf("RestoreShortcuts = %d, want 1", restored)
	}
	if _, err := os.Stat(bakPath); !os.IsNotExist(err) {
		t.Fatalf("expected shortcuts-backup.json to be removed after restore, err=%v", err)
	}

	if err := withWScriptShell(func(wshell *ole.IDispatch) error {
		scVar, err := oleutil.CallMethod(wshell, "CreateShortcut", lnkPath)
		if err != nil {
			return err
		}
		sc := scVar.ToIDispatch()
		defer sc.Release()
		curTarget = oleStrProp(sc, "TargetPath")
		curArgs = oleStrProp(sc, "Arguments")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(longPath(curTarget), origTarget) || curArgs != "--orig-flag" {
		t.Fatalf("unexpected restored shortcut: target=%q (want %q) args=%q", curTarget, origTarget, curArgs)
	}
}

func TestAncestorPIDSetContainsSelf(t *testing.T) {
	anc := ancestorPIDSet()
	self := uint32(os.Getpid())
	if _, ok := anc[self]; !ok {
		t.Fatalf("expected ancestorPIDSet to include self pid %d", self)
	}
	if ppid := uint32(os.Getppid()); ppid != 0 {
		if _, ok := anc[ppid]; !ok {
			t.Fatalf("expected ancestorPIDSet to include parent pid %d", ppid)
		}
	}
	if got := matchTargetApp("language_server_windows_x64.exe", true, false, false); got != "Antigravity" {
		t.Fatalf("expected Antigravity label for language_server, got %q", got)
	}
	if got := matchTargetApp("opencode-cli.exe", false, true, false); got != "OpenCode" {
		t.Fatalf("expected OpenCode label for opencode-cli, got %q", got)
	}
}

func TestGUIPidFileLifecycle(t *testing.T) {
	WriteGUIPidFile()
	raw, err := os.ReadFile(guiPidFilePath())
	if err != nil {
		t.Fatalf("expected pid file to exist: %v", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		t.Fatal("expected non-empty pid file")
	}
	RemoveGUIPidFile()
	if _, err := os.Stat(guiPidFilePath()); !os.IsNotExist(err) {
		t.Fatalf("expected pid file to be removed, err=%v", err)
	}
}
