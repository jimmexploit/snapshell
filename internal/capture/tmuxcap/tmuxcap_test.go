package tmuxcap

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeTmuxScript emulates the two tmux invocations this package makes,
// using only shell builtins so it works on an isolated PATH:
//
//   - display-message -p -t <pane> '#{history_size}' → value from hsFile
//   - capture-pane -p -S <s> -E <e> -t <pane>        → "row <s>".."row <e>",
//     and records the full arg list into argsFile so tests can assert the
//     exact translated range.
//
// TMUXCAP_NOTMUX=1 makes display-message exit 1 (simulates no tmux server).
const fakeTmuxScript = `#!/bin/sh
sub="$1"
case "$sub" in
  display-message)
    if [ "$TMUXCAP_NOTMUX" = "1" ]; then
      echo "no server running" >&2
      exit 1
    fi
    IFS= read -r hs < "$TMUXCAP_HS_FILE"
    echo "$hs"
    ;;
  capture-pane)
    echo "$@" > "$TMUXCAP_ARGS_FILE"
    s=""
    e=""
    i=1
    while [ "$i" -le "$#" ]; do
      eval a=\${$i}
      if [ "$a" = "-S" ]; then
        i=$((i+1))
        eval s=\${$i}
      elif [ "$a" = "-E" ]; then
        i=$((i+1))
        eval e=\${$i}
      fi
      i=$((i+1))
    done
    n=$s
    while [ "$n" -le "$e" ]; do
      echo "row $n"
      n=$((n+1))
    done
    ;;
esac
`

// fakeKittyScript emulates `kitty @ get-text --to <sock> --match id:<wid>
// --extent last_cmd_output`: it returns the contents of KITTYCAP_OUTPUT_FILE
// and records the full arg list into KITTYCAP_ARGS_FILE so tests can assert
// the exact invocation. KITTYCAP_FAIL=1 makes it exit 1 (window gone).
const fakeKittyScript = `#!/bin/sh
case "$*" in
  *get-text*)
    echo "$@" > "$KITTYCAP_ARGS_FILE"
    if [ "$KITTYCAP_FAIL" = "1" ]; then
      echo "kitty: could not connect to window" >&2
      exit 1
    fi
    cat "$KITTYCAP_OUTPUT_FILE"
    ;;
  *)
    echo "unexpected kitty args: $*" >&2
    exit 1
    ;;
esac
`

