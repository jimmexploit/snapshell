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

snapshell internal-popup --mode image|code|note --file <path>
                            NOT user-facing — invoked by the daemon itself
                            inside a spawned floating terminal. See
                            internal/popup/AGENTS.md for its contract.
```

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

- No direct calls to `flameshot`, `tmux`, `xdotool`, X11 libraries, or huh
  forms. Those live in `internal/capture/*`, `internal/hotkeys`, and
  `internal/popup` respectively. This package's only job is: parse args →
  build an IPC request → send it → print the response.
