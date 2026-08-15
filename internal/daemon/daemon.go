package daemon

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"snapshell/internal/blog"
	"snapshell/internal/capture/screenshot"
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
	cfg *config.Config

	// markersDir is where the shell hook writes per-pane row markers.
	markersDir string

	// screenshotFallbackWarn dedupes the one-time flameshot-fallback warning.
	screenshotFallbackWarn sync.Once

	// socket paths (derived from stateDir).
	sockPath string
	pidPath  string
	logPath  string

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
		shutdown:    make(chan struct{}),
		sessionRoot: sessionRoot,
		cfg:         cfg,
		markersDir:  filepath.Join(stateDir, "markers"),
		sockPath:    filepath.Join(stateDir, "daemon.sock"),
		pidPath:     filepath.Join(stateDir, "daemon.pid"),
		logPath:     filepath.Join(stateDir, "daemon.log"),
	}

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

// registerHotkeys grabs Alt+1/2/3. Grab failures are reported but do not
// abort the daemon — the daemon stays usable for session/IPC purposes.
func (d *Daemon) registerHotkeys() error {
	unregister, err := hotkeys.GrabAll(
		func() { d.onHotkey("screenshot") },
		func() { d.onHotkey("code") },
		func() { d.onHotkey("note") },
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
	if d.captureHandler == nil {
		return
	}
	go d.captureHandler(kind, s)
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

	d.session = &Session{Name: name, Dir: sessionDir, AttachNum: countAttachments(sessionDir)}
	d.logger.Printf("session started: %s (dir=%s, attachments=%d)", name, sessionDir, d.session.AttachNum)
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

	tool, err := d.cfg.ResolveScreenshotTool()
	if err != nil {
		d.logger.Printf("capture screenshot: %v", err)
		_ = notify.Send("snapshell", err.Error())
		return
	}
	if tool != d.cfg.Screenshot.Tool {
		d.screenshotFallbackWarn.Do(func() {
			d.logger.Printf("capture: %s not found on PATH — falling back to %s", d.cfg.Screenshot.Tool, tool)
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

	d.appendWithPopup(s, popup.ModeImage, res.RelPath,
		blog.Entry{Kind: blog.KindImage, ImagePath: res.RelPath})
}

// captureCode runs the Alt+2 flow: focused tmux pane → last command's text
// → popup caption → entry appended to blog.md.
func (d *Daemon) captureCode(s *Session) {
	res, err := tmuxcap.Capture(d.markersDir, d.cfg.OutputIncluded())
	if err != nil {
		d.logger.Printf("capture tmux: %v", err)
		_ = notify.Send("snapshell", err.Error())
		return
	}
	if strings.TrimSpace(res.Text) == "" {
		d.logger.Printf("capture tmux: empty capture, no entry added")
		return
	}

	tmp, err := popup.TempCodeFile(res.Text)
	if err != nil {
		d.logger.Printf("capture tmux: write temp file: %v", err)
		_ = notify.Send("snapshell", "failed to stage captured command: "+err.Error())
		return
	}

	d.appendWithPopup(s, popup.ModeCode, tmp,
		blog.Entry{Kind: blog.KindCode, CodeText: res.Text})
}

// captureNote runs the Alt+3 flow: spawn the floating note popup, which
// collects the text and appends it to blog.md itself. There is no fallback
// entry — the note text only exists inside the popup.
func (d *Daemon) captureNote(s *Session) {
	if err := d.spawnPopup(popup.ModeNote, "", s.Dir); err != nil {
		d.logger.Printf("capture note: %v", err)
		_ = notify.Send("snapshell", err.Error())
		return
	}
	d.logger.Printf("capture note popup spawned")
}

// spawnPopup resolves the configured popup terminal and launches the
// floating window. Resolving the terminal here (rather than inside popup)
// keeps tool resolution in internal/config.
func (d *Daemon) spawnPopup(mode, file, sessionDir string) error {
	term, err := d.cfg.ResolvePopupTerminal()
	if err != nil {
		return err
	}
	return popup.Spawn(selfExe(), mode, file, sessionDir,
		term, d.cfg.Popup.WidthCells, d.cfg.Popup.HeightCells)
}

// appendWithPopup is the shared tail of the image/code flows: spawn the
// popup to collect a caption and append the entry. The popup writes the
// entry (with or without caption) to blog.md itself. If the popup can't
// spawn, the capture is still appended without a caption — losing an
// already-taken screenshot or capture because the caption window failed
// would be a worse outcome.
func (d *Daemon) appendWithPopup(s *Session, mode, file string, fallback blog.Entry) {
	if err := d.spawnPopup(mode, file, s.Dir); err != nil {
		d.logger.Printf("capture %s: spawn popup: %v", mode, err)
		_ = notify.Send("snapshell", err.Error())
		if err := blog.Append(s.Dir, fallback); err != nil {
			d.logger.Printf("capture %s: fallback append blog: %v", mode, err)
		}
		return
	}
	d.logger.Printf("capture %s popup spawned", mode)
}

// selfExe returns the running daemon's own binary path, which the popup
// terminal re-invokes as `snapshell internal-popup`.
func selfExe() string {
	p, err := os.Executable()
	if err != nil {
		return "snapshell"
	}
	return p
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
	_ = os.Remove(d.pidPath)
	_ = os.Remove(d.sockPath)
	if d.logger != nil {
		d.logger.Printf("daemon stopped, pid=%d", os.Getpid())
	}
}
