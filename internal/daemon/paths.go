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
