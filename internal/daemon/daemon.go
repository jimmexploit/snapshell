package daemon

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"snapshell/internal/blog"
	"snapshell/internal/capture/screenshot"
	"snapshell/internal/capture/selection"
	"snapshell/internal/capture/tmuxcap"
	"snapshell/internal/config"
	"snapshell/internal/hotkeys"
	"snapshell/internal/notify"
	"snapshell/internal/popup"
)

// Options configures a daemon run. Zero values pick sensible defaults so
// the daemon can run standalone from the CLI.
type Options struct {
	// Config is the loaded configuration. When nil, the daemon loads it
	// itself (creating ~/.config/snapshell/config.toml on first run).
	Config *config.Config
	// StateDir overrides the state directory (defaults to
	// ~/.local/state/snapshell). Used by tests.
	StateDir string
	// DisableHotkeys skips X11 key grabbing. Used by tests so they never
	// touch the real X server.
	DisableHotkeys bool
}

// Session is the in-memory state of the active session.
type Session struct {
	Name      string
	Dir       string
	AttachNum int // last-assigned attachment number (derived on resume)
}

// CaptureHandler is invoked on hotkey firing. kind is "screenshot", "code",
// or "note". It runs with no session state held; the daemon looks up the
// current session and passes it to the handler.
type CaptureHandler func(kind string, s *Session)

// Daemon is the long-running process. It owns the socket server and the
// in-memory session state. All shared state is guarded by mu.
type Daemon struct {
	mu      sync.Mutex
	session *Session

	logger       *log.Logger
	listener     net.Listener
	shutdown     chan struct{}
	shutdownOnce sync.Once
	unregHook    func()

	// sessionRoot is the parent directory sessions are created under.
	sessionRoot string

	// cfg is the loaded configuration (screenshot tool, popup size, ...).
	// It is an atomic pointer because the reload hotkey / reload_on_hotkey
	// swap it out while capture goroutines are reading it concurrently.
	cfg atomic.Pointer[config.Config]

	// loadConfig reads the config file; overridable in tests. Defaults to
	// config.Load.
	loadConfig func() (*config.Config, error)

	// hotkeysDisabled mirrors Options.DisableHotkeys so reload can skip
	// re-grabbing keys in test mode.
	hotkeysDisabled bool

	// screenshotFallbackWarn dedupes the one-time flameshot-fallback warning.
	screenshotFallbackWarn sync.Once

	// socket paths (derived from stateDir).
	sockPath string
	pidPath  string
	logPath  string
	// activeSessionPath is where the active-session pointer lives (points
	// the shell hook at the active session's command log). See
	// ActiveSessionPath.
	activeSessionPath string

	// captureHandler dispatches Alt+1/2/3 flows.
	captureHandler CaptureHandler
}

// Run starts the daemon and blocks until it is stopped (via IPC, SIGTERM,
// or SIGINT).
func Run(opts Options) error {
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = StateDir()
	}

	cfg := opts.Config
	if cfg == nil {
		loaded, err := config.Load()
		if err != nil {
			// A broken/missing home config must not kill the daemon —
			// fall back to built-in defaults so it still runs.
			loaded = config.Default()
		}
		cfg = loaded
	}

	sessionRoot := expandHome(cfg.Paths.SessionRoot)
	if sessionRoot == "" {
		sessionRoot = filepath.Join(mustHome(), "snapshell")
	}

	d := &Daemon{
		shutdown:          make(chan struct{}),
		sessionRoot:       sessionRoot,
		cfg:               atomic.Pointer[config.Config]{},
		loadConfig:        config.Load,
		sockPath:          filepath.Join(stateDir, "daemon.sock"),
		pidPath:           filepath.Join(stateDir, "daemon.pid"),
		logPath:           filepath.Join(stateDir, "daemon.log"),
		activeSessionPath: filepath.Join(stateDir, "activesession"),
	}
	d.cfg.Store(cfg)

	if err := d.start(opts.DisableHotkeys); err != nil {
		return err
	}
	d.serve()
	return nil
}

func mustHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

// expandHome resolves a leading ~/ in a path. Config values may arrive here
// unexpanded (Default() keeps them literal), so the daemon normalizes them
// before creating session folders.
func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		return filepath.Join(mustHome(), p[2:])
	}
	return p
}

