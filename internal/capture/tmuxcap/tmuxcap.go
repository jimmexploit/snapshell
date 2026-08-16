package tmuxcap

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ErrNotInTmux reports that tmux isn't available or isn't attached, so the
// command-log row capture is impossible and the caller should fall back to
// the shell hook's plain-shell recorded command.
var ErrNotInTmux = errors.New("not in a tmux session")

// Result describes a completed tmux capture.
type Result struct {
	// Text is the literal pane text spanning the last completed command:
	// the full prompt (all its lines), the command, and — when output is
	// included — its full output (including output that scrolled past the
	// visible screen).
	Text string
}

// Capture returns the exact text of the most recently completed command
// (and, when includeOutput is true, its output), from whichever pane it
// ran in.
//
// commandLog is the append-only log the shell hook writes on every
// completed command, one line per command (newest last):
//
//	<pane_id> <prev_end> <start> <end>
//
// The rows are absolute (history_size + cursor_y): prev_end is where the
// current prompt started (or -1 when unknown), start is the first output
// row, and end is the next prompt row. Empirically the start row is one
// below the last prompt line, because the terminal echoes the accepted
// newline before preexec/DEBUG fires, and the end row is one past the last
// output line. So with a known prev_end the capture begins at the top of
// the (possibly multi-line) prompt, and with includeOutput the capture runs
// to the last output line. When prev_end is unknown (first command in the
// pane) it falls back to start-1 — the last prompt line, which is where the
// command was typed on a single-line prompt.
//
// Alt+2 reads the last log line, so it captures the most recently completed
// command regardless of which pane ran it — no focus resolution and no
// per-pane marker scanning, which are unreliable when the daemon/opencode
// runs in a different pane than the command shell.
func Capture(commandLog string, includeOutput bool) (Result, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return Result{}, fmt.Errorf("%w: tmux not found on PATH", ErrNotInTmux)
	}

	pane, prev, start, end, err := lastCommandRecord(commandLog)
	if err != nil {
		return Result{}, err
	}
	if start < 0 || end == -1 || end < start {
		return Result{}, fmt.Errorf("command log record for pane %s is degenerate (%d..%d) — rerun a command and try again", pane, start, end)
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

// lastCommandRecord returns the pane and absolute rows of the most recently
// completed command from the append-only command log. Invalid/torn lines
// are skipped in favor of the previous valid record. A missing or empty log
// means no command has completed since the hook was installed (or it isn't
// sourced) — that gets a specific, actionable message.
func lastCommandRecord(path string) (pane string, prev, start, end int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, 0, 0, fmt.Errorf("no command captured yet — check that the snapshell shell hook is sourced in your shell rc file")
		}
		return "", 0, 0, 0, fmt.Errorf("read command log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) != 4 {
			continue
		}
		vals := make([]int, 3)
		ok := true
		for j, f := range fields[1:] {
			n, e := strconv.Atoi(f)
			if e != nil {
				ok = false
				break
			}
			vals[j] = n
		}
		if !ok {
			continue
		}
		return fields[0], vals[0], vals[1], vals[2], nil
	}
	return "", 0, 0, 0, fmt.Errorf("no command captured yet — check that the snapshell shell hook is sourced in your shell rc file")
}

// historySize returns the pane's current scrollback line count, needed to
// translate absolute marker rows into capture-pane screen-relative rows.
func historySize(pane string) (int, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{history_size}").Output()
	if err != nil {
		return 0, fmt.Errorf("%w (tmux display-message failed for %s: %v)", ErrNotInTmux, pane, err)
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
