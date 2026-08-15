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

// Mark records the current absolute tmux row for a pane into its marker
// file. phase is "start" or "end".
//
// The row is computed as history_size + cursor_y, NOT cursor_y alone. tmux's
// cursor_y is relative to the visible screen and stops being valid once the
// command's output scrolls the prompt into history; the absolute row keeps
// the marker correct regardless of how much the pane scrolled.
//
// Expected failures (tmux not running, pane gone, no prior start row) return
// an error so callers can decide; the shell hook snippet ignores them so a
// broken hook never spams the prompt.
func Mark(pane, phase string) error {
	if pane == "" {
		return fmt.Errorf("mark: empty pane id")
	}

	abs, err := tmuxCursorAbs(pane)
	if err != nil {
		return err
	}

	dir := markersDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, pane+".last")

	switch phase {
	case "start":
		return writeAtomic(path, fmt.Sprintf("%d\n", abs))
	case "end":
		start, err := readStartRow(path)
		if err != nil {
			// No recorded start (first prompt, or an empty command in bash
			// whose hook skipped phase start). Nothing to complete.
			return nil
		}
		return writeAtomic(path, fmt.Sprintf("%d\n%d\n", start, abs))
	default:
		return fmt.Errorf("mark: phase must be start or end, got %q", phase)
	}
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

// readStartRow returns the first line (start row) of the marker file.
func readStartRow(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	line := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if line == "" {
		return 0, fmt.Errorf("marker has no start row")
	}
	row, err := strconv.Atoi(line)
	if err != nil {
		return 0, fmt.Errorf("bad start row %q", line)
	}
	return row, nil
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