func (d *Daemon) start(disableHotkeys bool) error {
	if err := os.MkdirAll(filepath.Dir(d.sockPath), 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(d.sockPath), "markers"), 0o700); err != nil {
		return fmt.Errorf("create markers dir: %w", err)
	}

	// PID file: refuse to double-start against a live process.
	if err := d.acquirePid(); err != nil {
		return err
	}

	d.logger = openLog(d.logPath)
	d.logger.Printf("daemon starting, pid=%d", os.Getpid())

	// Socket: remove a stale socket before listening.
	if err := d.removeStaleSocket(); err != nil {
		return err
	}
	ln, err := net.Listen("unix", d.sockPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", d.sockPath, err)
	}
	d.listener = ln

	d.captureHandler = d.dispatchCapture
	d.hotkeysDisabled = disableHotkeys

	if !disableHotkeys {
		if err := d.registerHotkeys(); err != nil {
			d.logger.Printf("hotkeys: %v", err)
			_ = notify.Send("snapshell", err.Error())
		}
	} else {
		d.logger.Printf("hotkeys disabled (test mode)")
	}

	d.logger.Printf("daemon started, pid=%d", os.Getpid())
	return nil
}

// registerHotkeys grabs the configured global hotkeys (default Alt+1/2/3/4/5).
// Grab failures are reported but do not abort the daemon — the daemon stays
// usable for session/IPC purposes.
func (d *Daemon) registerHotkeys() error {
	cfg := d.cfg.Load()
	unregister, err := hotkeys.GrabAll(
		map[string]string{
			"screenshot": cfg.Keymaps.Screenshot,
			"code":       cfg.Keymaps.Command,
			"note":       cfg.Keymaps.Note,
			"selection":  cfg.Keymaps.Selection,
			"reload":     cfg.Keymaps.Reload,
		},
		map[string]hotkeys.Handler{
			"screenshot": func() { d.onHotkey("screenshot") },
			"code":       func() { d.onHotkey("code") },
			"note":       func() { d.onHotkey("note") },
			"selection":  func() { d.onHotkey("selection") },
			"reload":     func() { d.reloadConfig() },
		},
	)
	if err != nil {
		d.logger.Printf("hotkey grab warning: %v", err)
	}
	if unregister != nil {
		d.unregHook = unregister
	}
	return err
}

// onHotkey runs in the X event loop goroutine. It spawns the capture flow
// in its own goroutine so the event loop is never blocked.
func (d *Daemon) onHotkey(kind string) {
	d.logger.Printf("hotkey: %s", kind)

	d.mu.Lock()
	s := d.session
	d.mu.Unlock()
	if s == nil {
		d.logger.Printf("hotkey %s ignored: no active session", kind)
		_ = notify.Send("snapshell", "no active snapshell session — run 'snapshell start <name>' first")
		return
	}
	// reload_on_hotkey: pick up config edits before this capture runs.
	if d.cfg.Load().ReloadOnHotkeyOn() {
		d.reloadConfig()
	}
	if d.captureHandler == nil {
		return
	}
	go d.captureHandler(kind, s)
}

// reloadConfig re-reads the config file and applies it live, then re-grabs
// hotkeys so keymap changes take effect. Config reloading is independent of
// session state: the active session keeps its folder, only new captures use
// the new values.
func (d *Daemon) reloadConfig() {
	load := d.loadConfig
	if load == nil {
		load = config.Load
	}
	loaded, err := load()
	if err != nil {
		d.logger.Printf("reload: %v", err)
		_ = notify.Send("snapshell", "config reload failed: "+err.Error())
		return
	}
	d.cfg.Store(loaded)
	d.logger.Printf("reload: config applied")
	_ = notify.Send("snapshell", "config reloaded")
	d.reregisterHotkeys()
}

// reregisterHotkeys drops the current X11 grabs and grabs again from the
// (possibly changed) config. No-op when hotkeys are disabled.
func (d *Daemon) reregisterHotkeys() {
	if d.hotkeysDisabled {
		return
	}
	if d.unregHook != nil {
		d.unregHook()
		d.unregHook = nil
	}
	if err := d.registerHotkeys(); err != nil {
		d.logger.Printf("reload: hotkey grab failed: %v", err)
		_ = notify.Send("snapshell", "config reloaded, but some hotkeys failed to grab: "+err.Error())
	}
}

func openLog(path string) *log.Logger {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		// Last resort: fall back to stderr so a logging failure is visible
		// rather than silent.
		return log.New(os.Stderr, "snapshell: ", log.LstdFlags)
	}
	return log.New(f, "", log.LstdFlags)
}

