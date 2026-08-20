package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func askReader(s string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(s))
}

func TestAskDefaultYes(t *testing.T) {
	got, err := ask(askReader("\n"), "question?", true)
	if err != nil || !got {
		t.Fatalf("empty input with default true: got %v, err %v", got, err)
	}
}

func TestAskDefaultNo(t *testing.T) {
	got, err := ask(askReader("\n"), "question?", false)
	if err != nil || got {
		t.Fatalf("empty input with default false: got %v, err %v", got, err)
	}
}

func TestAskYesNo(t *testing.T) {
	for _, tc := range []struct {
		in  string
		def bool
		exp bool
	}{
		{"y", false, true},
		{"yes", false, true},
		{"Y", false, true},
		{"n", true, false},
		{"no", true, false},
		{"N", true, false},
	} {
		got, err := ask(askReader(tc.in), "question?", tc.def)
		if err != nil || got != tc.exp {
			t.Fatalf("input %q def %v: got %v, err %v", tc.in, tc.def, got, err)
		}
	}
}

func TestAskInvalidReprompts(t *testing.T) {
	// First line is garbage, second line answers — should settle on the
	// second answer, not error.
	got, err := ask(askReader("maybe\nn\n"), "question?", true)
	if err != nil || got {
		t.Fatalf("invalid input should re-prompt then take n: got %v, err %v", got, err)
	}
}

func TestMissingDeps(t *testing.T) {
	// Everything present.
	found := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	if got := missingDeps(found, requiredDeps); len(got) != 0 {
		t.Fatalf("all present, got missing %v", got)
	}

	// Nothing present: both screenshot options reported.
	none := func(name string) (string, error) { return "", os.ErrNotExist }
	got := missingDeps(none, requiredDeps)
	names := []string{}
	for _, d := range got {
		names = append(names, d.bin)
	}
	for _, want := range []string{"flameshot", "mate-screenshot", "zenity", "notify-send", "xclip"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected %q in missing list, got %v", want, names)
		}
	}

	// Only flameshot present: the mate-screenshot fallback is not reported.
	onlyFlameshot := func(name string) (string, error) {
		if name == "flameshot" {
			return "/usr/bin/flameshot", nil
		}
		return "", os.ErrNotExist
	}
	got = missingDeps(onlyFlameshot, requiredDeps)
	for _, d := range got {
		if d.bin == "mate-screenshot" {
			t.Fatalf("mate-screenshot fallback should not be reported when flameshot is installed: %v", got)
		}
		if d.bin == "flameshot" {
			t.Fatalf("flameshot should not be missing: %v", got)
		}
	}

	// Optional tools are not part of the required set: nothing the wizard
	// installs by default should overlap with a bonus feature.
	for _, opt := range optionalDeps {
		for _, req := range requiredDeps {
			if opt.bin == req.bin {
				t.Fatalf("%s listed as both optional and required", opt.bin)
			}
		}
	}
}

func TestCurrentShell(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/zsh")
	if got := currentShell(); got != "zsh" {
		t.Fatalf("zsh: got %q", got)
	}
	t.Setenv("SHELL", "/bin/bash")
	if got := currentShell(); got != "bash" {
		t.Fatalf("bash: got %q", got)
	}
	t.Setenv("SHELL", "/usr/bin/fish")
	if got := currentShell(); got != "" {
		t.Fatalf("fish should be unsupported, got %q", got)
	}
}

func TestConfigExistsFalseWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if configExists() {
		t.Fatal("configExists should be false with no config on disk")
	}
}

func TestHookBlockEnd(t *testing.T) {
	text := "# marker\nif true; then\n  if true; then\n  fi\nfi\n# tail\n"
	// Marker at 0; the top-level `fi` is the line after the indented one.
	end := hookBlockEnd(text, 0)
	if end != len(text)-len("# tail\n") {
		t.Fatalf("hookBlockEnd = %d, want %d", end, len(text)-len("# tail\n"))
	}
	// An unterminated block yields -1.
	if end := hookBlockEnd("# marker\nif true; then\n", 0); end != -1 {
		t.Fatalf("unterminated block: hookBlockEnd = %d, want -1", end)
	}
}

func TestInstallHookAppendsWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := installHook("bash"); err != nil {
		t.Fatalf("installHook: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatalf("read .bashrc: %v", err)
	}
	if !strings.Contains(string(data), "_hook-mark") || !strings.Contains(string(data), "_hook-record") || !strings.Contains(string(data), "--exit-code") {
		t.Fatalf(".bashrc missing hook helpers (exit-code feeds auto mode):\n%s", data)
	}
}

func TestInstallHookIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := installHook("bash"); err != nil {
		t.Fatal(err)
	}
	if err := installHook("bash"); err != nil {
		t.Fatalf("second install should be a no-op, got %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	if strings.Count(string(data), hookMarker) != 1 {
		t.Fatalf("hook appended twice:\n%s", data)
	}
}

func TestInstallHookUpgradesStaleBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Simulate a hook installed by an older snapshell: same marker and
	// structure, but calling the old `shellhook` helper names.
	stale := "# --- snapshell shell integration ---\n" +
		"if command -v snapshell >/dev/null 2>&1; then\n" +
		"  _snapshell_mark_start() { snapshell shellhook mark --pane \"$TMUX_PANE\" --phase start; }\n" +
		"fi\n" +
		"# this line must survive\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := installHook("bash"); err != nil {
		t.Fatalf("installHook: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "shellhook mark") {
		t.Fatalf("stale helper call survived the upgrade:\n%s", text)
	}
	if !strings.Contains(text, "_hook-mark") {
		t.Fatalf("upgraded hook missing _hook-mark:\n%s", text)
	}
	if !strings.HasSuffix(text, "# this line must survive\n") {
		t.Fatalf("content after the hook block was lost:\n%s", text)
	}
}
