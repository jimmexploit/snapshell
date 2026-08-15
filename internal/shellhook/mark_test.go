package shellhook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"snapshell/internal/daemon"
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
	if _, err := Mark("%1", "start", ""); err != nil {
		t.Fatalf("Mark start: %v", err)
	}
	path := filepath.Join(markersDir(), "%1.last")
	data, _ := os.ReadFile(path)
	if string(data) != "-1\n5\n-1\n" {
		t.Fatalf("start marker = %q, want %q", data, "-1\n5\n-1\n")
	}

	// Change the reported position: history_size=2, cursor_y=7 → abs end=9.
	if err := os.WriteFile(stateFile, []byte("2 7"), 0o600); err != nil {
		t.Fatal(err)
	}
	row, err := Mark("%1", "end", "")
	if err != nil {
		t.Fatalf("Mark end: %v", err)
	}
	if row != 9 {
		t.Fatalf("Mark end row = %d, want 9", row)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "-1\n5\n9\n" {
		t.Fatalf("end marker = %q, want %q", data, "-1\n5\n9\n")
	}
}

func TestMarkStartWithPrevEnd(t *testing.T) {
	setUp(t, "0 5")
	if _, err := Mark("%2", "start", "3"); err != nil {
		t.Fatalf("Mark start with prev-end: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(markersDir(), "%2.last"))
	if string(data) != "3\n5\n-1\n" {
		t.Fatalf("start marker = %q, want %q", data, "3\n5\n-1\n")
	}
}

func TestMarkEndPreservesPrevEnd(t *testing.T) {
	stateFile := setUp(t, "0 5")
	if _, err := Mark("%1", "start", "3"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, []byte("2 7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark("%1", "end", ""); err != nil {
		t.Fatalf("Mark end: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(markersDir(), "%1.last"))
	if string(data) != "3\n5\n9\n" {
		t.Fatalf("end marker = %q, want %q", data, "3\n5\n9\n")
	}
}

func TestMarkEndWithoutStartIsNoop(t *testing.T) {
	setUp(t, "2 7")
	row, err := Mark("%9", "end", "")
	if err != nil {
		t.Fatalf("Mark end with no start should be a no-op, got %v", err)
	}
	if row != -1 {
		t.Fatalf("no-op end should return -1, got %d", row)
	}
	if _, err := os.Stat(filepath.Join(markersDir(), "%9.last")); !os.IsNotExist(err) {
		t.Fatal("no marker file should be created without a start")
	}
}

func TestMarkBadPhase(t *testing.T) {
	setUp(t, "0 5")
	if _, err := Mark("%1", "middle", ""); err == nil {
		t.Fatal("bad phase should error")
	}
}

func TestMarkMissingTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty
	if _, err := Mark("%1", "start", ""); err == nil {
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
	if !strings.Contains(BashSnippet, "internal-popup-inline") {
		t.Fatal("bash snippet must run the inline caption form at the prompt")
	}
	if !strings.Contains(ZshSnippet, "internal-popup-inline") {
		t.Fatal("zsh snippet must run the inline caption form at the prompt")
	}
	if !strings.Contains(BashSnippet, "record-command") || !strings.Contains(ZshSnippet, "record-command") {
		t.Fatal("snippets must record the command text for the plain-shell fallback")
	}
}

func TestRecordCommand(t *testing.T) {
	setUp(t, "0 5") // redirects markers dir; lastcommand lives in state dir
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := RecordCommand("uname -r"); err != nil {
		t.Fatalf("RecordCommand: %v", err)
	}
	data, err := os.ReadFile(daemon.LastCommandPath())
	if err != nil {
		t.Fatalf("read lastcommand: %v", err)
	}
	if string(data) != "uname -r\n" {
		t.Fatalf("lastcommand = %q, want %q", data, "uname -r\n")
	}
	// A later command replaces the earlier one.
	if err := RecordCommand("whoami"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(daemon.LastCommandPath())
	if string(data) != "whoami\n" {
		t.Fatalf("lastcommand = %q, want %q", data, "whoami\n")
	}
}
