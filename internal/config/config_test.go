package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCreatesDefaultFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Screenshot.Tool != "flameshot" {
		t.Fatalf("tool = %q, want flameshot", cfg.Screenshot.Tool)
	}
	if !cfg.OutputIncluded() {
		t.Fatal("include_output should default to true")
	}
	if cfg.Popup.Width != 560 || cfg.Popup.Height != 320 || cfg.Popup.Font != "Sans 13" {
		t.Fatalf("popup = %+v, want width 560 height 320 font Sans 13", cfg.Popup)
	}
	if cfg.Keymaps.Screenshot != "Alt+1" || cfg.Keymaps.Command != "Alt+2" || cfg.Keymaps.Note != "Alt+3" || cfg.Keymaps.Selection != "Alt+4" {
		t.Fatalf("keymaps = %+v, want Alt+1/Alt+2/Alt+3/Alt+4", cfg.Keymaps)
	}
	home, _ := os.UserHomeDir()
	if cfg.Paths.SessionRoot != filepath.Join(home, ".local", "share", "snapshell") {
		t.Fatalf("session_root = %q, want %s", cfg.Paths.SessionRoot, filepath.Join(home, ".local", "share", "snapshell"))
	}
	// The file must have been written with the defaults for the user to edit.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("default file not written: %v", err)
	}
	if !strings.Contains(string(data), "flameshot") || !strings.Contains(string(data), "session_root") {
		t.Fatalf("default file missing values:\n%s", data)
	}
}

func TestLoadPartialFillsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[popup]\nwidth = 600\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Popup.Width != 600 {
		t.Fatalf("width = %d, want configured 600", cfg.Popup.Width)
	}
	if cfg.Popup.Height != 320 {
		t.Fatalf("height = %d, want default 320", cfg.Popup.Height)
	}
	if cfg.Screenshot.Tool != "flameshot" {
		t.Fatalf("tool = %q, want default flameshot", cfg.Screenshot.Tool)
	}
	if cfg.Keymaps.Command != "Alt+2" {
		t.Fatalf("command keymap = %q, want default Alt+2", cfg.Keymaps.Command)
	}
	if !cfg.OutputIncluded() {
		t.Fatal("include_output should default to true when the key is missing")
	}
}

func TestCaptureIncludeOutputExplicitFalsePreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[capture]\ninclude_output = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.OutputIncluded() {
		t.Fatal("explicit include_output = false must be preserved, not reset to default")
	}
}

func TestResolveScreenshotToolFallback(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "mate-screenshot"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	cfg := Default() // configured = flameshot, missing here
	got, err := cfg.ResolveScreenshotTool()
	if err != nil {
		t.Fatalf("ResolveScreenshotTool: %v", err)
	}
	if got != "mate-screenshot" {
		t.Fatalf("got %q, want mate-screenshot fallback", got)
	}
}

func TestResolveScreenshotToolNone(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty
	cfg := Default()
	_, err := cfg.ResolveScreenshotTool()
	if err == nil {
		t.Fatal("expected an error when no tool exists")
	}
	if !strings.Contains(err.Error(), "flameshot") || !strings.Contains(err.Error(), "mate-screenshot") {
		t.Fatalf("error should name both tools, got: %v", err)
	}
}

func TestDefaultFileRoundTripsAndDocuments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The on-disk file should document the caption window config.
	for _, want := range []string{"width", "font", "include_output", "session_root", "zenity", "keymaps", "Alt+1"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("default file should document %q:\n%s", want, data)
		}
	}
	// Round-trip: re-reading the written file must reproduce the defaults.
	if got, err := LoadFrom(path); err != nil {
		t.Fatalf("reload: %v", err)
	} else if got.Popup.Width != cfg.Popup.Width || !got.OutputIncluded() {
		t.Fatalf("reloaded config drifted: %+v", got)
	}
}

func TestLoadBadToml(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[[[not valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(path); err == nil {
		t.Fatal("expected an error for malformed TOML")
	}
}

func TestResetDefaultCreatesWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := ResetDefault(); err != nil {
		t.Fatalf("ResetDefault: %v", err)
	}
	path := filepath.Join(home, ".config", "snapshell", "config.toml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config not created: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("no backup expected on first run, err=%v", err)
	}
}

func TestResetDefaultBacksUpExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "snapshell", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[screenshot]\ntool = \"mate-screenshot\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ResetDefault(); err != nil {
		t.Fatalf("ResetDefault: %v", err)
	}

	// The old config must survive as a backup.
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !strings.Contains(string(backup), "mate-screenshot") {
		t.Fatalf("backup should hold the previous config:\n%s", backup)
	}

	// The new config must be the documented defaults.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "flameshot") || !strings.Contains(string(data), "keymaps") {
		t.Fatalf("reset config should contain default values:\n%s", data)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom after reset: %v", err)
	}
	if cfg.Keymaps.Screenshot != "Alt+1" || cfg.Screenshot.Tool != "flameshot" {
		t.Fatalf("reset config drifted: %+v", cfg)
	}
}
