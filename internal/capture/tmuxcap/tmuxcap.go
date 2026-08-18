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
	// Text is the literal pane text spanning the captured command(s): the
	// full prompt (all its lines), the command, and — when output is
	// included — its full output (including output that scrolled past the
	// visible screen). For a multi-command capture the individual commands
	// are separated by a blank line.
	Text string
	// Count is how many completed commands the capture actually covered.
	// It can be less than the number requested when the command log holds
	// fewer records (the popup shows the real count, not the request).
	Count int
}

// Capture returns the exact text of the most recently completed command
// (and, when includeOutput is true, its output), from wherever it ran — a
// tmux pane or a plain terminal. It is CaptureN with a count of one.
func Capture(commandLog string, includeOutput bool) (Result, error) {
	return CaptureN(commandLog, includeOutput, 1)
}

// CaptureN is Capture but for the last n completed commands at once,
// concatenated with a blank line between them. n < 1 is treated as 1. It
// backs the Alt+2 command-count prefix: Alt+2 followed by a digit captures
// that many commands together. Fewer records than requested is fine — as
// many as exist are captured and reported in Result.Count.
func CaptureN(commandLog string, includeOutput bool, n int) (Result, error) {
	if n < 1 {
		n = 1
	}
	recs, err := lastCommandRecords(commandLog, n)
	if err != nil {
		return Result{}, err
	}

	// n == 1 keeps the historical single-record behavior: the text is
	// returned verbatim, exactly as a single tmux capture-pane produced it.
	if len(recs) == 1 {
		text, err := captureRecord(recs[0], includeOutput)
		if err != nil {
			return Result{}, err
		}
		return Result{Text: text, Count: 1}, nil
	}

	var parts []string
	for _, rec := range recs {
		text, err := captureRecord(rec, includeOutput)
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(text) != "" {
			parts = append(parts, strings.TrimRight(text, "\n"))
		}
	}
	return Result{Text: strings.Join(parts, "\n\n") + "\n", Count: len(recs)}, nil
}

// captureRecord captures a single command record, whatever its kind.
func captureRecord(rec commandRecord, includeOutput bool) (string, error) {
	switch rec.kind {
	case recordPlain:
		return rec.text, nil
	case recordKitty:
		return capturePlain(rec, includeOutput)
	case recordTmux:
		return captureTmuxRange(rec, includeOutput)
	default:
		return "", fmt.Errorf("unknown command log record kind")
	}
}

// CaptureRows captures the text of a single tmux command given its marker
// rows (pane, prev, start, end), exactly as CaptureN would from a record.
// It is the building block for the shell hook's live per-command
// transcript (commands.logs), which records each command's output at
// completion time rather than waiting for an Alt+2 press.
func CaptureRows(pane string, prev, start, end int, includeOutput bool) (string, error) {
	return captureTmuxRange(commandRecord{kind: recordTmux, pane: pane, prev: prev, start: start, end: end}, includeOutput)
}

// KittyOutput returns the last command's output from a kitty window with
// shell integration enabled — the same mechanism CaptureN uses for ktty
// records — so the shell hook can append a command's output to its live
// transcript at completion time.
func KittyOutput(window, listen string) (string, error) {
	return kittyLastCommandOutput(window, listen)
}

// captureTmuxRange runs tmux capture-pane over one record's row range.
func captureTmuxRange(rec commandRecord, includeOutput bool) (string, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return "", fmt.Errorf("%w: tmux not found on PATH", ErrNotInTmux)
	}
	if rec.start < 0 || rec.end == -1 || rec.end < rec.start {
		return "", fmt.Errorf("command log record for pane %s is degenerate (%d..%d) — rerun a command and try again", rec.pane, rec.start, rec.end)
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
		return "", err
	}

	return captureRange(rec.pane, from-hs, to-hs)
}

