//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPlatformDesktopAppDataBasesUsesLocalAndLegacyRoamingFolders(t *testing.T) {
	local, legacy, err := platformDesktopAppDataBases()
	if err != nil {
		t.Fatal(err)
	}
	wantLocal, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		t.Fatal(err)
	}
	wantLegacy, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(local) || !filepath.IsAbs(legacy) {
		t.Fatalf("app-data bases must be absolute: local=%q legacy=%q", local, legacy)
	}
	if filepath.Clean(local) != filepath.Clean(wantLocal) {
		t.Fatalf("local base = %q, want LocalAppData %q", local, wantLocal)
	}
	if filepath.Clean(legacy) != filepath.Clean(wantLegacy) {
		t.Fatalf("legacy base = %q, want roaming AppData %q", legacy, wantLegacy)
	}
}
