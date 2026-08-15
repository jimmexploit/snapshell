# AGENTS.md — internal/shellhook

Owns: the bash and zsh integration scripts the user sources in their rc
file, which write the row-marker files that `internal/capture/tmuxcap`
reads. This package is mostly **shell scripts, not Go** — the Go side here
is limited to `snapshell shellhook install` / `snapshell shellhook print
bash|zsh`, a helper subcommand that prints the snippet for the user to
paste (or appends it, if they pass a flag) rather than making them hunt
for a file on disk.

## Marker file

- Location: `~/.local/state/snapshell/markers/<pane_id>.last`
- Format: two lines, plain text, no JSON needed:
  ```
  <start_row>
  <end_row>
  ```
- Written atomically (write to a temp file in the same directory, then
  rename over the target) so `tmuxcap` never reads a half-written file.

## bash snippet (conceptual — implement and test against a real bash)

Hook points:
- **Before a command runs**: use `trap 'snapshell_precmd_start' DEBUG`
  (guarded so it only fires once per command, not per simple-command in a
  pipeline — the classic bash DEBUG-trap gotcha; use the standard
  `[ -z "$COMP_LINE" ] && [ "$BASH_COMMAND" != "$PROMPT_COMMAND" ]`-style
  guard pattern).
- **After a command finishes, right before the next prompt draws**: hook
  via `PROMPT_COMMAND`.
- Skip writing a marker if the command that just ran was empty (blank
  Enter) — compare against `$BASH_COMMAND` / history, don't just always
  write.
- Only do any of this when `$TMUX` is set — no-op entirely outside tmux
  (cheap early-return, don't add overhead to every prompt for users not
  in tmux).

## zsh snippet

Hook points: `preexec` (command start) and `precmd` (command end) — these
are zsh's native equivalents and are cleaner than bash's DEBUG trap, use
them directly rather than trying to reuse bash logic.

## Getting the tmux row number

Both hooks call the same helper (a tiny shell function, defined once in
the sourced snippet):
```sh
_snapshell_row() { tmux display-message -p -t "$TMUX_PANE" '#{cursor_y}'; }
```
And write via the Go binary itself rather than duplicating file-writing
logic in shell — i.e. the shell hook should shell out to a fast, tiny
subcommand like `snapshell shellhook mark --pane "$TMUX_PANE" --phase
start|end` that does the actual file write in Go. This keeps the shell
snippet itself minimal (less to get wrong across bash/zsh/tmux version
differences) and keeps the marker file format's implementation in one
place (Go), not duplicated per-shell.

## Install instructions

`snapshell shellhook print bash` / `print zsh` outputs the exact lines to
add, with a comment header like:
```
# --- snapshell shell integration ---
# add this near the end of your .bashrc / .zshrc
```
Document in this file's own comments (and in the root README once one
exists) that after sourcing, the user should start a **new** shell/tmux
pane for the hooks to take effect — don't claim it works retroactively in
already-open panes.

## What NOT to do here

- Don't try to parse or store the command *text* itself in the marker
  file — only row numbers. The command text is already visible in the
  captured pane output (it's the prompt line), no need to duplicate it.
- Don't make the shell hook depend on the daemon being running — it
  should work (write marker files) regardless of daemon state; the daemon
  just reads them later when Alt+2 fires.
