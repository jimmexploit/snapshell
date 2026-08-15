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
	if cfg.Popup.Terminal != "alacritty" {
		t.Fatalf("terminal = %q, want alacritty", cfg.Popup.Terminal)
	}
	if !cfg.OutputIncluded() {
		t.Fatal("include_output should default to true")
	}
	if cfg.Popup.WidthCells != 100 || cfg.Popup.HeightCells != 30 {
		t.Fatalf("cells = %dx%d, want 100x30", cfg.Popup.WidthCells, cfg.Popup.HeightCells)
	}
	home, _ := os.UserHomeDir()
	if cfg.Paths.SessionRoot != filepath.Join(home, "snapshell") {
		t.Fatalf("session_root = %q, want expanded %s", cfg.Paths.SessionRoot, filepath.Join(home, "snapshell"))
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
	if err := os.WriteFile(path, []byte("[popup]\nterminal = \"xterm\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Popup.Terminal != "xterm" {
		t.Fatalf("terminal = %q, want configured xterm", cfg.Popup.Terminal)
	}
	if cfg.Screenshot.Tool != "flameshot" {
		t.Fatalf("tool = %q, want default flameshot", cfg.Screenshot.Tool)
	}
	if !cfg.OutputIncluded() {
		t.Fatal("include_output should default to true when the key is missing")
	}
	if cfg.Popup.WidthCells != 100 {
		t.Fatalf("width_cells = %d, want default 100", cfg.Popup.WidthCells)
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

func TestResolvePopupTerminalOrder(t *testing.T) {
	bin := t.TempDir()
	// Configured alacritty is absent; only kitty exists.
	if err := os.WriteFile(filepath.Join(bin, "kitty"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	cfg := Default()
	got, err := cfg.ResolvePopupTerminal()
	if err != nil {
		t.Fatalf("ResolvePopupTerminal: %v", err)
	}
	if got != "kitty" {
		t.Fatalf("got %q, want kitty", got)
	}
}

func TestResolvePopupTerminalNone(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cfg := Default()
	_, err := cfg.ResolvePopupTerminal()
	if err == nil {
		t.Fatal("expected an error when no terminal exists")
	}
	for _, name := range []string{"alacritty", "kitty", "xterm"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error should name %s, got: %v", name, err)
		}
	}
}

func TestResolvePopupTerminalPicksFirstAvailableFallback(t *testing.T) {
	bin := t.TempDir()
	// alacritty and kitty absent; mate-terminal present → mate-terminal.
	if err := os.WriteFile(filepath.Join(bin, "mate-terminal"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	got, err := Default().ResolvePopupTerminal()
	if err != nil {
		t.Fatalf("ResolvePopupTerminal: %v", err)
	}
	if got != "mate-terminal" {
		t.Fatalf("got %q, want mate-terminal fallback", got)
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
	// The on-disk file should document how to set [popup].terminal.
	for _, want := range []string{"mate-terminal", "include_output", "session_root"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("default file should document %q:\n%s", want, data)
		}
	}
	// Round-trip: re-reading the written file must reproduce the defaults.
	if got, err := LoadFrom(path); err != nil {
		t.Fatalf("reload: %v", err)
	} else if got.Popup.Terminal != cfg.Popup.Terminal || !got.OutputIncluded() {
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
