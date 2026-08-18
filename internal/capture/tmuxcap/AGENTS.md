# AGENTS.md — internal/capture/tmuxcap

Owns: turning "Alt+2 was pressed" into "here is the exact text of the last
command and its output," using the row records written by
`internal/shellhook` (see that package's AGENTS.md for the record format —
read it before implementing this one, the two are tightly coupled).

## Why row numbers, not PS1 regex

Do not attempt to detect command boundaries by matching against the user's
prompt string. Prompts vary (colors, multi-line prompts, custom segments)
and matching against them is fragile. Instead, the shell hook records
*tmux pane row numbers* at command start/end, and this package just asks
tmux for the text between those rows verbatim — it works regardless of
what the prompt looks like.

## Which command to capture

Alt+2 must capture "the last command that actually ran, wherever it was
typed" — NOT "the focused tmux pane." The daemon typically runs in a
different pane than the command shell (e.g. inside an opencode TUI), so
tmux focus resolution is unreliable, and per-pane marker files are
ambiguous when multiple panes in the same session run commands close
together.

The shell hook therefore writes an **append-only command log**, one line per
completed command, newest last, with three record types:

```
%<pane_id> <prev_end> <start> <end>     tmux pane: row-based, capturable via tmux
tty <source> <command text...>          plain terminal: text only, no output
ktty <source> <kittywid> <listen> <text> kitty terminal: output via kitty
```

For a tmux record the rows are absolute (`history_size + cursor_y`); for a
plain-terminal record there is no tmux scrollback to capture from, so the
command text itself is the capture — unless it ran in a kitty window with
shell integration enabled (`ktty` record), in which case its output is read
back from that window with `kitty @ --to <listen> get-text --match
id:<kittywid> --extent last_cmd_output`. Each session has its own log at
`<session_root>/logs/<name>/commands.log` (the daemon points the hook at it
via the `~/.local/state/snapshell/activesession` pointer), so every session
keeps its own full command history. Alt+2 reads the **last line** of the
active session's log and dispatches on the first field: a `%` pane id → run
`tmux capture-pane` over that row range; a `tty` record → return the command
text verbatim; a `ktty` record → return the command text plus its output from
kitty. This is deterministic: the last-written record is always the most
recently completed command, wherever it was typed — no focus resolution, no
mtime scanning.

## Command count (Alt+2 + digit)

Pressing a digit (1-9) right after Alt+2 captures that many recent commands
at once, concatenated with a blank line between them. `CaptureN(commandLog,
includeOutput, n)` reads the **last n valid records** (in chronological
order) and captures each one independently — consecutive same-pane tmux
records are adjacent in the log (record A's `to` == the next record's
`from`-1), so per-record capture equals a single widened `capture-pane`
range; there is no gap or duplication to merge. `Result.Count` reports how
many records were actually captured, which can be fewer than `n` when the
log is short — the popup shows that real number, not the request. `n<1`
clamps to 1, and `Capture` is exactly `CaptureN(..., 1)` (same verbatim
text as always).

## Flow

1. Read the last line of the active session's command log
   (`<session_root>/logs/<name>/commands.log`, passed in by the daemon).
   Skip torn/invalid lines and fall back to the previous valid record.
   - Plain record (`tty ...`): return the command text directly (no tmux
     involved — works even when the tmux binary is absent).
   - Kitty record (`ktty ...`): return the command text, plus its output
     when the record has a kitty window id and `include_output` is on: run
     `kitty @ --to <listen> get-text --match id:<kittywid> --extent
     last_cmd_output` and append the result. A missing `kitty` binary or a
     dead socket/window is a named error ("kitty not found on PATH" /
     "...is that kitty window still open?"), not a panic or silent nothing.
     Without shell-integration marks in that window the extent comes back
     empty and the command text alone is returned.
   - tmux record (`%N ...`): continue.
2. Run `tmux capture-pane -p -S <start_row> -E <end_row> -t <pane_id>` to
   get the literal text, including the real prompt line and full output.
   The absolute rows are translated to screen-relative rows by subtracting
   the pane's current `history_size` (`tmux display-message -p -t <pane>
   '#{history_size}'`).
3. Hand that text off to the shared popup/blog-append tail (same pattern
   as the screenshot flow).

## Edge cases to handle explicitly

- **Not in tmux at all** (no `$TMUX` anywhere, or no tmux server):
  notify "not in a tmux session" and abort — do not crash, do not fall
  back to capturing raw terminal scrollback via other means.
- **No command log / empty log** (user pressed Alt+2 before running any
  command since starting their shell, or the shell hook isn't sourced):
  notify "no command captured yet — check that the snapshell shell hook is
  sourced in your shell rc file" — this specific, actionable message
  matters, a generic "capture failed" is not good enough here since the
  most likely cause is a missing `source` line.
- **Interrupted command** (marker end never written): the shell hook must
  not append a record whose end row is still `-1` (see
  `internal/shellhook/AGENTS.md`), so an interrupted command never becomes
  the "last" capture. If one is seen anyway, reject it as degenerate.
- **Output longer than the visible pane** (scrolled content): `tmux
  capture-pane -S`/`-E` with explicit row numbers should still work
  correctly against tmux's history buffer, not just the visible screen —
  verify this against tmux's actual history-limit behavior and increase
  the pane's `history-limit` in the install instructions if needed so
  long output isn't truncated.
- **Empty command** (user just hit Enter on a blank prompt): the shell
  hook is responsible for not recording it (see
  `internal/shellhook/AGENTS.md`), so this package can assume the log's
  last record always points at a real command when it exists — but don't
  crash if it turns out to point at an empty range anyway, just capture
  whatever's there.