package shellhook

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"snapshell/internal/daemon"
)

// markersDir resolves the markers directory; a variable so tests can point
// it at a temp dir.
var markersDir = daemon.MarkersDir

// Mark records tmux row positions for a pane into its marker file and
// returns the recorded absolute row.
//
// The marker file holds three absolute rows (history_size + cursor_y):
//
//	<prev_end>   row where the current prompt started (the previous
//	             command's end row); -1 when unknown (first command)
//	<start>      row after the prompt + command line
//	<end>        row where the next prompt will start
//
// prevEnd is passed from the shell hook's previous mark end (see the
// snippet); an empty string means "unknown" and is stored as -1. tmuxcap
// uses prev_end as the start of the capture window so multi-line prompts
// are captured in full, not just the command line.
//
// phase "start" writes [prev, row, -1]. phase "end" preserves prev/start,
// writes [prev, start, row], and the returned row is what the shell hook
// feeds back as the next prev_end.
//
// Expected failures (tmux not running, pane gone, no prior start row)
// return an error; the shell hook snippet ignores them so a broken hook
// never spams the prompt.
func Mark(pane, phase, prevEnd string) (int, error) {
	if pane == "" {
		return 0, fmt.Errorf("mark: empty pane id")
	}

	abs, err := tmuxCursorAbs(pane)
	if err != nil {
		return 0, err
	}

	dir := markersDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	path := filepath.Join(dir, pane+".last")

	switch phase {
	case "start":
		prev := -1
		if n, err := strconv.Atoi(strings.TrimSpace(prevEnd)); err == nil {
			prev = n
		}
		return abs, writeAtomic(path, fmt.Sprintf("%d\n%d\n%d\n", prev, abs, -1))
	case "end":
		prev, start, _, err := readMarkerFile(path)
		if err != nil {
			// No recorded start (first prompt, or an empty command whose
			// hook skipped phase start). Nothing to complete.
			return -1, nil
		}
		return abs, writeAtomic(path, fmt.Sprintf("%d\n%d\n%d\n", prev, start, abs))
	default:
		return 0, fmt.Errorf("mark: phase must be start or end, got %q", phase)
	}
}

// RecordCommand stores the most recent command's text for the plain-shell
// Alt+2 fallback (outside tmux there are no row markers, so the daemon
// captures the command from here instead). The marker file it overwrites
// on every command.
func RecordCommand(text string) error {
	if err := os.MkdirAll(filepath.Dir(daemon.LastCommandPath()), 0o700); err != nil {
		return err
	}
	return writeAtomic(daemon.LastCommandPath(), text+"\n")
}

// tmuxCursorAbs returns the pane's absolute cursor row = history_size +
// cursor_y.
func tmuxCursorAbs(pane string) (int, error) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return 0, fmt.Errorf("tmux not found on PATH")
	}
	out, err := exec.Command(bin, "display-message", "-p", "-t", pane,
		"#{history_size} #{cursor_y}").Output()
	if err != nil {
		return 0, fmt.Errorf("tmux display-message for %s: %v", pane, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, fmt.Errorf("unexpected tmux output %q", strings.TrimSpace(string(out)))
	}
	hs, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, fmt.Errorf("bad history_size %q: %v", fields[0], err)
	}
	cy, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("bad cursor_y %q: %v", fields[1], err)
	}
	return hs + cy, nil
}

// readMarkerFile returns (prev, start, end) from a marker file. It accepts
// the 3-row format as well as legacy 1-row (start) and 2-row (start, end)
// files; missing rows become -1.
func readMarkerFile(path string) (prev, start, end int, err error) {
	prev, start, end = -1, -1, -1
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, err
	}
	rows := strings.Split(strings.TrimSpace(string(data)), "\n")
	vals := []int{}
	for _, line := range rows {
		n, e := strconv.Atoi(strings.TrimSpace(line))
		if e != nil {
			return 0, 0, 0, fmt.Errorf("bad marker row %q", line)
		}
		vals = append(vals, n)
	}
	switch len(vals) {
	case 1:
		start = vals[0]
	case 2:
		start, end = vals[0], vals[1]
	case 3:
		prev, start, end = vals[0], vals[1], vals[2]
	default:
		return 0, 0, 0, fmt.Errorf("marker has %d rows, want 1-3", len(vals))
	}
	return prev, start, end, nil
}

// writeAtomic writes the marker file via a temp file + rename so a reader
// never sees a half-written marker.
func writeAtomic(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".marker-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