// setUpKitty isolates PATH to a fake kitty (no tmux — ktty records never
// touch tmux) whose get-text returns output. Returns the log path and the
// file where the fake kitty records its args.
func setUpKitty(t *testing.T, output string) (logFile string, kittyArgsFile string) {
	t.Helper()
	binDir := t.TempDir()
	kittyArgsFile = filepath.Join(t.TempDir(), "kittyargs")
	outputFile := filepath.Join(t.TempDir(), "kout")
	if err := os.WriteFile(outputFile, []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "kitty"), []byte(fakeKittyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// binDir shadows the real kitty (LookPath finds the fake first); the
	// real PATH stays so the fake's own `cat` works.
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("KITTYCAP_ARGS_FILE", kittyArgsFile)
	t.Setenv("KITTYCAP_OUTPUT_FILE", outputFile)
	t.Setenv("KITTYCAP_FAIL", "")

	logFile = filepath.Join(t.TempDir(), "commandlog")
	return logFile, kittyArgsFile
}

// setUp isolates PATH to the fake tmux and configures its inputs. hs is the
// history_size the fake reports.
func setUp(t *testing.T, hs string) (logFile string, argsFile string) {
	t.Helper()
	binDir := t.TempDir()
	hsFile := filepath.Join(t.TempDir(), "hs")
	argsFile = filepath.Join(t.TempDir(), "args")
	if err := os.WriteFile(hsFile, []byte(hs), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(fakeTmuxScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("TMUXCAP_HS_FILE", hsFile)
	t.Setenv("TMUXCAP_ARGS_FILE", argsFile)

	logFile = filepath.Join(t.TempDir(), "commandlog")
	return logFile, argsFile
}

// writeLog appends one or more "<pane> <prev> <start> <end>" lines to the
// command log, newest last.
func writeLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureFullRange(t *testing.T) {
	logFile, argsFile := setUp(t, "3")
	writeLog(t, logFile, "%0 9 10 15") // prev=9, start=10, end=15

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	// hs=3. From = prev (9) .. to = end-1 (14) → screen rows [9-3 .. 14-3]
	// = [6 .. 11].
	want := "row 6\nrow 7\nrow 8\nrow 9\nrow 10\nrow 11\n"
	if res.Text != want {
		t.Fatalf("Text = %q, want %q", res.Text, want)
	}

	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-S 6 -E 11") {
		t.Fatalf("capture-pane args = %q, want translated range -S 6 -E 11", args)
	}
}

func TestCaptureLastLineWins(t *testing.T) {
	// Two commands, from two different panes in the same session: `ls` ran
	// in %0, then `cat` ran in %7. The log's last line is the `cat` record,
	// so Alt+2 must capture %7 — not the earlier %0 record, and not by
	// guessing a focused pane.
	logFile, argsFile := setUp(t, "0")
	writeLog(t, logFile, "%0 9 10 15", "%7 20 21 30")

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	// hs=0, prev=20, end-1=29 → rows 20..29 (the %7 cat command).
	want := "row 20\nrow 21\nrow 22\nrow 23\nrow 24\nrow 25\nrow 26\nrow 27\nrow 28\nrow 29\n"
	if res.Text != want {
		t.Fatalf("Text = %q, want the last (%%7) command %q", res.Text, want)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-t %7") {
		t.Fatalf("capture-pane args = %q, want -t %%7", args)
	}
}

func TestCaptureSkipsTornLastLine(t *testing.T) {
	// A torn/partial last line (e.g. the shell was killed mid-write) must
	// not break the capture — fall back to the previous valid record.
	logFile, argsFile := setUp(t, "0")
	writeLog(t, logFile, "%0 9 10 15", "%7 20 21 30", "garbage")

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	want := "row 20\nrow 21\nrow 22\nrow 23\nrow 24\nrow 25\nrow 26\nrow 27\nrow 28\nrow 29\n"
	if res.Text != want {
		t.Fatalf("Text = %q, want previous valid record %q", res.Text, want)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-t %7") {
		t.Fatalf("capture-pane args = %q, want -t %%7", args)
	}
}

func TestCaptureNegativeRangeIntoHistory(t *testing.T) {
	// Scrolled output: marker rows far above the current visible top
	// (hs huge) must produce negative capture-pane rows.
	logFile, argsFile := setUp(t, "200")
	writeLog(t, logFile, "%0 29 30 42")

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	// range = [29-200 .. 42-1-200] = [-171 .. -159]
	want := ""
	for n := -171; n <= -159; n++ {
		want += "row " + strconv.Itoa(n) + "\n"
	}
	if res.Text != want {
		t.Fatalf("Text = %q, want %q", res.Text, want)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-S -171 -E -159") {
		t.Fatalf("capture-pane args = %q, want -S -171 -E -159", args)
	}
}

func TestCaptureNoOutputCollapsesToPromptLine(t *testing.T) {
	logFile, argsFile := setUp(t, "0")
	writeLog(t, logFile, "%0 4 5 5") // no output: start == end

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "row 4\n" { // [prev .. end-1] = [4 .. 4]
		t.Fatalf("Text = %q, want %q", res.Text, "row 4\n")
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-S 4 -E 4") {
		t.Fatalf("capture-pane args = %q, want -S 4 -E 4", args)
	}
}

func TestCaptureTwoLinePromptIncludesBothLines(t *testing.T) {
	// Two-line PS1: prompt starts at row 7 (line A), command on row 8
	// (line B), first output row 9, end 14. prev=7 captures line A too.
	logFile, argsFile := setUp(t, "0")
	writeLog(t, logFile, "%0 7 9 14")

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "row 7\nrow 8\nrow 9\nrow 10\nrow 11\nrow 12\nrow 13\n" {
		t.Fatalf("Text = %q, want rows 7..13", res.Text)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-S 7 -E 13") {
		t.Fatalf("capture-pane args = %q, want -S 7 -E 13", args)
	}
}

func TestCaptureCommandOnlyStopsAtCommandLine(t *testing.T) {
	// includeOutput=false: capture prompt lines + command, not output.
	logFile, argsFile := setUp(t, "0")
	writeLog(t, logFile, "%0 7 9 14")

	res, err := Capture(logFile, false)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "row 7\nrow 8\n" { // [prev .. start-1] = [7 .. 8]
		t.Fatalf("Text = %q, want %q", res.Text, "row 7\nrow 8\n")
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-S 7 -E 8") {
		t.Fatalf("capture-pane args = %q, want -S 7 -E 8", args)
	}
}

func TestCapturePlainRecordReturnsText(t *testing.T) {
	// A command typed in a plain terminal (kitty tab, no tmux) is logged as
	// "tty <source> <command...>". Alt+2 must return that text directly —
	// no tmux scrollback exists to capture from.
	logFile, _ := setUp(t, "0")
	writeLog(t, logFile, "tty /dev/pts/5 cat ~/.local/share/snapshell/global2/blog.md")

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "cat ~/.local/share/snapshell/global2/blog.md" {
		t.Fatalf("Text = %q, want the plain-record command text", res.Text)
	}
}

func TestCapturePlainLastBeatsTmuxEarlier(t *testing.T) {
	// The user ran commands in tmux, then opened a kitty tab and typed a
	// command. The last log line is the plain record, so Alt+2 must return
	// the kitty command — not the earlier tmux one.
	logFile, argsFile := setUp(t, "0")
	writeLog(t, logFile, "%3 31 33 62", "tty /dev/pts/5 whoami")

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "whoami" {
		t.Fatalf("Text = %q, want the plain (kitty) command %q", res.Text, "whoami")
	}
	// No tmux capture-pane should have run for a plain record.
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatalf("capture-pane should not run for a plain record")
	}
}

func TestCaptureTmuxLastBeatsPlainEarlier(t *testing.T) {
	// Plain command first, then a tmux command: last line wins, tmux record
	// is captured from the pane.
	logFile, argsFile := setUp(t, "0")
	writeLog(t, logFile, "tty /dev/pts/5 whoami", "%1 7 9 14")

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "row 7\nrow 8\nrow 9\nrow 10\nrow 11\nrow 12\nrow 13\n" {
		t.Fatalf("Text = %q, want the last (tmux) command capture", res.Text)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-t %1") {
		t.Fatalf("capture-pane args = %q, want -t %%1", args)
	}
}

func TestCapturePlainRecordWorksWithoutTmuxBinary(t *testing.T) {
	logFile, _ := setUp(t, "0")
	writeLog(t, logFile, "tty /dev/pts/5 whoami")
	t.Setenv("PATH", t.TempDir()) // no tmux anywhere
	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture of a plain record should not need tmux: %v", err)
	}
	if res.Text != "whoami" {
		t.Fatalf("Text = %q, want %q", res.Text, "whoami")
	}
}

