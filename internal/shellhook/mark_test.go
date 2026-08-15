package shellhook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTmux emulates `tmux display-message -p -t <pane> '#{history_size}
// #{cursor_y}'` and reads its answer from a state file, so a test can
// change the reported position between calls. Uses only shell builtins so
// it works on the isolated PATH.
func fakeTmux(t *testing.T, binDir, stateFile string) {
	t.Helper()
	script := "#!/bin/sh\nIFS=' ' read HS CY < " + stateFile + "\necho \"$HS $CY\"\n"
	p := filepath.Join(binDir, "tmux")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// setUp isolates PATH to a fake tmux and redirects the markers dir. pos is
// the initial "history_size cursor_y" the fake tmux reports.
func setUp(t *testing.T, pos string) (stateFile string) {
	t.Helper()
	binDir := t.TempDir()
	stateFile = filepath.Join(t.TempDir(), "tmuxstate")
	if err := os.WriteFile(stateFile, []byte(pos), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeTmux(t, binDir, stateFile)
	t.Setenv("PATH", binDir) // never touch the real tmux

	orig := markersDir
	md := filepath.Join(t.TempDir(), "markers")
	markersDir = func() string { return md }
	t.Cleanup(func() { markersDir = orig })
	return stateFile
}

func TestMarkStartAndEnd(t *testing.T) {
	stateFile := setUp(t, "0 5") // abs start = 5
	if err := Mark("%1", "start"); err != nil {
		t.Fatalf("Mark start: %v", err)
	}
	path := filepath.Join(markersDir(), "%1.last")
	data, _ := os.ReadFile(path)
	if string(data) != "5\n" {
		t.Fatalf("start marker = %q, want %q", data, "5\n")
	}

	// Change the reported position: history_size=2, cursor_y=7 → abs end=9.
	if err := os.WriteFile(stateFile, []byte("2 7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Mark("%1", "end"); err != nil {
		t.Fatalf("Mark end: %v", err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "5\n9\n" {
		t.Fatalf("end marker = %q, want %q", data, "5\n9\n")
	}
}

func TestMarkEndWithoutStartIsNoop(t *testing.T) {
	setUp(t, "2 7")
	if err := Mark("%9", "end"); err != nil {
		t.Fatalf("Mark end with no start should be a no-op, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(markersDir(), "%9.last")); !os.IsNotExist(err) {
		t.Fatal("no marker file should be created without a start")
	}
}

func TestMarkBadPhase(t *testing.T) {
	setUp(t, "0 5")
	if err := Mark("%1", "middle"); err == nil {
		t.Fatal("bad phase should error")
	}
}

func TestMarkMissingTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty
	if err := Mark("%1", "start"); err == nil {
		t.Fatal("missing tmux should error")
	}
}

func TestSnippetsMentionShell(t *testing.T) {
	if !strings.Contains(BashSnippet, "DEBUG") || !strings.Contains(BashSnippet, "PROMPT_COMMAND") {
		t.Fatal("bash snippet must use DEBUG trap + PROMPT_COMMAND")
	}
	if !strings.Contains(ZshSnippet, "preexec") || !strings.Contains(ZshSnippet, "precmd") {
		t.Fatal("zsh snippet must use preexec + precmd")
	}
}
