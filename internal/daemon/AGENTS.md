# AGENTS.md — internal/daemon

Owns: process lifecycle, the Unix socket IPC server, and in-memory active
session state. This is the long-running process; everything else in this
repo either gets invoked by it (hotkeys firing, popup spawning) or talks to
it (the CLI).

## Responsibilities

1. On startup:
   - Write PID to `~/.local/state/snapshell/daemon.pid`. If that file
     already exists and points at a live process, refuse to start a
     second daemon — print an error and exit non-zero.
   - Create and listen on `~/.local/state/snapshell/daemon.sock`. Remove
     any stale socket file first (check for a live listener before
     assuming stale — don't blindly delete a socket someone else might be
     using).
   - Load config (`internal/config`).
   - Register X11 global hotkey grabs (`internal/hotkeys`) for Alt+1/2/3.
   - Log "daemon started, pid=N" to the daemon log.
2. Hold active session state in memory: session name, session folder path,
   incrementing attachment counter (for `NNN.png` naming — zero-padded to
   3 digits, starting at `001`). This state is *not* persisted to disk
   separately — it's derivable by re-scanning the session's `attachments/`
   folder on `daemon start` if a session was left active (see "crash
   recovery" below), but for the common case (clean `stop`/`start`) it just
   lives in memory.
3. Handle IPC requests (`start`, `stop`, `status` per the protocol defined
   in `cmd/snapshell/AGENTS.md`):
   - `start <name>`: reject if a session is already active. Otherwise
     create `~/snapshell/<name>/` and `~/snapshell/<name>/attachments/` if
     they don't already exist (idempotent — resuming an existing session
     name is allowed and should pick up the existing attachment count by
     counting files already in `attachments/`).
   - `stop`: clear active session state. Hotkeys remain grabbed (daemon
     keeps running) but their handlers become no-ops with a
     `notify-send` "no active snapshell session" message.
   - `status`: report active session name + count of entries appended to
     `blog.md` so far (or "no active session").
4. On each hotkey firing (delivered from `internal/hotkeys`), dispatch to
   the appropriate capture flow (`internal/capture/screenshot` or
   `internal/capture/tmuxcap`, or the raw-note path) using the current
   active session's folder path. If no session is active, notify and
   return immediately — do not queue the capture for later.
5. On `daemon stop` (via IPC) or SIGTERM/SIGINT: release X11 hotkey grabs,
   close the socket listener, remove the PID file, remove the socket file,
   flush the log, exit 0.

## Crash recovery

If the daemon is killed without a clean shutdown (SIGKILL, crash), the PID
file and socket file may be left behind. On next `daemon start`:
- Check if the PID in the stale PID file is actually running. If not,
  treat it as stale, remove both files, and start normally.
- There is no session-state recovery beyond this — if a session was
  active when the daemon died, the user just runs `snapshell start
  <same-name>` again after the daemon restarts; because folder creation is
  idempotent (see above), this correctly resumes rather than duplicates.

## Concurrency

- Hotkey events and IPC requests can arrive concurrently. Guard the active
  session state (name, folder path, attachment counter) with a mutex — do
  not let two captures interleave and corrupt the attachment counter or
  cause a duplicate filename.
- A slow/ignored popup (see `internal/popup/AGENTS.md`) must not block the
  daemon's ability to grab and dispatch the *next* hotkey press. Run each
  capture flow in its own goroutine; only the shared state mutations need
  to be serialized, not the whole flow.

## systemd unit (in `systemd/`)

Provide `systemd/snapshell.service` as a **user** unit (`~/.config/systemd/
user/snapshell.service`), `ExecStart=%h/go/bin/snapshell daemon start`
(adjust path to wherever the binary actually installs), `Restart=on-failure`,
and a comment block above it with the exact `systemctl --user enable
--now snapshell` install instructions. Do not make this a system-wide
(root) unit — it needs the user's X11 session (`DISPLAY`, `XAUTHORITY`)
to grab hotkeys, so it must run as the logged-in user under their
graphical session.
