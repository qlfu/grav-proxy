package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qlfu/grav-proxy/internal/config"
)

func TestInstalledStateIgnoresOrphanExeWithoutConfig(t *testing.T) {
	dir := t.TempDir()
	orphanExe := filepath.Join(dir, "GravProxy.exe")
	if err := os.WriteFile(orphanExe, []byte("MZ"), 0644); err != nil {
		t.Fatal(err)
	}

	if isInstalledInDir(dir, false, false) {
		t.Fatal("expected installed=false when only orphan GravProxy.exe exists without config.json")
	}

	if err := os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if !isInstalledInDir(dir, false, false) {
		t.Fatal("expected installed=true when config.json exists")
	}
	if isInstalledInDir(dir, true, true) {
		t.Fatal("expected installed=false when pendingSelfDelete is true")
	}
}

func TestUninstallMoveSelfOutDeletesDataDirImmediately(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "GravProxy")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	selfExe := filepath.Join(dataDir, "GravProxy.exe")
	if err := os.WriteFile(selfExe, []byte("MZ"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, config.ConfigFileName), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	newSelf, moved := relocateSelfForUninstall(selfExe, dataDir)
	if !moved {
		t.Fatal("expected relocateSelfForUninstall to succeed inside TempDir")
	}
	defer func() { _ = os.Remove(newSelf) }()

	if isInsideDir(newSelf, dataDir) {
		t.Fatalf("relocated path %s must be outside dataDir %s", newSelf, dataDir)
	}
	if _, err := os.Stat(newSelf); err != nil {
		t.Fatalf("expected relocated binary at %s: %v", newSelf, err)
	}
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatalf("expected immediate RemoveAll(%s) to succeed: %v", dataDir, err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("expected dataDir %s to be deleted", dataDir)
	}
}
