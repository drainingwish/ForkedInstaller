package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultBaseDirWithoutHome(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows HOME regression")
	}
	config := t.TempDir()
	t.Setenv("VENCORD_USER_DATA_DIR", "")
	t.Setenv("DISCORD_USER_DATA_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("APPDATA", config)
	t.Setenv("XDG_CONFIG_HOME", config)
	dir, err := resolveBaseDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(config, "Vencord"); dir != want {
		t.Fatalf("got %q, want %q", dir, want)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("relative directory: %q", dir)
	}
}

func TestExplicitBaseDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Cutcord with spaces")
	t.Setenv("VENCORD_USER_DATA_DIR", dir)
	got, err := resolveBaseDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("got %q, want %q", got, dir)
	}
}

func TestRelativeBaseDirBecomesAbsolute(t *testing.T) {
	t.Setenv("VENCORD_USER_DATA_DIR", "cutcord-relative")
	got, err := resolveBaseDir()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("relative directory: %q", got)
	}
}

func TestLoaderRejectsRelativePatcher(t *testing.T) {
	output := filepath.Join(t.TempDir(), "app.asar")
	if err := WriteAppAsar(output, filepath.Join(".config", "Vencord", "dist", "patcher.js")); err == nil {
		t.Fatal("expected relative patcher path to be rejected")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("invalid loader was written")
	}
}

func TestMissingPatcherPreservesDiscord(t *testing.T) {
	oldPatcher := Patcher
	t.Cleanup(func() { Patcher = oldPatcher })
	resources := t.TempDir()
	original := filepath.Join(resources, "app.asar")
	if err := os.WriteFile(original, []byte("original Discord archive"), 0644); err != nil {
		t.Fatal(err)
	}
	Patcher = filepath.Join(t.TempDir(), "missing-patcher.js")
	if err := patchAppAsar(resources); err == nil {
		t.Fatal("expected missing patcher to fail")
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "original Discord archive" {
		t.Fatal("original Discord archive was changed")
	}
	if _, err := os.Stat(filepath.Join(resources, "_app.asar")); !os.IsNotExist(err) {
		t.Fatal("original archive was renamed")
	}
}