func TestCaptureSkipsTornPlainLastLine(t *testing.T) {
	logFile, _ := setUp(t, "0")
	// A plain record with no text is torn/invalid; fall back to the tmux
	// record before it.
	writeLog(t, logFile, "%1 7 9 14", "tty /dev/pts/5")
	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "row 7\nrow 8\nrow 9\nrow 10\nrow 11\nrow 12\nrow 13\n" {
		t.Fatalf("Text = %q, want the previous valid (tmux) record", res.Text)
	}
}

func TestCaptureMissingLogIsActionable(t *testing.T) {
	logFile, _ := setUp(t, "0")
	_, err := Capture(logFile, true)
	if err == nil {
		t.Fatal("expected error for missing log")
	}
	if errors.Is(err, ErrNotInTmux) {
		t.Fatalf("empty command log is NOT a not-in-tmux condition, got: %v", err)
	}
	if !strings.Contains(err.Error(), "shell hook is sourced") {
		t.Fatalf("error should tell the user to check the shell hook, got: %v", err)
	}
}

func TestCaptureDegenerateRecord(t *testing.T) {
	logFile, _ := setUp(t, "0")
	writeLog(t, logFile, "%0 3 2 1") // end < start
	if _, err := Capture(logFile, true); err == nil {
		t.Fatal("degenerate record should error")
	}
	// An incomplete record (end -1) is also rejected as degenerate.
	logFile2, _ := setUp(t, "0")
	writeLog(t, logFile2, "%0 3 2 -1")
	if _, err := Capture(logFile2, true); err == nil {
		t.Fatal("incomplete record should error")
	}
}

