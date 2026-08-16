package shellhook

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"snapshell/internal/daemon"
)

// markersDir resolves the markers directory; a variable so tests can point
// it at a temp dir.
var markersDir = daemon.MarkersDir

// commandLogPath resolves the append-only command log; a variable so tests
// can point it at a temp dir.
var commandLogPath = daemon.CommandLogPath

// activeSessionPath resolves the daemon's active-session pointer; a
// variable so tests can point it at a temp file. See daemon.ActiveSessionPath.
var activeSessionPath = daemon.ActiveSessionPath

// lastCommandPath resolves where the plain-shell fallback stores the most
// recent command's text; a variable so tests can point it at a temp dir.
var lastCommandPath = daemon.LastCommandPath

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
		if err := appendCommandLog(pane, prev, start, abs); err != nil {
			return 0, fmt.Errorf("mark end: record command log: %w", err)
		}
		return abs, writeAtomic(path, fmt.Sprintf("%d\n%d\n%d\n", prev, start, abs))
	default:
		return 0, fmt.Errorf("mark: phase must be start or end, got %q", phase)
	}
}

// appendCommandLog records a completed command so Alt+2 captures the most
// recently completed command regardless of which pane it ran in. While a
// session is active the record goes to that session's command log
// (<session_root>/logs/<name>/commands.log, resolved via the daemon's
// active-session pointer) so each session keeps its own full command
// history; with no active session it falls back to the global log.
// Degenerate records (unstarted, or end still -1) are skipped so an
// interrupted command never pollutes a log.
func appendCommandLog(pane string, prev, start, end int) error {
	if start < 0 || end == -1 || end < start {
		return nil
	}
	path := activeSessionLog()
	if path == "" {
		path = commandLogPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return appendWrite(path, fmt.Sprintf("%s %d %d %d\n", pane, prev, start, end))
}

// activeSessionLog returns the resolved command-log path of the active
// session (written by the daemon on `start`, removed on `stop`), or "" when
// no session is active.
func activeSessionLog() string {
	data, err := os.ReadFile(activeSessionPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// activeSessionDir returns the log directory of the active session (the
// directory holding its commands.log), or "" when no session is active.
func activeSessionDir() string {
	if p := activeSessionLog(); p != "" {
		return filepath.Dir(p)
	}
	return ""
}

// RecordCommand records a completed command's text. It always overwrites
// the plain-shell Alt+2 fallback file (~/.local/state/snapshell/lastcommand)
// and appends a readable line to the active session's history
// (<session_root>/logs/<name>/commands.history), which together document
// every command from every shell.
//
// When the command ran in a plain terminal (source doesn't look like a
// tmux pane id), it is also appended to the session's command log so Alt+2
// picks up the most recently completed command no matter which shell it was
// typed in:
//
//	tty <source> <command text...>                             (no kitty)
//	ktty <source> <kitty-window> <kitty-listen> <command text...>  (in kitty)
//
// The kitty window id + listen socket let tmuxcap read the command's output
// back from the window's scrollback via `kitty @ get-text`. tmux commands
// don't write these records — their row record in commands.log is written
// by the _hook-mark end phase and the output is captured from tmux.
//
// Empty text (a skipped probe or an empty command) is ignored entirely.
// The shell snippets filter out framework probes before calling this, so
// only real user commands are recorded.
func RecordCommand(source, kittyWindow, kittyListen, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(lastCommandPath()), 0o700); err != nil {
		return err
	}
	if err := writeAtomic(lastCommandPath(), text+"\n"); err != nil {
		return err
	}
	dir := activeSessionDir()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if !strings.HasPrefix(source, "%") {
		line := "tty"
		if kittyWindow != "" {
			line = "ktty"
		}
		if kittyWindow != "" {
			line += " " + source + " " + kittyWindow + " " + kittyListen + " " + text
		} else {
			line += " " + source + " " + text
		}
		if err := appendWrite(filepath.Join(dir, "commands.log"), line+"\n"); err != nil {
			return err
		}
	}
	return appendWrite(filepath.Join(dir, "commands.history"), formatHistoryLine(source, text))
}

// formatHistoryLine renders one command-history record: an ISO-ish
// timestamp, the source (tmux pane or tty), and the command text with
// newlines collapsed so every record is exactly one line.
func formatHistoryLine(source, text string) string {
	if source == "" {
		source = "?"
	}
	flat := strings.NewReplacer("\r\n", " ", "\n", " ").Replace(text)
	return fmt.Sprintf("%s  %s  %s\n", time.Now().Format("2006-01-02 15:04:05"), source, flat)
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

// appendWrite appends content to path (creating it if needed). One single
// write so a concurrent reader never observes a torn record.
func appendWrite(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}
