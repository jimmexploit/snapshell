package daemon

import (
	"os"
	"path/filepath"
)

// StateDir returns the daemon's state directory
// (~/.local/state/snapshell). All PID/socket/log files live here.
func StateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "snapshell")
}

func SocketPath() string { return filepath.Join(StateDir(), "daemon.sock") }
func PidPath() string    { return filepath.Join(StateDir(), "daemon.pid") }
func LogPath() string    { return filepath.Join(StateDir(), "daemon.log") }

// MarkersDir returns the directory holding per-pane row marker files
// written by the shell hook.
func MarkersDir() string { return filepath.Join(StateDir(), "markers") }

// LastCommandPath returns the path where the shell hook records the most
// recent command's text. Outside tmux (no row markers possible) Alt+2
// falls back to this.
func LastCommandPath() string { return filepath.Join(StateDir(), "lastcommand") }

// CommandLogPath returns the append-only log where the shell hook records
// every completed command across every tmux pane. One line per command,
// newest last:
//
//	<pane_id> <prev_end> <start> <end>
//
// Alt+2 reads the last line, so it always captures the most recently
// completed command regardless of which pane it ran in.
//
// This is the fallback used when no session is active. While a session is
// active the shell hook appends to that session's marker-record log instead
// (SessionLogPath, tracked via ActiveSessionPath) so each session keeps its
// own full command history under ~/.local/share/snapshell/logs/<name>/
// (markers.logs for the records, commands.logs for the readable
// per-command transcript, commands.history for the one-line history).
func CommandLogPath() string { return filepath.Join(StateDir(), "commandlog") }

// ActiveSessionPath is a pointer file the daemon writes when a session
// starts and removes when it stops/shuts down. Its contents are the
// resolved command-log path of the active session, so the shell hook (which
// runs in the user's shells, outside the daemon) knows where to append
// command records without needing to resolve session_root itself. Empty or
// missing means no session is active.
func ActiveSessionPath() string { return filepath.Join(StateDir(), "activesession") }
