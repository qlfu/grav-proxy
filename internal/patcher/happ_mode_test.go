package patcher

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func deleteTestRegTree(subKey string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, subKey, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		_ = registry.DeleteKey(registry.CURRENT_USER, subKey)
		return
	}
	subs, _ := k.ReadSubKeyNames(-1)
	_ = k.Close()
	for _, s := range subs {
		deleteTestRegTree(subKey + `\` + s)
	}
	_ = registry.DeleteKey(registry.CURRENT_USER, subKey)
}

func TestHappTunOnlyLifecycle(t *testing.T) {
	rootKey := fmt.Sprintf(`Software\GravProxyTestHapp_%d`, time.Now().UnixNano())
	prefsKey := rootKey + `\Preferences`
	advKey := prefsKey + `\AdvancedSettings`
	defer deleteTestRegTree(rootKey)

	// Case 1: Fresh Happ (systemProxy unset = default enabled) -> sets systemProxy="false", then restores unset state.
	t.Run("fresh install unset systemProxy", func(t *testing.T) {
		pk, _, err := registry.CreateKey(registry.CURRENT_USER, prefsKey, registry.SET_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		_ = pk.Close()
		ak, _, err := registry.CreateKey(registry.CURRENT_USER, advKey, registry.SET_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		_ = ak.Close()

		dataDir := t.TempDir()
		changed, _, err := configureHappTunOnlyAtKey(prefsKey, advKey, dataDir, true)
		if err != nil || !changed {
			t.Fatalf("expected changed=true err=nil, got changed=%v err=%v", changed, err)
		}

		rk, err := registry.OpenKey(registry.CURRENT_USER, advKey, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		gotSys, _, gErr := rk.GetStringValue("systemProxy")
		_ = rk.Close()
		if gErr != nil || gotSys != "false" {
			t.Fatalf("expected systemProxy=false, got %q (err=%v)", gotSys, gErr)
		}

		restored, err := restoreHappSettingsAtKey(advKey, dataDir)
		if err != nil || !restored {
			t.Fatalf("expected restored=true err=nil, got restored=%v err=%v", restored, err)
		}
		rk2, err := registry.OpenKey(registry.CURRENT_USER, advKey, registry.QUERY_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		_, _, gErr2 := rk2.GetStringValue("systemProxy")
		_ = rk2.Close()
		if gErr2 == nil {
			t.Fatal("expected systemProxy value to be deleted on restore")
		}
		if _, err := os.Stat(filepath.Join(dataDir, happBackupFile)); !os.IsNotExist(err) {
			t.Fatal("expected happ-backup.json to be removed")
		}
	})

	// Case 2: systemProxy="true" and tun="false" -> switches both, restores originals.
	t.Run("systemProxy true and tun false", func(t *testing.T) {
		ak, _, err := registry.CreateKey(registry.CURRENT_USER, advKey, registry.SET_VALUE)
		if err != nil {
			t.Fatal(err)
		}
		_ = ak.SetStringValue("systemProxy", "true")
		_ = ak.SetStringValue("tun", "false")
		_ = ak.Close()

		dataDir := t.TempDir()
		changed, _, err := configureHappTunOnlyAtKey(prefsKey, advKey, dataDir, true)
		if err != nil || !changed {
			t.Fatalf("expected changed=true err=nil, got changed=%v err=%v", changed, err)
		}

		rk, _ := registry.OpenKey(registry.CURRENT_USER, advKey, registry.QUERY_VALUE)
		gotSys, _, _ := rk.GetStringValue("systemProxy")
		gotTun, _, _ := rk.GetStringValue("tun")
		_ = rk.Close()
		if gotSys != "false" || gotTun != "true" {
			t.Fatalf("expected systemProxy=false tun=true, got %q %q", gotSys, gotTun)
		}

		restored, err := restoreHappSettingsAtKey(advKey, dataDir)
		if err != nil || !restored {
			t.Fatalf("expected restored=true err=nil, got restored=%v err=%v", restored, err)
		}
		rk2, _ := registry.OpenKey(registry.CURRENT_USER, advKey, registry.QUERY_VALUE)
		restSys, _, _ := rk2.GetStringValue("systemProxy")
		restTun, _, _ := rk2.GetStringValue("tun")
		_ = rk2.Close()
		if restSys != "true" || restTun != "false" {
			t.Fatalf("expected restored systemProxy=true tun=false, got %q %q", restSys, restTun)
		}
	})

	// Case 3: Already systemProxy="false" -> no-op, no backup created.
	t.Run("already systemProxy false", func(t *testing.T) {
		ak, _, _ := registry.CreateKey(registry.CURRENT_USER, advKey, registry.SET_VALUE)
		_ = ak.SetStringValue("systemProxy", "false")
		_ = ak.DeleteValue("tun")
		_ = ak.Close()

		dataDir := t.TempDir()
		changed, _, err := configureHappTunOnlyAtKey(prefsKey, advKey, dataDir, true)
		if err != nil || changed {
			t.Fatalf("expected changed=false err=nil, got changed=%v err=%v", changed, err)
		}
		if _, err := os.Stat(filepath.Join(dataDir, happBackupFile)); !os.IsNotExist(err) {
			t.Fatal("expected no backup file when already in TUN-only mode")
		}
	})

	// Case 4: TUN service not available -> no-op, leaves systemProxy untouched.
	t.Run("tun unavailable noop", func(t *testing.T) {
		ak, _, _ := registry.CreateKey(registry.CURRENT_USER, advKey, registry.SET_VALUE)
		_ = ak.SetStringValue("systemProxy", "true")
		_ = ak.Close()

		dataDir := t.TempDir()
		changed, _, err := configureHappTunOnlyAtKey(prefsKey, advKey, dataDir, false)
		if err != nil || changed {
			t.Fatalf("expected changed=false err=nil when TUN unavailable, got changed=%v err=%v", changed, err)
		}
		rk, _ := registry.OpenKey(registry.CURRENT_USER, advKey, registry.QUERY_VALUE)
		gotSys, _, _ := rk.GetStringValue("systemProxy")
		_ = rk.Close()
		if gotSys != "true" {
			t.Fatalf("expected systemProxy to remain true, got %q", gotSys)
		}
	})
}