func (d *Daemon) acquirePid() error {
	if data, err := os.ReadFile(d.pidPath); err == nil {
		pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
		if perr == nil && pid > 0 && processAlive(pid) {
			return fmt.Errorf("daemon already running (pid=%d), refusing to start a second instance", pid)
		}
		// Stale PID: clean it (and any stale socket) below.
		_ = os.Remove(d.pidPath)
	}
	return os.WriteFile(d.pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 performs no actual signal delivery, only an existence check.
	return proc.Signal(syscall.Signal(0)) == nil
}

func (d *Daemon) removeStaleSocket() error {
	if _, err := os.Stat(d.sockPath); os.IsNotExist(err) {
		return nil
	}
	// Try to connect: if something answers, this socket is in use and we
	// must not clobber it.
	conn, err := net.Dial("unix", d.sockPath)
	if err == nil {
		conn.Close()
		return fmt.Errorf("socket %s is already in use by a live listener", d.sockPath)
	}
	if err := os.Remove(d.sockPath); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", d.sockPath, err)
	}
	d.logger.Printf("removed stale socket %s", d.sockPath)
	return nil
}

func (d *Daemon) serve() {
	go d.handleSignals()

	for {
		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-d.shutdown:
				d.cleanup()
				return
			default:
				d.logger.Printf("accept error: %v", err)
				continue
			}
		}
		go d.handleConn(conn)
	}
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			d.logger.Printf("read request: %v", err)
		}
		return
	}

	req, err := decodeRequest(sc.Bytes())
	if err != nil {
		d.writeResponse(conn, fail(err.Error()))
		return
	}
	d.logger.Printf("request: %s %v", req.Cmd, req.Args)

	switch req.Cmd {
	case CmdStart:
		d.writeResponse(conn, d.handleStart(req))
	case CmdStop:
		d.writeResponse(conn, d.handleStop())
	case CmdStatus:
		d.writeResponse(conn, d.handleStatus())
	case CmdDaemonStop:
		d.writeResponse(conn, ok("daemon shutting down"))
		d.triggerShutdown()
	default:
		d.writeResponse(conn, fail(fmt.Sprintf("unknown command %q", req.Cmd)))
	}
}

// triggerShutdown closes the listener (so a blocked Accept returns) and
// then the shutdown channel. Safe to call multiple times.
func (d *Daemon) triggerShutdown() {
	d.shutdownOnce.Do(func() {
		if d.listener != nil {
			_ = d.listener.Close()
		}
		close(d.shutdown)
	})
}

func (d *Daemon) writeResponse(conn net.Conn, resp Response) {
	b, err := encodeResponse(resp)
	if err != nil {
		d.logger.Printf("encode response: %v", err)
		return
	}
	if _, err := conn.Write(b); err != nil {
		d.logger.Printf("write response: %v", err)
	}
}

func (d *Daemon) handleStart(req Request) Response {
	name := strings.TrimSpace(req.Args["name"])
	if name == "" {
		return fail("start requires a session name")
	}
	if strings.ContainsAny(name, "/") {
		return fail("session name must not contain '/'")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.session != nil {
		return fail(fmt.Sprintf("session %q is already active — stop it before starting another", d.session.Name))
	}

	sessionDir, created, err := setupSessionDir(d.sessionRoot, name)
	if err != nil {
		return fail(fmt.Sprintf("create session folder: %v", err))
	}

	// Point the shell hook at this session's command log so every command
	// run while the session is active lands in
	// <session_root>/logs/<name>/commands.log.
	logPath := d.sessionLogPath(name)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fail(fmt.Sprintf("create session log dir: %v", err))
	}
	if err := writePointer(d.activeSessionPath, logPath); err != nil {
		return fail(fmt.Sprintf("write active session pointer: %v", err))
	}

	d.session = &Session{Name: name, Dir: sessionDir, AttachNum: countAttachments(sessionDir)}
	d.logger.Printf("session started: %s (dir=%s, log=%s, attachments=%d)", name, sessionDir, logPath, d.session.AttachNum)
	if created {
		return ok(fmt.Sprintf("started session %q", name))
	}
	return ok(fmt.Sprintf("resumed existing session %q", name))
}

func (d *Daemon) handleStop() Response {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.session == nil {
		return fail("no active session")
	}
	name := d.session.Name
	d.session = nil
	_ = os.Remove(d.activeSessionPath)
	d.logger.Printf("session stopped: %s", name)
	return ok(fmt.Sprintf("stopped session %q", name))
}

func (d *Daemon) handleStatus() Response {
	d.mu.Lock()
	defer d.mu.Unlock()

	base := fmt.Sprintf("daemon running (pid=%d)", os.Getpid())
	if d.session == nil {
		return ok(base + "; no active session")
	}
	count, err := entryCount(d.session.Dir)
	if err != nil {
		d.logger.Printf("count entries: %v", err)
	}
	return ok(fmt.Sprintf("%s; active session: %s (%d entries)", base, d.session.Name, count))
}

