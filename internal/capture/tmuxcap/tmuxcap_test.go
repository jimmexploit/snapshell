package tmuxcap

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeTmuxScript emulates the three tmux invocations this package makes,
// using only shell builtins so it works on an isolated PATH:
//
//   - display-message -p '#{pane_id}'            → %0
//   - display-message -p -t <pane> '#{history_size}' → value from hsFile
//   - capture-pane -p -S <s> -E <e> -t <pane>     → "row <s>".."row <e>",
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
    eval fmt=\${$#}
    case "$fmt" in
      '#{pane_id}') echo '%0' ;;
      '#{history_size}')
        IFS= read -r hs < "$TMUXCAP_HS_FILE"
        echo "$hs"
        ;;
    esac
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

// setUp isolates PATH to the fake tmux, points the markers dir at a temp
// dir, and configures the fake's inputs. hs is the history_size the fake
// reports.
func setUp(t *testing.T, hs string) (markersDir string, argsFile string) {
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

	markersDir = t.TempDir()
	return markersDir, argsFile
}

func writeMarker(t *testing.T, dir, pane, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, pane+".last"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureFullRange(t *testing.T) {
	md, argsFile := setUp(t, "3")
	writeMarker(t, md, "%0", "9\n10\n15\n") // prev=9, start=10, end=15

	res, err := Capture(md, true)
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

func TestCaptureNegativeRangeIntoHistory(t *testing.T) {
	// Scrolled output: marker rows far above the current visible top
	// (hs huge) must produce negative capture-pane rows.
	md, argsFile := setUp(t, "200")
	writeMarker(t, md, "%0", "29\n30\n42\n")

	res, err := Capture(md, true)
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
	md, argsFile := setUp(t, "0")
	writeMarker(t, md, "%0", "4\n5\n5\n") // no output: start == end

	res, err := Capture(md, true)
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
	md, argsFile := setUp(t, "0")
	writeMarker(t, md, "%0", "7\n9\n14\n")

	res, err := Capture(md, true)
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
	md, argsFile := setUp(t, "0")
	writeMarker(t, md, "%0", "7\n9\n14\n")

	res, err := Capture(md, false)
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

func TestCaptureCommandOnlyLegacyMarkerFallsBackToCommandLine(t *testing.T) {
	// Legacy 2-row marker (no prev row): command-only falls back to
	// start-1, the row the command was typed on.
	md, argsFile := setUp(t, "0")
	writeMarker(t, md, "%0", "9\n14\n")

	res, err := Capture(md, false)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Text != "row 8\n" { // [start-1 .. start-1] = [8 .. 8]
		t.Fatalf("Text = %q, want %q", res.Text, "row 8\n")
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "-S 8 -E 8") {
		t.Fatalf("capture-pane args = %q, want -S 8 -E 8", args)
	}
}

func TestCaptureMissingMarkerIsActionable(t *testing.T) {
	md, _ := setUp(t, "0")
	_, err := Capture(md, true)
	if err == nil {
		t.Fatal("expected error for missing marker")
	}
	if !strings.Contains(err.Error(), "shell hook is sourced") {
		t.Fatalf("error should tell the user to check the shell hook, got: %v", err)
	}
}

func TestCaptureDegenerateMarker(t *testing.T) {
	md, _ := setUp(t, "0")
	writeMarker(t, md, "%0", "3\n2\n") // end < start
	if _, err := Capture(md, true); err == nil {
		t.Fatal("degenerate marker should error")
	}
}

func TestFocusedPaneMissingTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty
	if _, err := FocusedPane(); err == nil {
		t.Fatal("missing tmux should error")
	} else if !strings.Contains(err.Error(), "tmux") {
		t.Fatalf("error should name tmux, got: %v", err)
	}
}

func TestCaptureNotInTmux(t *testing.T) {
	md, _ := setUp(t, "0")
	t.Setenv("TMUXCAP_NOTMUX", "1")
	_, err := Capture(md, true)
	if err == nil {
		t.Fatal("not-in-tmux should error")
	}
	if !strings.Contains(err.Error(), "not in a tmux session") {
		t.Fatalf("error should mention not in a tmux session, got: %v", err)
	}
}
