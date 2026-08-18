# AGENTS.md — internal/shellhook

Owns: the bash and zsh integration scripts the user sources in their rc
file, which record command rows that `internal/capture/tmuxcap` reads. This
package is mostly **shell scripts, not Go** — the Go side here is limited to
the `Mark` / `RecordCommand` functions the hidden CLI helpers `snapshell
_hook-mark` and `snapshell _hook-record` wrap (installed by the `snapshell
setup` wizard).

## Marker file (working memory)

The hook must remember the *in-progress* command's rows between the start
and end phases, so it keeps a per-pane marker file as scratch state:

- Location: `~/.local/state/snapshell/markers/<pane_id>.last`
- Format: three lines, plain text:
  ```
  <prev_end>
  <start_row>
  <end_row>
  ```
  `prev_end` is the previous command's end row (`-1` when unknown, i.e. the
  first command in the pane), `start_row` is the first output row, and
  `end_row` is `-1` while the command is still running.
- Written atomically (write to a temp file in the same directory, then
  rename over the target) so a concurrent reader never sees a half-written
  file.

## Command log (what Alt+2 actually reads)

On every **completed** command, the hook appends a single line to the
active session's marker-record log `<session_root>/logs/<name>/markers.logs`
(the daemon points the hook at it via the
`~/.local/state/snapshell/activesession` pointer file, written on `start`,
removed on `stop`/shutdown). With no session active nothing is logged.
(The file used to be named `commands.log`; the rename to `markers.logs`
separates it from the two human-readable logs below.)

Three record types, newest last:

```
%<pane_id> <prev_end> <start> <end>     tmux: rows → tmux capture
tty <source> <command text...>          plain terminal: text only
ktty <source> <kittywid> <listen> <text> kitty terminal: output via kitty
```

- **tmux commands** (inside `$TMUX`): `_hook-mark`'s end phase appends the
  row record `<pane_id> <prev_end> <start_row> <end_row>`, e.g.
  `%7 -1 320 328`, from which `tmuxcap` captures the full prompt + output.
  Appended only when the command actually completed — never write a record
  with `end_row == -1`, so an interrupted command (Ctrl-C mid-run, or a
  DEBUG/PROMPT_COMMAND race) never becomes the "last" command Alt+2
  captures.
- **plain-terminal commands** (no `$TMUX`): `_hook-record` appends
  `tty <source> <command text...>`, e.g. `tty /dev/pts/5 whoami`. There is
  no tmux scrollback to capture, so the text is the capture.
- **kitty plain-terminal commands**: when the shell ran in a kitty window
  (`$KITTY_WINDOW_ID` set), the record carries the window id and listen
  socket so `tmuxcap` can read the command's output back:
  `ktty /dev/pts/5 3 unix:/tmp/kitty-2200 whoami`. For that to work the
  window's shell must have kitty shell integration enabled — the snippets
  do this themselves: when a non-tmux shell has `$KITTY_WINDOW_ID` but no
  `$KITTY_SHELL_INTEGRATION`, they set the variable and `source`
  `/usr/lib/kitty/shell-integration/{bash,zsh}/kitty.{bash,zsh}`, which
  installs the OSC 133 prompt/command marks `get-text --extent
  last_cmd_output` relies on. Both branches capture `$KITTY_WINDOW_ID` and
  `$KITTY_LISTEN_ON` at command start and pass them via
  `--kitty-window`/`--kitty-listen`.

Use a single `write(2)` per record (O_APPEND) so a concurrent reader never
sees a torn line.

This is the source of truth for Alt+2: `tmuxcap` reads the last line of the
active session's log, so it always captures the most recently completed
command regardless of which shell or pane it ran in.

## bash snippet (conceptual — implement and test against a real bash)

Hook points:
- **Before a command runs**: use `trap 'snapshell_precmd_start' DEBUG`
  (guarded so it only fires once per command, not per simple-command in a
  pipeline — the classic bash DEBUG-trap gotcha; use the standard
  `[ -z "$COMP_LINE" ] && [ "$BASH_COMMAND" != "$PROMPT_COMMAND" ]`-style
  guard pattern).
- **After a command finishes, right before the next prompt draws**: hook
  via `PROMPT_COMMAND`.
- Skip recording if the command that just ran was empty (blank Enter) —
  compare against `$BASH_COMMAND` / history, don't just always write.
