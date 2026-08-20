package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"snapshell/internal/config"
)

// testConfig returns a config rooted at sessionRoot, avoiding any real
// ~/.config/snapshell side effects during tests.
func testConfig(t *testing.T, sessionRoot string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Paths.SessionRoot = sessionRoot
	return cfg
}

func startTestDaemon(t *testing.T, stateDir, sessionRoot string) (done chan error, sockPath string) {
	t.Helper()
	done = make(chan error, 1)
	go func() {
		done <- Run(Options{Config: testConfig(t, sessionRoot), StateDir: stateDir, DisableHotkeys: true})
	}()
	sockPath = filepath.Join(stateDir, "daemon.sock")
	waitForSocket(t, sockPath)
	return done, sockPath
}

func waitForSocket(t *testing.T, sockPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", sockPath)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("daemon socket %s did not appear", sockPath)
}

func send(t *testing.T, sockPath string, req Request) Response {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode request: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", line, err)
	}
	return resp
}

func TestSessionLifecycle(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)

	// No session yet.
	resp := send(t, sockPath, Request{Cmd: CmdStatus})
	if !resp.OK || !strings.Contains(resp.Message, "no active session") {
		t.Fatalf("expected no active session, got %+v", resp)
	}

	// Start a session.
	resp = send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme"}})
	if !resp.OK {
		t.Fatalf("start should succeed, got %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(sessionRoot, "acme", "attachments")); err != nil {
		t.Fatalf("attachments dir not created: %v", err)
	}
	// The active-session pointer points the shell hook at this session's
	// marker-record log under <session_root>/logs/<name>/markers.logs.
	pointerPath := filepath.Join(stateDir, "activesession")
	pointer, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("read activesession pointer: %v", err)
	}
	wantLog := filepath.Join(sessionRoot, "logs", "acme", "markers.logs")
	if string(pointer) != wantLog {
		t.Fatalf("activesession = %q, want %q", string(pointer), wantLog)
	}
	if _, err := os.Stat(filepath.Dir(wantLog)); err != nil {
		t.Fatalf("session log dir not created: %v", err)
	}

	// A second start while active must fail and not switch sessions.
	resp = send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme"}})
	if resp.OK {
		t.Fatalf("second start of same session should fail, got %+v", resp)
	}
	resp = send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "other"}})
	if resp.OK {
		t.Fatalf("start while active should fail, got %+v", resp)
	}

	// Status reports the active session.
	resp = send(t, sockPath, Request{Cmd: CmdStatus})
	if !resp.OK || !strings.Contains(resp.Message, "acme") {
		t.Fatalf("expected active session acme, got %+v", resp)
	}

	// Stop, then resume the same session (idempotent folder creation).
	resp = send(t, sockPath, Request{Cmd: CmdStop})
	if !resp.OK {
		t.Fatalf("stop should succeed, got %+v", resp)
	}
	// Stopping clears the active-session pointer.
	if _, err := os.Stat(pointerPath); !os.IsNotExist(err) {
		t.Fatalf("activesession pointer should be removed on stop")
	}
	resp = send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme"}})
	if !resp.OK || !strings.Contains(resp.Message, "resumed") {
		t.Fatalf("resume should succeed with 'resumed', got %+v", resp)
	}
	resp = send(t, sockPath, Request{Cmd: CmdStop})
	if !resp.OK {
		t.Fatalf("second stop should succeed, got %+v", resp)
	}

	// Clean shutdown via daemon_stop.
	resp = send(t, sockPath, Request{Cmd: CmdDaemonStop})
	if !resp.OK {
		t.Fatalf("daemon_stop should succeed, got %+v", resp)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daemon Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not exit after daemon_stop")
	}

	// PID, socket, and active-session pointer files must be cleaned up.
	if _, err := os.Stat(filepath.Join(stateDir, "daemon.pid")); !os.IsNotExist(err) {
		t.Fatalf("pid file not removed")
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatalf("socket file not removed")
	}
	if _, err := os.Stat(pointerPath); !os.IsNotExist(err) {
		t.Fatalf("activesession pointer not removed on shutdown")
	}
}

func TestRefuseSecondDaemon(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)

	err := Run(Options{Config: testConfig(t, sessionRoot), StateDir: stateDir, DisableHotkeys: true})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second daemon should refuse to start, got err=%v", err)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

