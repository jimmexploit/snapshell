# AGENTS.md — cmd/snapshell (CLI entrypoint)

This is the `main` package. It parses CLI args and talks to the daemon over
a Unix socket — it should contain no business logic itself (no direct
screenshot/tmux/X11 calls). If you find yourself writing capture logic
here, move it into the relevant `internal/` package instead.

## Commands

```
snapshell daemon start     start the background daemon (foreground by default;
                            systemd unit handles backgrounding — see systemd/)
snapshell daemon stop       tell a running daemon to shut down cleanly
snapshell daemon status     print running/not-running + PID

snapshell start <name>      begin a session named <name>
snapshell stop               end the active session
snapshell status             print active session name + item count, or
                            "no active session"

snapshell setup              interactive wizard: check/install
                            dependencies, install the shell hook, create
                            or reset the config file. Also runs
                            automatically on the first `snapshell start`
                            when the config file does not exist AND stdin
                            is a TTY (never in scripts/pipes).
                            When the config already exists it asks whether
                            to reset it to defaults (backing it up to
                            config.toml.bak) and otherwise points the user
                            at its location.

snapshell list-fonts         list every font family on the system so the
                            user can pick a [popup].font value (e.g.
                            "JetBrains Mono 13"). Runs fc-list and prints
                            the sorted, deduplicated family names; the
                            generic Pango families (Sans/Serif/Monospace)
                            are always included.

Hidden plumbing (not shown in help, called by the installed shell hook):
snapshell _hook-mark         record a tmux row marker
snapshell _hook-record       record the last command's text
```

There is no `shellhook` command (the setup wizard owns hook install) and no
`completion` command (`root.CompletionOptions.DisableDefaultCmd = true`).
`list-fonts` is the one command that does not talk to the daemon: it is a
read-only system query (`fc-list`), so it works even when the daemon is
stopped — and it must fail with a named "fc-list not found on PATH" error
when fontconfig is absent, never a raw exec error.

Use a real CLI library (`github.com/spf13/cobra` is fine) so `--help`
output is generated for free. Every subcommand needs a one-line
description visible in `--help`.

## Daemon IPC protocol

- Socket path: `~/.local/state/snapshell/daemon.sock`.
- Keep the protocol trivial: newline-delimited JSON request/response,
  one request per connection, connection closes after response. Do not
  build a persistent connection/streaming protocol — there's no need.
- Request shape: `{"cmd": "start", "args": {"name": "acme-box"}}`
- Response shape: `{"ok": true, "message": "..."}` or
  `{"ok": false, "error": "..."}`
- If the socket file exists but nothing is listening (stale socket after a
  crash), the CLI must detect the connection failure, remove the stale
  socket, and print a clear message telling the user to run
  `snapshell daemon start` again — do not auto-restart the daemon from the
  CLI process.
- `daemon status` / plain `status` should work even against a daemon with
  no active session — print "no active session", not an error.

## Exit codes

- `0` success
- `1` general error (daemon unreachable, bad args, session-already-active,
  etc.) — always accompanied by a stderr message, never a bare exit code.

## What NOT to put here

- No direct calls to `flameshot`, `tmux`, `xdotool`, X11 libraries, or
  zenity. Those live in `internal/capture/*`, `internal/hotkeys`, and
  `internal/popup` respectively. This package's only job is: parse args →
  build an IPC request → send it → print the response.
