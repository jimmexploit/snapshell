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
// (and, when includeOutput is true, its output), from wherever it ran —
// a tmux pane or a plain terminal.
//
// commandLog is the append-only log the shell hook writes on every
// completed command, one line per command (newest last):
//
//	%<pane_id> <prev_end> <start> <end>        tmux: row-based, captured via tmux
//	tty <source> <command text...>             plain terminal: text only, no output
//	ktty <source> <kittywid> <listen> <text...> kitty plain terminal: output via kitty
//
// For a tmux record the rows are absolute (history_size + cursor_y):
// prev_end is where the current prompt started (or -1 when unknown), start
// is the first output row, and end is the next prompt row. Empirically the
// start row is one below the last prompt line, because the terminal echoes
// the accepted newline before preexec/DEBUG fires, and the end row is one
// past the last output line. So with a known prev_end the capture begins at
// the top of the (possibly multi-line) prompt, and with includeOutput the
// capture runs to the last output line. When prev_end is unknown (first
// command in the pane) it falls back to start-1 — the last prompt line,
// which is where the command was typed on a single-line prompt.
//
// For a plain-terminal record there is no tmux scrollback to capture from.
// A tty record returns just the command text. A ktty record additionally
// holds the kitty window id + listen socket the command ran in; when the
// window's shell had kitty shell integration enabled (prompt marks), its
// output is read back with `kitty @ get-text --extent last_cmd_output` and
// returned alongside the command text. Without the marks (shell started
// before the hook was installed) that extent is empty and the text alone is
// returned.
//
// Alt+2 reads the last log line, so it captures the most recently completed
// command regardless of which shell or pane it ran in — no focus resolution
// and no per-pane marker scanning, which are unreliable when the
// daemon/opencode runs in a different pane than the command shell.
func Capture(commandLog string, includeOutput bool) (Result, error) {
	rec, err := lastCommandRecord(commandLog)
	if err != nil {
		return Result{}, err
	}
	if rec.kind == recordPlain || rec.kind == recordKitty {
		return capturePlain(rec, includeOutput)
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		return Result{}, fmt.Errorf("%w: tmux not found on PATH", ErrNotInTmux)
	}
	if rec.start < 0 || rec.end == -1 || rec.end < rec.start {
		return Result{}, fmt.Errorf("command log record for pane %s is degenerate (%d..%d) — rerun a command and try again", rec.pane, rec.start, rec.end)
	}

	from := rec.start - 1
	if rec.prev >= 0 {
		from = rec.prev
	}
	to := rec.end - 1
	if !includeOutput {
		to = rec.start - 1
	}

	hs, err := historySize(rec.pane)
	if err != nil {
		return Result{}, err
	}

	text, err := captureRange(rec.pane, from-hs, to-hs)
	if err != nil {
		return Result{}, err
	}
	return Result{Text: text}, nil
}

// capturePlain returns the recorded command text, reading the command's
// output back from kitty when the record carries kitty window metadata and
// the output is wanted.
func capturePlain(rec commandRecord, includeOutput bool) (Result, error) {
	// Without kitty window metadata (a tty record, or a ktty record whose
	// shell wasn't inside kitty) the text alone is all we have.
	if rec.kind == recordPlain || rec.kittyWindow == "" {
		return Result{Text: rec.text}, nil
	}
	if !includeOutput {
		return Result{Text: rec.text}, nil
	}

	if _, err := exec.LookPath("kitty"); err != nil {
		return Result{}, fmt.Errorf("could not capture output from kitty: kitty not found on PATH")
	}
	out, err := kittyLastCommandOutput(rec.kittyWindow, rec.kittyListen)
	if err != nil {
		return Result{}, err
	}
	text := rec.text
	if out := strings.TrimRight(out, "\n"); out != "" {
		// One blank line between command and output; single trailing newline,
		// matching the tmux capture.
		text += "\n" + out + "\n"
	}
	return Result{Text: text}, nil
}

// kittyLastCommandOutput runs `kitty @ get-text --extent last_cmd_output`
// for the given window: the output region of the last command that ran
// there, per kitty's shell-integration prompt marks. listen is the
// `--to <socket>` value from the shell's KITTY_LISTEN_ON; empty means let
// kitty pick its default socket. Note the flag order: `--to` belongs to the
// `@` kitten, so it must come after `@` (`kitty @ --to <sock> get-text`).
func kittyLastCommandOutput(window, listen string) (string, error) {
	args := []string{"@"}
	if listen != "" {
		args = append(args, "--to", listen)
	}
	args = append(args, "get-text", "--match", "id:"+window, "--extent", "last_cmd_output")
	out, err := exec.Command("kitty", args...).Output()
	if err != nil {
		return "", fmt.Errorf("could not capture output from kitty: %v (is that kitty window still open?)", err)
	}
	return string(out), nil
}

// recordKind distinguishes the command-log record types.
type recordKind int

const (
	recordTmux  recordKind = iota // "%N <prev> <start> <end>" — capturable via tmux
	recordPlain                   // "tty <source> <text...>" — plain terminal, text only
	recordKitty                   // "ktty <source> <kittywid> <listen> <text...>" — kitty terminal, output via kitty
)

// commandRecord is one parsed line of the command log.
type commandRecord struct {
	kind             recordKind
	pane             string // tmux pane id (%N) for tmux records
	source           string // tty device for plain/kitty records
	prev, start, end int
	text             string // plain/kitty-record command text
	kittyWindow      string // kitty window id for ktty records
	kittyListen      string // kitty listen socket for ktty records
}

// lastCommandRecord returns the most recently completed command from the
// append-only command log. Invalid/torn lines are skipped in favor of the
// previous valid record. A missing or empty log means no command has
// completed since the hook was installed (or it isn't sourced) — that gets
// a specific, actionable message.
func lastCommandRecord(path string) (commandRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return commandRecord{}, fmt.Errorf("no command captured yet — check that the snapshell shell hook is sourced in your shell rc file")
		}
		return commandRecord{}, fmt.Errorf("read command log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(fields[0], "%") {
			// tmux record: exactly four whitespace-separated fields, three
			// of them integers.
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
			return commandRecord{kind: recordTmux, pane: fields[0], prev: vals[0], start: vals[1], end: vals[2]}, nil
		}
		if fields[0] == "tty" {
			// Plain-terminal record: "tty <source> <command text...>".
			if len(fields) < 3 {
				continue
			}
			return commandRecord{kind: recordPlain, source: fields[1], text: strings.Join(fields[2:], " ")}, nil
		}
		if fields[0] == "ktty" {
			// Kitty plain-terminal record:
			// "ktty <source> <kittywid> <listen> <command text...>".
			if len(fields) < 5 {
				continue
			}
			return commandRecord{kind: recordKitty, source: fields[1],
				kittyWindow: fields[2], kittyListen: fields[3],
				text: strings.Join(fields[4:], " ")}, nil
		}
	}
	return commandRecord{}, fmt.Errorf("no command captured yet — check that the snapshell shell hook is sourced in your shell rc file")
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
