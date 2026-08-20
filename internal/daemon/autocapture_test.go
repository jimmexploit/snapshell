package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"snapshell/internal/inventory"
)

// startAutoTestDaemon starts a daemon whose config has [auto].enabled set,
// so the autocapture handler's decision paths can be exercised end-to-end
// over the socket.
func startAutoTestDaemon(t *testing.T, stateDir, sessionRoot string, auto bool) (chan error, string) {
	t.Helper()
	done := make(chan error, 1)
	cfg := testConfig(t, sessionRoot)
	cfg.Auto.Enabled = auto
	go func() {
		done <- Run(Options{Config: cfg, StateDir: stateDir, DisableHotkeys: true})
	}()
	sockPath := filepath.Join(stateDir, "daemon.sock")
	waitForSocket(t, sockPath)
	return done, sockPath
}

func TestAutoCaptureQueuesSuccessfulCommand(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	precreateInventorySession(t, sessionRoot, "acme", nil)
	done, sockPath := startAutoTestDaemon(t, stateDir, sessionRoot, true)

	resp := send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme", "mode": "inventory"}})
	if !resp.OK {
		t.Fatalf("start inventory: %+v", resp)
	}

	// A successful plain-terminal command is queued as a code card.
	resp = send(t, sockPath, Request{Cmd: CmdAutoCapture, Args: map[string]string{
		"exit": "0", "text": "nmap -sV 10.10.10.1", "source": "/dev/pts/5",
	}})
	if !resp.OK {
		t.Fatalf("autocapture should succeed, got %+v", resp)
	}
	list := listCards(t, sockPath)
	if len(list) != 1 || list[0].Kind != inventory.KindCode || list[0].Text != "nmap -sV 10.10.10.1" {
		t.Fatalf("autocaptured cards = %+v, want the nmap command", list)
	}

	// A second successful command lands too; excluded and non-zero-exit
	// commands do not.
	resp = send(t, sockPath, Request{Cmd: CmdAutoCapture, Args: map[string]string{
		"exit": "0", "text": "curl -s https://example.com", "source": "/dev/pts/5",
	}})
	if !resp.OK {
		t.Fatalf("second autocapture failed: %+v", resp)
	}
	resp = send(t, sockPath, Request{Cmd: CmdAutoCapture, Args: map[string]string{
		"exit": "0", "text": "ls -la", "source": "/dev/pts/5",
	}})
	if !resp.OK {
		t.Fatalf("excluded autocapture should still respond ok, got %+v", resp)
	}
	resp = send(t, sockPath, Request{Cmd: CmdAutoCapture, Args: map[string]string{
		"exit": "1", "text": "false", "source": "/dev/pts/5",
	}})
	if !resp.OK {
		t.Fatalf("non-zero exit should respond ok (ignored), got %+v", resp)
	}
	list = listCards(t, sockPath)
	if len(list) != 2 {
		t.Fatalf("cards = %d, want 2 (ls and false must be skipped)", len(list))
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

func TestAutoCaptureIgnoredOutsideConditions(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()

	// Auto mode disabled: even in an inventory session nothing queues.
	precreateInventorySession(t, sessionRoot, "acme", nil)
	done, sockPath := startAutoTestDaemon(t, stateDir, sessionRoot, false)
	resp := send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme", "mode": "inventory"}})
	if !resp.OK {
		t.Fatalf("start inventory: %+v", resp)
	}
	resp = send(t, sockPath, Request{Cmd: CmdAutoCapture, Args: map[string]string{
		"exit": "0", "text": "whoami", "source": "/dev/pts/5",
	}})
	if !resp.OK {
		t.Fatalf("autocapture with auto disabled should respond ok, got %+v", resp)
	}
	if n := len(listCards(t, sockPath)); n != 0 {
		t.Fatalf("auto disabled must not queue, cards = %d", n)
	}
	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done

	// A normal-mode session ignores autocapture.
	stateDir2 := filepath.Join(t.TempDir(), "state")
	sessionRoot2 := t.TempDir()
	done2, sockPath2 := startAutoTestDaemon(t, stateDir2, sessionRoot2, true)
	resp = send(t, sockPath2, Request{Cmd: CmdStart, Args: map[string]string{"name": "normal-session"}})
	if !resp.OK {
		t.Fatalf("start normal: %+v", resp)
	}
	resp = send(t, sockPath2, Request{Cmd: CmdAutoCapture, Args: map[string]string{
		"exit": "0", "text": "whoami", "source": "/dev/pts/5",
	}})
	if !resp.OK || !strings.Contains(resp.Message, "not in inventory mode") {
		t.Fatalf("normal-mode session should report ignored, got %+v", resp)
	}
	send(t, sockPath2, Request{Cmd: CmdDaemonStop})
	<-done2

	// No active session at all.
	stateDir3 := filepath.Join(t.TempDir(), "state")
	done3, sockPath3 := startAutoTestDaemon(t, stateDir3, t.TempDir(), true)
	resp = send(t, sockPath3, Request{Cmd: CmdAutoCapture, Args: map[string]string{
		"exit": "0", "text": "whoami", "source": "/dev/pts/5",
	}})
	if !resp.OK || !strings.Contains(resp.Message, "no active session") {
		t.Fatalf("no-session autocapture should report ignored, got %+v", resp)
	}
	send(t, sockPath3, Request{Cmd: CmdDaemonStop})
	<-done3
}

func TestAutoCaptureTmuxSourceFallsBackToText(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	precreateInventorySession(t, sessionRoot, "acme", nil)
	done, sockPath := startAutoTestDaemon(t, stateDir, sessionRoot, true)
	resp := send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme", "mode": "inventory"}})
	if !resp.OK {
		t.Fatalf("start inventory: %+v", resp)
	}

	// tmux sources try to capture the full command+output from the session
	// log; in the test environment there is no tmux server, so the handler
	// must fall back to the recorded command text rather than losing the
	// command or failing the request.
	resp = send(t, sockPath, Request{Cmd: CmdAutoCapture, Args: map[string]string{
		"exit": "0", "text": "whoami", "source": "%7",
	}})
	if !resp.OK {
		t.Fatalf("tmux-source autocapture should succeed (fallback), got %+v", resp)
	}
	list := listCards(t, sockPath)
	if len(list) != 1 || list[0].Text != "whoami" {
		t.Fatalf("tmux fallback card = %+v, want the command text", list)
	}
	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

// listCards returns the pending cards for the active session.
func listCards(t *testing.T, sockPath string) []inventory.Card {
	t.Helper()
	resp := send(t, sockPath, Request{Cmd: CmdList})
	if !resp.OK {
		t.Fatalf("list: %+v", resp)
	}
	var list ListData
	if err := json.Unmarshal(resp.Data, &list); err != nil {
		t.Fatalf("unmarshal list data: %v", err)
	}
	return list.Cards
}
