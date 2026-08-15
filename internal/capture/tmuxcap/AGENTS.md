# AGENTS.md — internal/capture/tmuxcap

Owns: turning "Alt+2 was pressed" into "here is the exact text of the last
command and its output," using the row markers written by
`internal/shellhook` (see that package's AGENTS.md for the marker file
format — read it before implementing this one, the two are tightly
coupled).

## Why row numbers, not PS1 regex

Do not attempt to detect command boundaries by matching against the user's
prompt string. Prompts vary (colors, multi-line prompts, custom segments)
and matching against them is fragile. Instead, the shell hook records
*tmux pane row numbers* at command start/end, and this package just asks
tmux for the text between those rows verbatim — it works regardless of
what the prompt looks like.

## Flow

1. Determine the focused tmux pane: `tmux display-message -p
   '#{pane_id}'` (run against the currently active tmux client — if
   the user has multiple tmux clients attached to different sessions,
   resolving "the" focused one may need `tmux list-clients` filtered by
   which one is actually attached to an X window with input focus; if
   that's not cleanly resolvable, documenting a simpler fallback —
   "whichever tmux session was most recently active" — is acceptable,
   but note the limitation clearly in a code comment).
2. Read the marker file for that pane
   (`~/.local/state/snapshell/markers/<pane_id>.last`) written by the
   shell hook. It contains the start row and end row of the last
   completed command.
3. Run `tmux capture-pane -p -S <start_row> -E <end_row> -t <pane_id>` to
   get the literal text, including the real prompt line and full output.
4. Hand that text off to the shared popup/blog-append tail (same pattern
   as the screenshot flow).

## Edge cases to handle explicitly

- **Not in tmux at all** (no `$TMUX` anywhere, or the focused window
  isn't a tmux client): notify "not in a tmux session" and abort — do not
  crash, do not fall back to capturing raw terminal scrollback via other
  means.
- **No marker file yet** (user pressed Alt+2 before running any command
  since starting their shell, or the shell hook isn't sourced): notify
  "no command captured yet — check that the snapshell shell hook is
  sourced in your shell rc file" — this specific, actionable message
  matters, a generic "capture failed" is not good enough here since the
  most likely cause is a missing `source` line.
- **Output longer than the visible pane** (scrolled content): `tmux
  capture-pane -S`/`-E` with explicit row numbers should still work
  correctly against tmux's history buffer, not just the visible screen —
  verify this against tmux's actual history-limit behavior and increase
  the pane's `history-limit` in the install instructions if needed so
  long output isn't truncated.
- **Empty command** (user just hit Enter on a blank prompt): the shell
  hook is responsible for not updating the marker file in this case (see
  `internal/shellhook/AGENTS.md`), so this package can assume the marker
  file always points at a real command when it exists — but don't crash
  if it turns out to point at an empty range anyway, just capture
  whatever's there.