func TestStalePidIsCleared(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	os.MkdirAll(stateDir, 0o700)

	// A stale pid pointing at a long-dead pid (1 is init, alive on Linux;
	// use an impossible high pid instead — but if a stale pid points at a
	// live process we must refuse, which is covered by TestRefuseSecondDaemon).
	if err := os.WriteFile(filepath.Join(stateDir, "daemon.pid"), []byte("99999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)

	resp := send(t, sockPath, Request{Cmd: CmdStatus})
	if !resp.OK {
		t.Fatalf("daemon should start normally despite stale pid, got %+v", resp)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

func TestRecycledPidWithDeadSocketIsCleared(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	os.MkdirAll(stateDir, 0o700)

	// A stale PID pointing at a *live* process that is NOT our daemon (a
	// recycled PID): os.Getpid() is alive but nothing listens on the socket.
	// The socket is the authority — the daemon must start anyway, not refuse
	// on a live-looking PID.
	if err := os.WriteFile(filepath.Join(stateDir, "daemon.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)

	resp := send(t, sockPath, Request{Cmd: CmdStatus})
	if !resp.OK {
		t.Fatalf("daemon should start despite a recycled (live) pid with a dead socket, got %+v", resp)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

func TestCleanupNotBlockedByHungHotkeyUnregister(t *testing.T) {
	stateDir := t.TempDir()
	d := &Daemon{
		logger: log.New(io.Discard, "", 0),
		// A hotkey unregister that never completes (blocked X event loop)
		// must not keep the daemon alive: shutdown has to complete anyway.
		unregHook:         func() { select {} },
		pidPath:           filepath.Join(stateDir, "daemon.pid"),
		sockPath:          filepath.Join(stateDir, "daemon.sock"),
		activeSessionPath: filepath.Join(stateDir, "activesession"),
	}
	os.WriteFile(d.pidPath, []byte("999\n"), 0o600)

	start := time.Now()
	d.cleanup()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("cleanup took %v with a hung unregister, want <= ~2s", elapsed)
	}
	if _, err := os.Stat(d.pidPath); !os.IsNotExist(err) {
		t.Fatalf("pid file not removed despite hung unregister")
	}
}

func TestMalformedRequest(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "this is not json\n")
	line, _ := bufio.NewReader(conn).ReadBytes('\n')
	conn.Close()
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("expected an ok=false response, got %q", line)
	}
	if resp.OK {
		t.Fatalf("malformed request should yield ok=false, got %+v", resp)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

// TestConcurrentStartStop hammers the daemon with concurrent IPC requests
// to catch data races on session state. Run with -race.
func TestReloadConfigApplies(t *testing.T) {
	d := &Daemon{
		hotkeysDisabled:   true,
		logger:            log.New(io.Discard, "", 0),
		activeSessionPath: filepath.Join(t.TempDir(), "activesession"),
	}
	d.cfg.Store(testConfig(t, t.TempDir()))

	calls := 0
	d.loadConfig = func() (*config.Config, error) {
		calls++
		cfg := testConfig(t, t.TempDir())
		cfg.Popup.Width = 999
		return cfg, nil
	}

	d.reloadConfig()

	if calls != 1 {
		t.Fatalf("loadConfig called %d times, want 1", calls)
	}
	if got := d.cfg.Load().Popup.Width; got != 999 {
		t.Fatalf("width = %d after reload, want reloaded 999", got)
	}
}

func TestReloadConfigFailureKeepsOldConfig(t *testing.T) {
	d := &Daemon{hotkeysDisabled: true, logger: log.New(io.Discard, "", 0)}
	d.cfg.Store(testConfig(t, t.TempDir()))
	d.loadConfig = func() (*config.Config, error) { return nil, errors.New("parse error") }

	d.reloadConfig()

	if got := d.cfg.Load().Popup.Width; got != 560 {
		t.Fatalf("width = %d after failed reload, want untouched 560", got)
	}
}

func TestOnHotkeyReloadsWhenEnabled(t *testing.T) {
	d := &Daemon{hotkeysDisabled: true, logger: log.New(io.Discard, "", 0)}
	cfg := testConfig(t, t.TempDir())
	reloadOn := true
	cfg.Capture.ReloadOnHotkey = &reloadOn
	d.cfg.Store(cfg)
	d.mu.Lock()
	d.session = &Session{Name: "acme", Dir: t.TempDir()}
	d.mu.Unlock()

	reloaded := false
	d.loadConfig = func() (*config.Config, error) {
		reloaded = true
		return testConfig(t, t.TempDir()), nil
	}
	captured := make(chan string, 1)
	d.captureHandler = func(kind string, s *Session) { captured <- kind }

	d.onHotkey("note")

	select {
	case kind := <-captured:
		if kind != "note" {
			t.Fatalf("captured kind %q, want note", kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("capture handler not invoked")
	}
	if !reloaded {
		t.Fatal("reload_on_hotkey should reload config before capturing")
	}
}

func TestOnHotkeySkipsReloadWhenDisabled(t *testing.T) {
	d := &Daemon{hotkeysDisabled: true, logger: log.New(io.Discard, "", 0)}
	d.cfg.Store(testConfig(t, t.TempDir())) // reload_on_hotkey defaults off
	d.mu.Lock()
	d.session = &Session{Name: "acme", Dir: t.TempDir()}
	d.mu.Unlock()

	reloaded := false
	d.loadConfig = func() (*config.Config, error) {
		reloaded = true
		return testConfig(t, t.TempDir()), nil
	}
	d.captureHandler = func(kind string, s *Session) {}

	d.onHotkey("note")
	time.Sleep(100 * time.Millisecond)
	if reloaded {
		t.Fatal("reload_on_hotkey disabled, but config was reloaded")
	}
}

func TestConcurrentStartStop(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("s%d", i%4)
			send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": name}})
			send(t, sockPath, Request{Cmd: CmdStop})
			send(t, sockPath, Request{Cmd: CmdStatus})
		}(i)
	}
	wg.Wait()

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}
