package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"snapshell/internal/daemon"
)

// ensureDaemonStarted makes sure a daemon is reachable before a command
// that needs one, spawning it in the background if it isn't. This removes
// the two-step "daemon start, then start <name>" dance: `snapshell start
// box` just works.
func ensureDaemonStarted() error {
	if _, err := sendRequest(daemon.Request{Cmd: daemon.CmdStatus}); err == nil {
		return nil // already up
	}
	if err := spawnDaemon(); err != nil {
		return err
	}
	return waitForDaemon(5 * time.Second)
}

// spawnDaemon launches `snapshell daemon start` as a detached background
// process (own session, /dev/null stdio). The daemon writes its own PID
// file and log, so nothing needs to be piped back to us.
func spawnDaemon() error {
	self, err := os.Executable()
	if err != nil || self == "" {
		self = "snapshell"
	}
	cmd := exec.Command(self, "daemon", "start")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	return nil
}

// waitForDaemon polls the socket until the daemon answers or the timeout
// expires.
func waitForDaemon(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := sendRequest(daemon.Request{Cmd: daemon.CmdStatus}); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start within %s — check %s", timeout, daemon.LogPath())
}