func TestCaptureNotInTmux(t *testing.T) {
	logFile, _ := setUp(t, "0")
	writeLog(t, logFile, "%0 9 10 15")
	t.Setenv("TMUXCAP_NOTMUX", "1")
	_, err := Capture(logFile, true)
	if err == nil {
		t.Fatal("not-in-tmux should error")
	}
	if !errors.Is(err, ErrNotInTmux) {
		t.Fatalf("error should wrap ErrNotInTmux, got: %v", err)
	}
	if !strings.Contains(err.Error(), "not in a tmux session") {
		t.Fatalf("error should mention not in a tmux session, got: %v", err)
	}
}

func TestCaptureKittyRecordReturnsCommandWithOutput(t *testing.T) {
	// A command typed in a plain kitty tab with shell integration enabled is
	// logged as "ktty <source> <wid> <listen> <command...>". Alt+2 must read
	// the command's output back via `kitty @ get-text` and return command +
	// output.
	logFile, argsFile := setUpKitty(t, "PORT    STATE SERVICE\n22/tcp   open  ssh\n")
	writeLog(t, logFile, "ktty /dev/pts/9 3 unix:/tmp/kitty-2200 nmap -p 22 10.10.11.42")

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	want := "nmap -p 22 10.10.11.42\nPORT    STATE SERVICE\n22/tcp   open  ssh\n"
	if res.Text != want {
		t.Fatalf("Text = %q, want %q", res.Text, want)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--to unix:/tmp/kitty-2200") ||
		!strings.Contains(string(args), "--match id:3") ||
		!strings.Contains(string(args), "--extent last_cmd_output") {
		t.Fatalf("kitty get-text args = %q, want --to/--match id:3/--extent last_cmd_output", args)
	}
}

func TestCaptureKittyRecordEmptyOutputReturnsCommandOnly(t *testing.T) {
	// The window's shell had no shell integration marks (e.g. started before
	// the hook was installed), so get-text returns nothing — the command text
	// alone is still captured.
	logFile, _ := setUpKitty(t, "")
	writeLog(t, logFile, "ktty /dev/pts/9 3 unix:/tmp/kitty-2200 whoami")

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "whoami" {
		t.Fatalf("Text = %q, want command text only", res.Text)
	}
}

func TestCaptureKittyRecordSkipsOutputWhenDisabled(t *testing.T) {
	// includeOutput=false: command text only, kitty never invoked.
	logFile, argsFile := setUpKitty(t, "some output that must be ignored\n")
	writeLog(t, logFile, "ktty /dev/pts/9 3 unix:/tmp/kitty-2200 whoami")

	res, err := Capture(logFile, false)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "whoami" {
		t.Fatalf("Text = %q, want command text only", res.Text)
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatalf("kitty should not be invoked when output is disabled")
	}
}

func TestCaptureKittyRecordMissingKittyBinary(t *testing.T) {
	// The kitty binary is gone (or not on PATH): name it in the error.
	logFile, _ := setUpKitty(t, "ignored")
	writeLog(t, logFile, "ktty /dev/pts/9 3 unix:/tmp/kitty-2200 whoami")
	t.Setenv("PATH", t.TempDir())

	_, err := Capture(logFile, true)
	if err == nil {
		t.Fatal("missing kitty should error")
	}
	if !strings.Contains(err.Error(), "kitty not found on PATH") {
		t.Fatalf("error should name the missing kitty binary, got: %v", err)
	}
}

func TestCaptureKittyRecordGetTextFailureIsActionable(t *testing.T) {
	// The window closed or the socket went stale: surface it, don't crash.
	logFile, _ := setUpKitty(t, "ignored")
	writeLog(t, logFile, "ktty /dev/pts/9 3 unix:/tmp/kitty-2200 whoami")
	t.Setenv("KITTYCAP_FAIL", "1")

	_, err := Capture(logFile, true)
	if err == nil {
		t.Fatal("get-text failure should error")
	}
	if !strings.Contains(err.Error(), "kitty") || !strings.Contains(err.Error(), "window") {
		t.Fatalf("error should be actionable about kitty/window, got: %v", err)
	}
}

func TestCaptureKittyLastBeatsTmuxEarlier(t *testing.T) {
	// A tmux command, then a plain kitty command: the ktty record wins.
	logFile, _ := setUpKitty(t, "hello\n")
	writeLog(t, logFile, "%3 31 33 62", "ktty /dev/pts/9 3 unix:/tmp/kitty-2200 echo hi")

	res, err := Capture(logFile, true)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "echo hi\nhello\n" {
		t.Fatalf("Text = %q, want the last (kitty) command with output", res.Text)
	}
}

func TestCaptureMissingTmux(t *testing.T) {
	logFile, _ := setUp(t, "0")
	writeLog(t, logFile, "%0 9 10 15")
	t.Setenv("PATH", t.TempDir()) // no tmux
	_, err := Capture(logFile, true)
	if err == nil {
		t.Fatal("missing tmux should error")
	}
	if !errors.Is(err, ErrNotInTmux) {
		t.Fatalf("missing-tmux error should wrap ErrNotInTmux, got: %v", err)
	}
	if !strings.Contains(err.Error(), "tmux") {
		t.Fatalf("error should name tmux, got: %v", err)
	}
}

func TestCaptureNTwoConsecutiveCommands(t *testing.T) {
	// Two consecutive commands in the same pane. Record A ran rows
	// [prev=4 .. end-1=8], record B picked up where A left off
	// (prev=9=A.end, so [9 .. 14]). CaptureN(2) concatenates both with a
	// blank line — a widened single capture-pane range would produce the
	// same rows, since A's to == B's from-1.
	logFile, argsFile := setUp(t, "0")
	writeLog(t, logFile, "%0 4 5 9", "%0 9 10 15")

	res, err := CaptureN(logFile, true, 2)
	if err != nil {
		t.Fatalf("CaptureN: %v", err)
	}
	want := "row 4\nrow 5\nrow 6\nrow 7\nrow 8\n\nrow 9\nrow 10\nrow 11\nrow 12\nrow 13\nrow 14\n"
	if res.Text != want {
		t.Fatalf("Text = %q, want %q", res.Text, want)
	}
	if res.Count != 2 {
		t.Fatalf("Count = %d, want 2", res.Count)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-t %0") {
		t.Fatalf("capture-pane args = %q, want -t %%0", args)
	}
}

func TestCaptureNTwoCommandsDifferentPanes(t *testing.T) {
	// Two commands, each in its own pane: older %1 first, newer %0 last.
	// CaptureN(2) captures both, oldest first.
	logFile, _ := setUp(t, "0")
	writeLog(t, logFile, "%1 7 9 14", "%0 20 21 30")

	res, err := CaptureN(logFile, true, 2)
	if err != nil {
		t.Fatalf("CaptureN: %v", err)
	}
	want := "row 7\nrow 8\nrow 9\nrow 10\nrow 11\nrow 12\nrow 13\n\n" +
		"row 20\nrow 21\nrow 22\nrow 23\nrow 24\nrow 25\nrow 26\nrow 27\nrow 28\nrow 29\n"
	if res.Text != want {
		t.Fatalf("Text = %q, want %q", res.Text, want)
	}
	if res.Count != 2 {
		t.Fatalf("Count = %d, want 2", res.Count)
	}
}

func TestCaptureNFewerRecordsThanRequested(t *testing.T) {
	// Requested 3, but only two commands exist in the log: both are
	// captured and Count reports the real number.
	logFile, _ := setUp(t, "0")
	writeLog(t, logFile, "%0 4 5 9", "%0 9 10 15")

	res, err := CaptureN(logFile, true, 3)
	if err != nil {
		t.Fatalf("CaptureN: %v", err)
	}
	want := "row 4\nrow 5\nrow 6\nrow 7\nrow 8\n\nrow 9\nrow 10\nrow 11\nrow 12\nrow 13\nrow 14\n"
	if res.Text != want {
		t.Fatalf("Text = %q, want %q", res.Text, want)
	}
	if res.Count != 2 {
		t.Fatalf("Count = %d, want 2 (only two records exist)", res.Count)
	}
}

func TestCaptureNSingleKeepsHistoricalBehavior(t *testing.T) {
	// CaptureN(..., 1) — the default Alt+2 — must produce exactly what
	// Capture always produced (verbatim, no blank-line join).
	logFile, _ := setUp(t, "0")
	writeLog(t, logFile, "%0 4 5 9", "%0 9 10 15")

	res, err := CaptureN(logFile, true, 1)
	if err != nil {
		t.Fatalf("CaptureN: %v", err)
	}
	want := "row 9\nrow 10\nrow 11\nrow 12\nrow 13\nrow 14\n"
	if res.Text != want {
		t.Fatalf("Text = %q, want %q (last command verbatim)", res.Text, want)
	}
	if res.Count != 1 {
		t.Fatalf("Count = %d, want 1", res.Count)
	}
}

func TestCaptureNNegativeCountDefaultsToOne(t *testing.T) {
	logFile, _ := setUp(t, "0")
	writeLog(t, logFile, "%0 9 10 15")
	res, err := CaptureN(logFile, true, 0)
	if err != nil {
		t.Fatalf("CaptureN: %v", err)
	}
	if res.Count != 1 || res.Text != "row 9\nrow 10\nrow 11\nrow 12\nrow 13\nrow 14\n" {
		t.Fatalf("Count=%d Text=%q, want a single-command capture", res.Count, res.Text)
	}
}

func TestCaptureNMixedPlainAndTmux(t *testing.T) {
	// A plain-terminal command, then a tmux command: CaptureN(2) spans both,
	// returning the plain text followed by the tmux capture.
	logFile, argsFile := setUp(t, "0")
	writeLog(t, logFile, "tty /dev/pts/5 whoami", "%1 7 9 14")

	res, err := CaptureN(logFile, true, 2)
	if err != nil {
		t.Fatalf("CaptureN: %v", err)
	}
	want := "whoami\n\nrow 7\nrow 8\nrow 9\nrow 10\nrow 11\nrow 12\nrow 13\n"
	if res.Text != want {
		t.Fatalf("Text = %q, want %q", res.Text, want)
	}
	if res.Count != 2 {
		t.Fatalf("Count = %d, want 2", res.Count)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-t %1") {
		t.Fatalf("capture-pane args = %q, want -t %%1", args)
	}
}

func TestCaptureNCommandOnlyStopsAtEachCommandLine(t *testing.T) {
	// includeOutput=false applies per record: each captures its prompt lines
	// + command only, no output rows.
	logFile, _ := setUp(t, "0")
	writeLog(t, logFile, "%0 4 5 9", "%0 9 10 15")

	res, err := CaptureN(logFile, false, 2)
	if err != nil {
		t.Fatalf("CaptureN: %v", err)
	}
	want := "row 4\n\nrow 9\n"
	if res.Text != want {
		t.Fatalf("Text = %q, want %q", res.Text, want)
	}
	if res.Count != 2 {
		t.Fatalf("Count = %d, want 2", res.Count)
	}
}
