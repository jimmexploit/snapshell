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
completed command, newest last:

```
<pane_id> <prev_end> <start> <end>
```

where the rows are absolute (`history_size + cursor_y`). Each session has
its own log at `<session_root>/logs/<name>/commands.log` (the daemon points
the hook at it via the `~/.local/state/snapshell/activesession` pointer),
so every session keeps its own full command history. Alt+2 reads the **last
line** of the active session's log and captures that pane's range. This is
deterministic: the last-written record is always the most recently
completed command, no focus resolution, no mtime scanning.

## Flow

1. Read the last line of the active session's command log
   (`<session_root>/logs/<name>/commands.log`, passed in by the daemon).
   Skip torn/invalid lines and fall back to the previous valid record.
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