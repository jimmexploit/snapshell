# snapshell — Agent Instructions (root)

You are building **snapshell**: a background daemon + CLI that lets a user
document an HTB (HackTheBox) pentest session in real time via global
hotkeys, auto-generating a Markdown blog post per session with embedded
screenshots and terminal-output code blocks.

This file applies to the whole repo. Each subdirectory listed below has its
own `AGENTS.md` with module-specific detail — read the one in whatever
directory you're currently working in *in addition to* this file, not
instead of it.

```
snapshell/
├── AGENTS.md                          ← you are here (global rules)
├── cmd/snapshell/AGENTS.md            ← CLI entrypoint + daemon IPC protocol
├── internal/daemon/AGENTS.md          ← daemon lifecycle, socket server, session state
├── internal/hotkeys/AGENTS.md         ← X11 global key grabbing
├── internal/capture/screenshot/AGENTS.md  ← screenshot tool invocation
├── internal/capture/tmuxcap/AGENTS.md ← tmux pane capture by row range
├── internal/shellhook/AGENTS.md       ← bash/zsh hook scripts + marker files
├── internal/popup/AGENTS.md           ← huh-based floating capture UI
├── internal/blog/AGENTS.md            ← blog.md writer, formatting contract
├── internal/config/AGENTS.md          ← TOML config schema + defaults
└── systemd/                           ← user service unit (see daemon AGENTS.md)
```

## Non-negotiable constraints

- Target platform: **Linux, MATE desktop, X11 only.** Do not add Wayland
  code paths, abstraction layers "for future portability," or build tags
  for other platforms. If a library only has an X11 backend, that's fine —
  that's the target.
- Terminal multiplexer: **tmux**. Shells: bash and zsh, both must work.
- Language: **Go**, single static binary, one `go.mod` at repo root.
- Do not introduce a database. All state is plain files (session folder,
  marker files, PID/socket files, TOML config).
- Build **Phase 1 ("normal mode") only**. There is a second mode planned
  but it is intentionally unspecified — do not guess at it or leave stub
  code for it. Build only what these AGENTS.md files describe.

## Build order

Follow this order. Each step should be independently runnable/testable
before you move to the next — do not build steps 4-9 before steps 1-3 pass
a manual smoke test.

1. `go.mod` + CLI skeleton (`cmd/snapshell`) with subcommands parsed but
   unimplemented (print "not implemented" and exit).
2. Daemon skeleton: Unix socket server, PID file, `daemon start|stop|status`
   round-trips over the socket. See `internal/daemon/AGENTS.md`.
3. X11 global hotkey grabbing for Alt+1/2/3 — log to stdout when each
   fires. Get this solid standalone before wiring in capture logic. See
   `internal/hotkeys/AGENTS.md`.
4. Alt+1 screenshot pipeline end-to-end, **skip captions for now**: hotkey
   → screenshot tool → file lands in `attachments/` → line appended to
   `blog.md`. See `internal/capture/screenshot/AGENTS.md` and
   `internal/blog/AGENTS.md`.
5. Shell hook + marker files (`internal/shellhook/AGENTS.md`) — verify by
   hand (cat the marker file after running a command) before wiring to a
   hotkey.
6. Alt+2 tmux capture pipeline end-to-end, **skip captions for now**. See
   `internal/capture/tmuxcap/AGENTS.md`.
7. Alt+3 raw note (simplest one — no preview, just text → paragraph in
   `blog.md`).
8. Popup UI (`internal/popup/AGENTS.md`) — build it once, wire it into all
   three flows for captions.
9. Config loading + graceful fallbacks (`internal/config/AGENTS.md`).

## Cross-cutting conventions

- **No panics in the daemon.** Every error that can occur at runtime
  (missing binary, X11 grab failure, tmux not running, file write failure)
  must be caught, logged, and — where it affects the user's current
  action — surfaced via `notify-send` so they know the hotkey didn't
  silently do nothing.
- **Logging**: daemon writes to `~/.local/state/snapshell/daemon.log`,
  append mode, plain text with timestamps. Not syslog, not structured
  JSON — keep it simple and human-readable.
- **One active session at a time.** `snapshell start <name>` while a
  session is already active must fail with a clear stderr message, not
  silently switch or queue.
- **Idempotent folder creation.** `snapshell start <name>` on a session
  name that already has a folder should resume appending to its existing
  `blog.md`, not error and not overwrite.
- Prefer small, separately testable functions over one large handler per
  hotkey — the three capture flows share the "open popup → get caption →
  append to blog.md" tail end; factor that shared piece out rather than
  duplicating it three times.
- Every subprocess call (`flameshot`, `tmux`, `xdotool`, `notify-send`,
  `wmctrl`) must check whether the binary exists on `$PATH` before calling
  it, and fail with a specific, actionable error message naming the
  missing binary — not a generic "exec failed."

## Acceptance checklist (repo is done when all of these are true)

- [ ] `snapshell daemon start` runs standalone; a systemd user unit is
      provided (`systemd/`) so it can also be enabled to survive
      logout/login.
- [ ] Alt+1/2/3 work regardless of which window currently has focus.
- [ ] Alt+2 captures exactly one command and its full output (including
      long/scrolled output) when in tmux, and cleanly no-ops with a
      notification when not in tmux.
- [ ] `snapshell start a` then `snapshell start b` (without stopping `a`)
      fails with a clear error and does not touch session `a`.
- [ ] `blog.md`, opened in any plain Markdown viewer, renders images and
      code blocks correctly using only relative paths — the whole session
      folder is portable (can be zipped and opened elsewhere).
- [ ] Ignoring a popup (closing it, or leaving it open) does not hang the
      daemon or block subsequent hotkey presses.
- [ ] All subprocess dependencies degrade gracefully with named error
      messages, never a raw Go panic or stack trace to the user.