// sessionLogPath returns the append-only command log path for a session:
// <session_root>/logs/<name>/commands.log. Every completed command run
// while the session is active is recorded here (see ActiveSessionPath).
func (d *Daemon) sessionLogPath(name string) string {
	return filepath.Join(d.sessionRoot, "logs", name, "commands.log")
}

// writePointer atomically writes a small pointer file (via temp + rename).
func writePointer(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pointer-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// setupSessionDir creates <root>/<name>/ and <root>/<name>/attachments/
// idempotently. Returns the dir path and whether it was newly created.
func setupSessionDir(root, name string) (dir string, created bool, err error) {
	dir = filepath.Join(root, name)
	attach := filepath.Join(dir, "attachments")

	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		if err := os.MkdirAll(attach, 0o700); err != nil {
			return "", false, fmt.Errorf("attachments dir: %w", err)
		}
		return dir, false, nil
	}

	if err := os.MkdirAll(attach, 0o700); err != nil {
		return "", false, err
	}
	return dir, true, nil
}

// entryCount counts the entries appended to blog.md so far.
func entryCount(sessionDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "blog.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return strings.Count(string(data), "<!-- "), nil
}

// countAttachments returns the number of files in the session's
// attachments/ folder. Used to resume the attachment counter after a crash
// or on an idempotent session resume.
func countAttachments(sessionDir string) int {
	entries, err := os.ReadDir(filepath.Join(sessionDir, "attachments"))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}

// dispatchCapture routes a hotkey firing to its capture flow. It runs in
// its own goroutine per firing (see onHotkey).
func (d *Daemon) dispatchCapture(kind string, s *Session) {
	switch kind {
	case "screenshot":
		d.captureScreenshot(s)
	case "code":
		d.captureCode(s)
	case "note":
		d.captureNote(s)
	case "selection":
		d.captureSelection(s)
	default:
		d.logger.Printf("capture: unhandled hotkey kind %q", kind)
	}
}

// nextAttachment allocates the next attachment number for a session. It is
// safe for concurrent capture goroutines.
func (d *Daemon) nextAttachment(s *Session) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	s.AttachNum++
	return s.AttachNum
}

// captureScreenshot runs the Alt+1 flow: screenshot tool → attachments/NNN.png
// → popup caption → entry appended to blog.md.
func (d *Daemon) captureScreenshot(s *Session) {
	num := d.nextAttachment(s)

	tool, err := d.cfg.Load().ResolveScreenshotTool()
	if err != nil {
		d.logger.Printf("capture screenshot: %v", err)
		_ = notify.Send("snapshell", err.Error())
		return
	}
	if tool != d.cfg.Load().Screenshot.Tool {
		d.screenshotFallbackWarn.Do(func() {
			d.logger.Printf("capture: %s not found on PATH — falling back to %s", d.cfg.Load().Screenshot.Tool, tool)
		})
	}

	res, err := screenshot.Capture(s.Dir, tool, num, nil)
	if err != nil {
		d.logger.Printf("capture screenshot: %v", err)
		_ = notify.Send("snapshell", err.Error())
		return
	}
	if res.Cancelled {
		d.logger.Printf("capture screenshot: cancelled, no entry added")
		return
	}

	// The caption window runs inside this capture goroutine, so a slow or
	// ignored dialog never blocks the daemon or the next hotkey press.
	// The dialog failing must not lose the screenshot that was just taken:
	// it is still appended, just without a caption.
	if err := popup.Capture(popup.ModeImage, s.Dir, res.RelPath, "", d.cfg.Load().Popup.Width, d.cfg.Load().Popup.Height, d.cfg.Load().Popup.Font, d.cfg.Load().Popup.Position, d.cfg.Load().Themes.Name); err != nil {
		d.logger.Printf("capture screenshot: popup: %v", err)
		_ = notify.Send("snapshell", err.Error())
		if err := blog.Append(s.Dir, blog.Entry{Kind: blog.KindImage, ImagePath: res.RelPath}); err != nil {
			d.logger.Printf("capture screenshot: fallback append blog: %v", err)
		}
		return
	}
}

