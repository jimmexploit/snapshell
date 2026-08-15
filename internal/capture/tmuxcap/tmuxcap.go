package tmuxcap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Result describes a completed tmux capture.
type Result struct {
	// Text is the literal pane text spanning the last completed command:
	// the full prompt (all its lines), the command, and — when output is
	// included — its full output (including output that scrolled past the
	// visible screen).
	Text string
}

// Capture returns the exact text of the last completed command (and, when
// includeOutput is true, its output) from the focused tmux pane.
//
// markerDir is the directory holding the per-pane <pane_id>.last marker
// files written by the shell hook (internal/shellhook). The daemon passes
// its own state-derived markers dir.
//
// The marker file holds three absolute rows (history_size + cursor_y):
// prev_end (the row where the current prompt started, or -1 when unknown),
// start (the first output row), and end (the next prompt row). Empirically
// the start row is one below the last prompt line, because the terminal
// echoes the accepted newline before preexec/DEBUG fires, and the end row
// is one past the last output line. So with a known prev_end the capture
// begins at the top of the (possibly multi-line) prompt, and with
// includeOutput the capture runs to the last output line. When prev_end is
// unknown (first command in the pane) it falls back to start-1 — the last
// prompt line, which is where the command was typed on a single-line
// prompt.
func Capture(markerDir string, includeOutput bool) (Result, error) {
	pane, err := FocusedPane()
	if err != nil {
		return Result{}, err
	}

	prev, start, end, err := readMarker(markerDir, pane)
	if err != nil {
		return Result{}, err
	}
	if start < 1 || end < start {
		return Result{}, fmt.Errorf("marker for pane %s is degenerate (%d..%d) — rerun a command and try again", pane, start, end)
	}

	from := start - 1
	if prev >= 0 {
		from = prev
	}
	to := end - 1
	if !includeOutput {
		to = start - 1
	}

	hs, err := historySize(pane)
	if err != nil {
		return Result{}, err
	}

	text, err := captureRange(pane, from-hs, to-hs)
	if err != nil {
		return Result{}, err
	}
	return Result{Text: text}, nil
}

// FocusedPane resolves the tmux pane to capture from.
//
// The daemon runs outside any tmux client, so display-message without -t
// reports the pane of the tmux session most recently active (limitation
// noted in internal/capture/tmuxcap/AGENTS.md — if multiple clients are
// attached to different sessions, "the" focused pane isn't cleanly
// resolvable from a non-client; most-recently-active is the documented
// fallback).
func FocusedPane() (string, error) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return "", fmt.Errorf("tmux not found on PATH")
	}
	out, err := exec.Command(bin, "display-message", "-p", "#{pane_id}").Output()
	if err != nil {
		return "", fmt.Errorf("not in a tmux session (tmux display-message failed: %v) — open a tmux window first", err)
	}
	pane := strings.TrimSpace(string(out))
	if pane == "" {
		return "", fmt.Errorf("not in a tmux session — open a tmux window first")
	}
	return pane, nil
}

// readMarker parses the <pane>.last marker file into prev, start and end
// rows. A missing marker means the user pressed Alt+2 before running any
// command (or the hook isn't sourced) — that gets a specific, actionable
// message.
func readMarker(markerDir, pane string) (prev, start, end int, err error) {
	prev = -1
	data, err := os.ReadFile(filepath.Join(markerDir, pane+".last"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, 0, fmt.Errorf("no command captured yet for pane %s — check that the snapshell shell hook is sourced in your shell rc file", pane)
		}
		return 0, 0, 0, fmt.Errorf("read marker for pane %s: %v", pane, err)
	}

	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, 0, 0, fmt.Errorf("marker for pane %s is incomplete: %q", pane, strings.TrimSpace(string(data)))
	}
	if len(fields) >= 3 {
		prev, err = strconv.Atoi(fields[0])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("bad prev row %q in marker for pane %s", fields[0], pane)
		}
	}
	start, err = strconv.Atoi(fields[len(fields)-2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("bad start row %q in marker for pane %s", fields[len(fields)-2], pane)
	}
	end, err = strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("bad end row %q in marker for pane %s", fields[len(fields)-1], pane)
	}
	return prev, start, end, nil
}

// historySize returns the pane's current scrollback line count, needed to
// translate absolute marker rows into capture-pane screen-relative rows.
func historySize(pane string) (int, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{history_size}").Output()
	if err != nil {
		return 0, fmt.Errorf("tmux display-message history_size for %s: %v", pane, err)
	}
	hs, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("bad history_size %q for pane %s", strings.TrimSpace(string(out)), pane)
	}
	return hs, nil
}

// captureRange runs capture-pane over the given screen-relative rows. s and
// e may be negative (above the visible top, into tmux's history buffer),
// which is how scrolled output is captured.
func captureRange(pane string, s, e int) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-p",
		"-S", strconv.Itoa(s), "-E", strconv.Itoa(e), "-t", pane).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane for %s: %v", pane, err)
	}
	return string(out), nil
}
