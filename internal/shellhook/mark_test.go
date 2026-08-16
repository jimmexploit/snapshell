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

// setUp isolates PATH to a fake tmux and redirects the markers dir and
// command log. pos is the initial "history_size cursor_y" the fake tmux
// reports.
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

	olog := commandLogPath
	lg := filepath.Join(t.TempDir(), "commandlog")
	commandLogPath = func() string { return lg }
	t.Cleanup(func() { commandLogPath = olog })

	oactive := activeSessionPath
	ap := filepath.Join(t.TempDir(), "activesession")
	activeSessionPath = func() string { return ap }
	t.Cleanup(func() { activeSessionPath = oactive })

	olast := lastCommandPath
	lc := filepath.Join(t.TempDir(), "lastcommand")
	lastCommandPath = func() string { return lc }
	t.Cleanup(func() { lastCommandPath = olast })
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

func TestMarkEndAppendsToCommandLog(t *testing.T) {
	stateFile := setUp(t, "0 5")
	if _, err := Mark("%1", "start", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, []byte("2 7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark("%1", "end", ""); err != nil {
		t.Fatalf("Mark end: %v", err)
	}

	data, err := os.ReadFile(commandLogPath())
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	// prev=-1, start=5, end=9 → "%1 -1 5 9"
	if string(data) != "%1 -1 5 9\n" {
		t.Fatalf("command log = %q, want %q", data, "%1 -1 5 9\n")
	}
}

func TestMarkEndAppendsMultipleCommands(t *testing.T) {
	stateFile := setUp(t, "0 5")
	// First command: start at 5, end at 9.
	if _, err := Mark("%1", "start", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, []byte("2 7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark("%1", "end", "9"); err != nil {
		t.Fatal(err)
	}
	// Second command in a different pane: start at 20, end at 25.
	if err := os.WriteFile(stateFile, []byte("0 20"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark("%7", "start", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, []byte("0 25"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark("%7", "end", "25"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(commandLogPath())
	want := "%1 -1 5 9\n%7 -1 20 25\n"
	if string(data) != want {
		t.Fatalf("command log = %q, want %q", data, want)
	}
}

func TestMarkEndWithoutStartAppendsNothing(t *testing.T) {
	setUp(t, "2 7")
	if _, err := Mark("%9", "end", ""); err != nil {
		t.Fatalf("Mark end with no start should be a no-op, got %v", err)
	}
	if _, err := os.Stat(commandLogPath()); !os.IsNotExist(err) {
		t.Fatal("command log should not exist when no command completed")
	}
}

func TestMarkEndRoutesToActiveSessionLog(t *testing.T) {
	stateFile := setUp(t, "0 5")
	// Simulate an active session: the daemon pointed activesession at this
	// session's log under <session_root>/logs/<name>/commands.log.
	sessLog := filepath.Join(t.TempDir(), "logs", "acme-box", "commands.log")
	if err := os.WriteFile(activeSessionPath(), []byte(sessLog), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Mark("%1", "start", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, []byte("2 7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark("%1", "end", ""); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(sessLog)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	if string(data) != "%1 -1 5 9\n" {
		t.Fatalf("session log = %q, want %q", data, "%1 -1 5 9\n")
	}
	// The global log must NOT have the record.
	if _, err := os.Stat(commandLogPath()); !os.IsNotExist(err) {
		t.Fatal("record must not go to the global log while a session is active")
	}
}

func TestMarkEndWithNoActiveSessionUsesGlobalLog(t *testing.T) {
	stateFile := setUp(t, "0 5")
	// No activesession pointer (empty/missing) → record goes to the global
	// command log.
	if _, err := Mark("%1", "start", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, []byte("2 7"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Mark("%1", "end", ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(commandLogPath())
	if err != nil {
		t.Fatalf("read global log: %v", err)
	}
	if string(data) != "%1 -1 5 9\n" {
		t.Fatalf("global log = %q, want %q", data, "%1 -1 5 9\n")
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
	if strings.Contains(BashSnippet, "internal-popup-inline") || strings.Contains(ZshSnippet, "internal-popup-inline") {
		t.Fatal("snippets must not reference the removed inline caption form")
	}
	if !strings.Contains(BashSnippet, "_hook-record") || !strings.Contains(ZshSnippet, "_hook-record") {
		t.Fatal("snippets must record the command text for the plain-shell fallback")
	}
	if !strings.Contains(BashSnippet, "_hook-mark") || !strings.Contains(ZshSnippet, "_hook-mark") {
		t.Fatal("snippets must call the hidden row-marker helper")
	}
}

func TestRecordCommand(t *testing.T) {
	setUp(t, "0 5")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := RecordCommand("pts/3", "", "", "uname -r"); err != nil {
		t.Fatalf("RecordCommand: %v", err)
	}
	data, err := os.ReadFile(lastCommandPath())
	if err != nil {
		t.Fatalf("read lastcommand: %v", err)
	}
	if string(data) != "uname -r\n" {
		t.Fatalf("lastcommand = %q, want %q", data, "uname -r\n")
	}
	// A later command replaces the earlier one.
	if err := RecordCommand("pts/3", "", "", "whoami"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(lastCommandPath())
	if string(data) != "whoami\n" {
		t.Fatalf("lastcommand = %q, want %q", data, "whoami\n")
	}
}

func TestRecordCommandAppendsToSessionHistory(t *testing.T) {
	setUp(t, "0 5")
	// Simulate an active session: the daemon pointed activesession at this
	// session's commands.log, so history goes next to it.
	sessLog := filepath.Join(t.TempDir(), "logs", "acme-box", "commands.log")
	if err := os.WriteFile(activeSessionPath(), []byte(sessLog), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RecordCommand("%1", "", "", "ls -la /tmp"); err != nil {
		t.Fatalf("RecordCommand: %v", err)
	}
	hist := filepath.Join(filepath.Dir(sessLog), "commands.history")
	data, err := os.ReadFile(hist)
	if err != nil {
		t.Fatalf("read session history: %v", err)
	}
	line := string(data)
	if !strings.Contains(line, "%1") || !strings.Contains(line, "ls -la /tmp") {
		t.Fatalf("history line = %q, want source %%1 and command text", line)
	}
	if !strings.Contains(line, "  ") {
		t.Fatalf("history line %q has no field separators", line)
	}

	// A second command from a plain terminal appends another history line.
	if err := RecordCommand("/dev/pts/3", "", "", "uname -r"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(hist)
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Fatalf("history has %d lines, want 2:\n%s", got, data)
	}
}

func TestRecordCommandPlainSourceAppendsTtyRecord(t *testing.T) {
	setUp(t, "0 5")
	sessLog := filepath.Join(t.TempDir(), "logs", "acme", "commands.log")
	if err := os.WriteFile(activeSessionPath(), []byte(sessLog), 0o600); err != nil {
		t.Fatal(err)
	}

	// A plain-terminal command goes into the unified command log as a
	// text-only "tty" record so Alt+2 can read it back.
	if err := RecordCommand("/dev/pts/5", "", "", "cat ~/blog.md"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sessLog)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	if string(data) != "tty /dev/pts/5 cat ~/blog.md\n" {
		t.Fatalf("command log = %q, want a tty record", data)
	}
}

func TestRecordCommandKittySourceAppendsKttyRecord(t *testing.T) {
	setUp(t, "0 5")
	sessLog := filepath.Join(t.TempDir(), "logs", "acme", "commands.log")
	if err := os.WriteFile(activeSessionPath(), []byte(sessLog), 0o600); err != nil {
		t.Fatal(err)
	}

	// A command typed in a plain kitty tab carries the window id + listen
	// socket so Alt+2 can read the output back from the window.
	if err := RecordCommand("/dev/pts/9", "3", "unix:/tmp/kitty-2200", "nmap -sV 10.10.11.42"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sessLog)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	want := "ktty /dev/pts/9 3 unix:/tmp/kitty-2200 nmap -sV 10.10.11.42\n"
	if string(data) != want {
		t.Fatalf("command log = %q, want %q", data, want)
	}
}

func TestRecordCommandTmuxSourceSkipsCommandLog(t *testing.T) {
	setUp(t, "0 5")
	sessLog := filepath.Join(t.TempDir(), "logs", "acme", "commands.log")
	if err := os.WriteFile(activeSessionPath(), []byte(sessLog), 0o600); err != nil {
		t.Fatal(err)
	}

	// tmux commands don't add a tty record — their row record is written by
	// the _hook-mark end phase; RecordCommand only feeds lastcommand + history.
	if err := RecordCommand("%4", "", "", "ls /"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessLog); !os.IsNotExist(err) {
		t.Fatalf("tmux command must not write a tty record to the command log")
	}
}

func TestRecordCommandEmptyTextIgnored(t *testing.T) {
	setUp(t, "0 5")
	sessLog := filepath.Join(t.TempDir(), "logs", "acme", "commands.log")
	if err := os.WriteFile(activeSessionPath(), []byte(sessLog), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordCommand("/dev/pts/5", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessLog); !os.IsNotExist(err) {
		t.Fatalf("empty command must not be recorded")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(sessLog), "commands.history")); !os.IsNotExist(err) {
		t.Fatalf("empty command must not be recorded in history")
	}
}

func TestRecordCommandCollapsesNewlines(t *testing.T) {
	setUp(t, "0 5")
	sessLog := filepath.Join(t.TempDir(), "logs", "acme", "commands.log")
	if err := os.WriteFile(activeSessionPath(), []byte(sessLog), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecordCommand("%1", "", "", "for i in 1 2\n\ndo\n\techo hi\ndone"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(sessLog), "commands.history"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "\n"); got != 1 {
		t.Fatalf("history must be one line per command, got %d lines:\n%s", got, data)
	}
}

func TestRecordCommandNoSessionOnlyLastCommand(t *testing.T) {
	setUp(t, "0 5") // no activesession pointer
	if err := RecordCommand("pts/7", "", "", "echo no-session"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(lastCommandPath())
	if err != nil || string(data) != "echo no-session\n" {
		t.Fatalf("lastcommand = %q (err=%v), want the command text", data, err)
	}
}