- Outside tmux the hook records the command *text* via `_hook-record` (see
  below). When the plain shell runs in kitty it additionally enables kitty
  shell integration (`export KITTY_SHELL_INTEGRATION=enabled; source
  /usr/lib/kitty/shell-integration/bash/kitty.bash`, guarded by
  `$KITTY_WINDOW_ID` set, `$KITTY_SHELL_INTEGRATION` unset, and the file
  present) so Alt+2 can capture the command's output from the window; the
  snippet captures `$KITTY_WINDOW_ID`/`$KITTY_LISTEN_ON` per command and
  passes them to `_hook-record`. Never enable this inside `$TMUX` — kitty's
  prompt marks would leak into the tmux capture.
- The DEBUG trap must never treat its own probe commands or other
  frameworks' probes as user commands (skip `:`, `builtin ...`, `_snapshell_*`,
  `__bp_*`, and any function name).

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
subcommand like `snapshell _hook-mark --pane "$TMUX_PANE" --phase
start|end` that does the actual file write in Go. This keeps the shell
snippet itself minimal (less to get wrong across bash/zsh/tmux version
differences) and keeps the marker/log formats' implementation in one place
(Go), not duplicated per-shell.

## Plain-shell fallback and session history (no tmux)

`RecordCommand` (wrapped as `snapshell _hook-record`) overwrites
`~/.local/state/snapshell/lastcommand` with the most recent command's text.
The daemon falls back to it only when tmux is genuinely unavailable and the
last record needs tmux. It must not record framework probes (`builtin ...`,
DEBUG-trap fragments from other prompt frameworks) — those are not user
commands.

`_hook-record` also appends every command — from tmux panes AND plain
terminals — to the active session's history at
`<session_root>/logs/<name>/commands.history`, one line per command:

```
2026-08-16 04:05:00  %1        ls -la /tmp
2026-08-16 04:10:12  /dev/pts/3  uname -r
```

The snippet passes `--source` (`$TMUX_PANE` in tmux, `$(tty)` outside) so
each line says where the command ran. Newlines in the command text are
collapsed to spaces so every record is exactly one line. This gives each
session a complete command history from every shell, not just tmux — the
daemon's `markers.logs` row records remain the Alt+2 capture source, and
`commands.history` is the human-readable one-line history. With no active
session, `_hook-record` only writes `lastcommand` and creates no history
file.

## Live transcript (`commands.logs`)

On every completed command the hook also appends a readable record of the
command **and its full output** to
`<session_root>/logs/<name>/commands.logs` at completion time, so the
session documents itself in real time rather than waiting for an Alt+2
press. Best-effort by design — this runs in the prompt-critical path, so a
capture failure (no tmux, kitty window gone) drops only that command's
output, never breaks the shell.

```
=== 2026-08-18 12:00:00  %1 ===
┌─[root@box]# nmap -sV 10.10.11.5
22/tcp open  ssh
80/tcp open  http
```
```
=== 2026-08-18 12:05:00  /dev/pts/3 ===
whoami
root
```

The header line is the same timestamp+source shape as `commands.history`; the
captured block below it is the literal pane text (tmux, via
`tmuxcap.CaptureRows`), the command text plus its output read back from the
kitty window (plain kitty, via `tmuxcap.KittyOutput`), or the command text
alone (plain terminal). tmux capture happens in the `_hook-mark end` phase,
which already has the row range; plain/kitty capture happens in
`_hook-record`.

## Install instructions

The `snapshell setup` wizard appends the snippet (via the CLI's internal
`installHook`, which refuses to double-append). The snippet carries a
comment header like:
```
# --- snapshell shell integration ---
# add this near the end of your .bashrc / .zshrc
```
Document in this file's own comments (and in the root README once one
exists) that after sourcing, the user should start a **new** shell/tmux
pane for the hooks to take effect — don't claim it works retroactively in
already-open panes.

## What NOT to do here

- Don't try to parse or store the command *text* itself in the marker file
  or command log — only row numbers. The command text is already visible in
  the captured pane output (it's the prompt line), no need to duplicate it.
- Don't make the shell hook depend on the daemon being running — it should
  work (write markers / the command log) regardless of daemon state; the
  daemon just reads them later when Alt+2 fires.