// capturePlain returns the recorded command text, reading the command's
// output back from kitty when the record carries kitty window metadata and
// the output is wanted.
func capturePlain(rec commandRecord, includeOutput bool) (string, error) {
	// Without kitty window metadata (a tty record, or a ktty record whose
	// shell wasn't inside kitty) the text alone is all we have.
	if rec.kind == recordPlain || rec.kittyWindow == "" {
		return rec.text, nil
	}
	if !includeOutput {
		return rec.text, nil
	}

	if _, err := exec.LookPath("kitty"); err != nil {
		return "", fmt.Errorf("could not capture output from kitty: kitty not found on PATH")
	}
	out, err := kittyLastCommandOutput(rec.kittyWindow, rec.kittyListen)
	if err != nil {
		return "", err
	}
	text := rec.text
	if out := strings.TrimRight(out, "\n"); out != "" {
		// One blank line between command and output; single trailing newline,
		// matching the tmux capture.
		text += "\n" + out + "\n"
	}
	return text, nil
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

// lastCommandRecords returns the most recently completed commands from the
// append-only command log — up to n, oldest first — so a multi-command
// capture can span them. commandLog is the file the shell hook appends to
// on every completed command, one line per command (newest last):
//
//	%<pane_id> <prev_end> <start> <end>        tmux: row-based, captured via tmux
//	tty <source> <command text...>             plain terminal: text only, no output
//	ktty <source> <kittywid> <listen> <text...> kitty plain terminal: output via kitty
//
// Invalid/torn lines are skipped in favor of the previous valid record; a
// requested n may come back with fewer records when the log is short. A
// missing or empty log means no command has completed since the hook was
// installed (or it isn't sourced) — that gets a specific, actionable
// message.
func lastCommandRecords(path string, n int) ([]commandRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no command captured yet — check that the snapshell shell hook is sourced in your shell rc file")
		}
		return nil, fmt.Errorf("read command log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var newest []commandRecord
	for i := len(lines) - 1; i >= 0 && len(newest) < n; i-- {
		if rec, ok := parseRecord(lines[i]); ok {
			newest = append(newest, rec)
		}
	}
	if len(newest) == 0 {
		return nil, fmt.Errorf("no command captured yet — check that the snapshell shell hook is sourced in your shell rc file")
	}
	// Reverse into chronological (oldest-first) order: the capture reads
	// left-to-right like the terminal, so command text appears in the order
	// it was actually run.
	recs := make([]commandRecord, len(newest))
	for i, rec := range newest {
		recs[len(newest)-1-i] = rec
	}
	return recs, nil
}

// parseRecord parses one command-log line. ok is false for blank or
// malformed lines (torn writes), which the caller skips.
func parseRecord(line string) (rec commandRecord, ok bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return commandRecord{}, false
	}
	if strings.HasPrefix(fields[0], "%") {
		// tmux record: exactly four whitespace-separated fields, three of
		// them integers.
		if len(fields) != 4 {
			return commandRecord{}, false
		}
		vals := make([]int, 3)
		for j, f := range fields[1:] {
			n, e := strconv.Atoi(f)
			if e != nil {
				return commandRecord{}, false
			}
			vals[j] = n
		}
		return commandRecord{kind: recordTmux, pane: fields[0], prev: vals[0], start: vals[1], end: vals[2]}, true
	}
	if fields[0] == "tty" {
		// Plain-terminal record: "tty <source> <command text...>".
		if len(fields) < 3 {
			return commandRecord{}, false
		}
		return commandRecord{kind: recordPlain, source: fields[1], text: strings.Join(fields[2:], " ")}, true
	}
	if fields[0] == "ktty" {
		// Kitty plain-terminal record:
		// "ktty <source> <kittywid> <listen> <command text...>".
		if len(fields) < 5 {
			return commandRecord{}, false
		}
		return commandRecord{kind: recordKitty, source: fields[1],
			kittyWindow: fields[2], kittyListen: fields[3],
			text: strings.Join(fields[4:], " ")}, true
	}
	return commandRecord{}, false
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