// captureCode runs the Alt+2 flow: the most recently completed command's
// text (from the command log when in tmux, otherwise the shell hook's
// recorded command) → popup caption → entry appended to blog.md.
func (d *Daemon) captureCode(s *Session) {
	res, err := tmuxcap.Capture(d.sessionLogPath(s.Name), d.cfg.Load().OutputIncluded())
	if err != nil {
		if !errors.Is(err, tmuxcap.ErrNotInTmux) {
			// In tmux but nothing captured (empty command log, bad record):
			// show the specific, actionable error rather than falling back to
			// a possibly-stale plain-shell last command.
			d.logger.Printf("capture tmux: %v", err)
			_ = notify.Send("snapshell", err.Error())
			return
		}
		// Not in tmux: there are no row records, so fall back to the
		// command text the shell hook recorded. Full output needs tmux —
		// the notification says so instead of staying silent.
		text, rerr := readLastCommand()
		if rerr != nil || strings.TrimSpace(text) == "" {
			d.logger.Printf("capture tmux: %v", err)
			_ = notify.Send("snapshell", err.Error())
			return
		}
		d.logger.Printf("capture tmux: %v — falling back to recorded last command", err)
		_ = notify.Send("snapshell", "not in tmux — capturing last command only (no output)")
		d.appendCodeEntry(s, text, "lastcommand", popup.ModeCode)
		return
	}
	d.appendCodeEntry(s, res.Text, "tmux", popup.ModeCode)
}

// appendCodeEntry is the shared tail of the code/selection capture paths:
// show the caption window for the captured text, then append it to blog.md.
func (d *Daemon) appendCodeEntry(s *Session, text, source, mode string) {
	if strings.TrimSpace(text) == "" {
		d.logger.Printf("capture %s: empty capture, no entry added", source)
		return
	}

	// Same reasoning as the image flow: the captured command text is
	// valuable on its own, so if the caption window can't spawn the entry
	// is still appended without a caption.
	if err := popup.Capture(mode, s.Dir, "", text, d.cfg.Load().Popup.Width, d.cfg.Load().Popup.Height, d.cfg.Load().Popup.Font, d.cfg.Load().Popup.Position, d.cfg.Load().Themes.Name); err != nil {
		d.logger.Printf("capture %s: popup: %v", source, err)
		_ = notify.Send("snapshell", err.Error())
		if err := blog.Append(s.Dir, blog.Entry{Kind: blog.KindCode, CodeText: text}); err != nil {
			d.logger.Printf("capture %s: fallback append blog: %v", source, err)
		}
		return
	}
}

// readLastCommand returns the most recent command text recorded by the
// shell hook (plain-shell Alt+2 fallback).
func readLastCommand() (string, error) {
	data, err := os.ReadFile(LastCommandPath())
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}

// captureNote runs the Alt+3 flow: a note form window collects the text
// and appends it to blog.md itself. There is no fallback entry — the note
// text only exists inside the form.
func (d *Daemon) captureNote(s *Session) {
	if err := popup.Capture(popup.ModeNote, s.Dir, "", "", d.cfg.Load().Popup.Width, d.cfg.Load().Popup.Height, d.cfg.Load().Popup.Font, d.cfg.Load().Popup.Position, d.cfg.Load().Themes.Name); err != nil {
		d.logger.Printf("capture note: popup: %v", err)
		_ = notify.Send("snapshell", err.Error())
		return
	}
}

// captureSelection runs the Alt+4 flow: the currently selected text
// (falling back to the clipboard when nothing is selected) → caption popup
// → entry appended to blog.md. An empty selection+clipboard is not an
// error, just a notification.
func (d *Daemon) captureSelection(s *Session) {
	text, err := selection.Read()
	if err != nil {
		if errors.Is(err, selection.ErrEmpty) {
			d.logger.Printf("capture selection: nothing selected and clipboard empty")
			_ = notify.Send("snapshell", err.Error())
			return
		}
		d.logger.Printf("capture selection: %v", err)
		_ = notify.Send("snapshell", err.Error())
		return
	}
	d.appendCodeEntry(s, text, "selection", popup.ModeSelection)
}

func (d *Daemon) handleSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	select {
	case sig := <-ch:
		d.logger.Printf("received signal %s, shutting down", sig)
		d.triggerShutdown()
	case <-d.shutdown:
	}
	signal.Stop(ch)
}

func (d *Daemon) cleanup() {
	if d.unregHook != nil {
		d.unregHook()
	}
	if d.listener != nil {
		_ = d.listener.Close()
	}
	_ = os.Remove(d.activeSessionPath)
	_ = os.Remove(d.pidPath)
	_ = os.Remove(d.sockPath)
	if d.logger != nil {
		d.logger.Printf("daemon stopped, pid=%d", os.Getpid())
	}
}
