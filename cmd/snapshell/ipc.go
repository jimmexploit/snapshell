package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"snapshell/internal/daemon"
)

// sendRequest dials the daemon socket, sends one request, reads the
// response, and closes the connection. It returns an error both for
// transport failures (daemon unreachable) and for daemon responses with
// ok=false (the error message is the response's error text).
func sendRequest(req daemon.Request) (daemon.Response, error) {
	conn, err := net.Dial("unix", daemon.SocketPath())
	if err != nil {
		return daemon.Response{}, connectError(err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return daemon.Response{}, fmt.Errorf("write request: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return daemon.Response{}, fmt.Errorf("read response: %w", err)
	}

	var resp daemon.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return daemon.Response{}, fmt.Errorf("malformed response from daemon: %w", err)
	}

	if !resp.OK {
		return resp, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

// connectError turns a dial failure into an actionable message. If the
// socket file exists but nothing is listening, it removes the stale file
// (and any stale PID file) and tells the user to start the daemon again.
func connectError(dialErr error) error {
	if _, statErr := os.Stat(daemon.SocketPath()); statErr != nil {
		if os.IsNotExist(statErr) {
			return fmt.Errorf("daemon not running — start it with 'snapshell daemon start'")
		}
		return fmt.Errorf("stat socket: %w", statErr)
	}

	// Socket exists but dial failed. Check whether the recorded PID is alive.
	if data, err := os.ReadFile(daemon.PidPath()); err == nil {
		pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
		if perr == nil && pid > 0 && processAlive(pid) {
			return fmt.Errorf("daemon socket %s is broken but pid %d appears alive — try 'snapshell daemon stop', then 'snapshell daemon start'", daemon.SocketPath(), pid)
		}
	}

	_ = os.Remove(daemon.SocketPath())
	_ = os.Remove(daemon.PidPath())
	return fmt.Errorf("daemon not running — stale socket removed, start it again with 'snapshell daemon start'")
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
