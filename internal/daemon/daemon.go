package daemon

import (
	"bufio"
	"encoding/json"
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
	"time"

	"snapshell/internal/blog"
	"snapshell/internal/capture/screenshot"
	"snapshell/internal/capture/selection"
	"snapshell/internal/capture/tmuxcap"
	"snapshell/internal/config"
	"snapshell/internal/hotkeys"
	"snapshell/internal/inventory"
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

// SessionMode is the mode a session runs in: normal (popup on every
// capture) or inventory (silent captures queued for review).
type SessionMode string

const (
	ModeNormal    SessionMode = "normal"
	ModeInventory SessionMode = "inventory"
)

// Session is the in-memory state of the active session.
type Session struct {
	Name      string
	Dir       string
	AttachNum int // last-assigned attachment number (derived on resume)
	Mode      SessionMode
	// Queue is the pending-card queue, non-nil only for inventory sessions.
	// It is owned by the daemon (single writer); the review TUI mutates it
	// over IPC.
	Queue *inventory.Queue
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

	d.logger = openLog(d.logPath)

	// PID file: refuse to double-start against a live process. The socket is
	// the authority (a recycled PID alone proves nothing).
	if err := d.acquirePid(); err != nil {
		return err
	}

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
		if perr == nil && pid > 0 && processAlive(pid) && d.socketListening() {
			return fmt.Errorf("daemon already running (pid=%d), refusing to start a second instance", pid)
		}
		// Stale PID: the PID is dead, or it was recycled by an unrelated
		// process while no daemon is listening on the socket. Clean it
		// (and any stale socket) below.
		if pid > 0 && processAlive(pid) {
			d.logger.Printf("pid %d is alive but no daemon is listening on %s — treating it as stale (recycled PID)", pid, d.sockPath)
		}
		_ = os.Remove(d.pidPath)
	}
	return os.WriteFile(d.pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
}

// socketListening reports whether a live listener is accepting connections
// on the daemon socket. The socket is the authority on whether a daemon is
// actually running: a PID file alone can be fooled by a recycled PID.
func (d *Daemon) socketListening() bool {
	if _, err := os.Stat(d.sockPath); os.IsNotExist(err) {
		return false
	}
	conn, err := net.Dial("unix", d.sockPath)
	if err != nil {
		return false
	}
	conn.Close()
	return true
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
	case CmdList:
		d.writeResponse(conn, d.handleList())
	case CmdCommit:
		d.writeResponse(conn, d.handleCommit(req))
	case CmdDiscard:
		d.writeResponse(conn, d.handleDiscard(req))
	case CmdNote:
		d.writeResponse(conn, d.handleNote(req))
	case CmdAutoCapture:
		d.writeResponse(conn, d.handleAutoCapture(req))
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

	// The mode is expressed by whether the CLI passed "inventory" as the
	// first argument after `start`; the daemon just receives it as an arg.
	requested := SessionMode(strings.TrimSpace(req.Args["mode"]))
	if requested == "" {
		requested = ModeNormal
	}
	if requested != ModeNormal && requested != ModeInventory {
		return fail(fmt.Sprintf("unknown session mode %q", requested))
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

	// A resumed session keeps the mode it was started with — never let a
	// bare `start` silently downgrade an inventory session, nor `start
	// inventory` upgrade a normal one. A brand-new session simply takes the
	// requested mode.
	if !created {
		existing := readMode(sessionDir)
		if existing != requested {
			if existing == ModeInventory {
				return fail(fmt.Sprintf("session %q exists in inventory mode — use 'snapshell start inventory %s' to resume it, or 'snapshell inventory' to open the review UI", name, name))
			}
			return fail(fmt.Sprintf("session %q exists in normal mode — it cannot be opened in inventory mode", name))
		}
	}
	if err := writeMode(sessionDir, requested); err != nil {
		return fail(fmt.Sprintf("write session mode: %v", err))
	}

	// Point the shell hook at this session's marker-record log so every
	// command run while the session is active lands in
	// <session_root>/logs/<name>/markers.logs.
	logPath := d.sessionLogPath(name)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fail(fmt.Sprintf("create session log dir: %v", err))
	}
	if err := writePointer(d.activeSessionPath, logPath); err != nil {
		return fail(fmt.Sprintf("write active session pointer: %v", err))
	}

	d.session = &Session{Name: name, Dir: sessionDir, AttachNum: countAttachments(sessionDir), Mode: requested}
	if requested == ModeInventory {
		q, err := inventory.Load(sessionDir)
		if err != nil {
			return fail(fmt.Sprintf("load pending cards: %v", err))
		}
		d.session.Queue = q
	}
	d.logger.Printf("session started: %s (dir=%s, log=%s, attachments=%d, mode=%s)", name, sessionDir, logPath, d.session.AttachNum, requested)
	blogPath := filepath.Join(sessionDir, "blog.md")
	if created {
		return ok(fmt.Sprintf("started session %q (%s mode); blog: %s", name, requested, blogPath))
	}
	return ok(fmt.Sprintf("resumed existing session %q (%s mode); blog: %s", name, requested, blogPath))
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
		resp := ok(base + "; no active session")
		data, err := json.Marshal(StatusData{})
		if err == nil {
			resp.Data = data
		}
		return resp
	}
	count, err := entryCount(d.session.Dir)
	if err != nil {
		d.logger.Printf("count entries: %v", err)
	}
	mode := d.session.Mode
	if mode == "" {
		mode = ModeNormal
	}
	pending := 0
	if d.session.Queue != nil {
		pending = d.session.Queue.Len()
	}
	msg := fmt.Sprintf("%s; active session: %s (%s mode, %d entries)", base, d.session.Name, mode, count)
	if mode == ModeInventory {
		msg += fmt.Sprintf(", %d pending", pending)
	}
	msg += fmt.Sprintf("; blog: %s", filepath.Join(d.session.Dir, "blog.md"))
	resp := ok(msg)
	if data, err := json.Marshal(StatusData{Session: d.session.Name, Mode: string(mode), Entries: count, Pending: pending, Dir: d.session.Dir}); err == nil {
		resp.Data = data
	}
	return resp
}

// handleList returns the active session's pending cards, oldest-first. The
// image cards carry a derived absolute path so the review TUI can open the
// file without resolving session-relative paths itself; the payload also
// carries the session dir for the TUI's read-only blog.md render view.
func (d *Daemon) handleList() Response {
	d.mu.Lock()
	defer d.mu.Unlock()

	q, errResp, ok := d.activeQueueLocked()
	if !ok {
		return errResp
	}
	cards := q.List()
	for i := range cards {
		if cards[i].Kind == inventory.KindImage {
			cards[i].AbsPath = filepath.Join(d.session.Dir, cards[i].Path)
		}
	}
	data, err := json.Marshal(ListData{Dir: d.session.Dir, Cards: cards})
	if err != nil {
		return fail(fmt.Sprintf("marshal pending cards: %v", err))
	}
	return Response{OK: true, Message: fmt.Sprintf("%d pending", len(cards)), Data: data}
}

// handleCommit appends a pending card to blog.md (with an optional caption)
// and removes it from the queue. Empty caption = append as-is.
func (d *Daemon) handleCommit(req Request) Response {
	id, err := strconv.Atoi(strings.TrimSpace(req.Args["id"]))
	if err != nil {
		return fail("commit requires a numeric card id")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	q, errResp, haveQueue := d.activeQueueLocked()
	if !haveQueue {
		return errResp
	}
	if err := q.Commit(id, req.Args["caption"], d.cfg.Load().CaptionAfter()); err != nil {
		return fail(err.Error())
	}
	d.logger.Printf("inventory: committed card %d", id)
	return ok(fmt.Sprintf("committed card %d", id))
}

// handleDiscard permanently removes a pending card (deleting the underlying
// screenshot for image cards). It requires an explicit confirm flag in the
// request so the TUI's own y/n prompt is never the only safety check.
func (d *Daemon) handleDiscard(req Request) Response {
	if strings.TrimSpace(req.Args["confirm"]) != "true" {
		return fail("discard is permanent and requires confirm=true")
	}
	id, err := strconv.Atoi(strings.TrimSpace(req.Args["id"]))
	if err != nil {
		return fail("discard requires a numeric card id")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	q, errResp, haveQueue := d.activeQueueLocked()
	if !haveQueue {
		return errResp
	}
	if err := q.Discard(id); err != nil {
		return fail(err.Error())
	}
	d.logger.Printf("inventory: discarded card %d", id)
	return ok(fmt.Sprintf("discarded card %d", id))
}

// handleNote appends a standalone note to the active session's blog.md as a
// plain paragraph (the same entry the Alt+3 flow produces). Notes never pass
// through the pending queue — they are written directly.
func (d *Daemon) handleNote(req Request) Response {
	text := strings.TrimSpace(req.Args["text"])
	if text == "" {
		return fail("note requires text")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.session == nil {
		return fail("no active session")
	}
	if err := blog.Append(d.session.Dir, blog.Entry{Kind: blog.KindNote, NoteText: text}); err != nil {
		return fail(fmt.Sprintf("append note: %v", err))
	}
	d.logger.Printf("inventory: appended standalone note")
	return ok("note added")
}

// handleAutoCapture decides whether a completed command becomes a pending
// code card. The shell hook sends one of these after every command that
// exited 0 while a session is active; this handler is the single
// decision-maker and single writer for the auto path: auto mode must be
// enabled in the config, the session must be an inventory session, and the
// command must not be on the [auto].exclude list. Everything else returns
// ok without touching the queue — an ignored autocapture is not an error,
// and the hook must never see a failure it can't act on.
//
// The card text is built the way Alt+2 would: for a tmux pane the full
// prompt+command+output (including scrolled content) is captured from the
// session's marker record; for a plain terminal the recorded command text
// is used, plus the output read back from the kitty window when the command
// ran in one. A capture failure falls back to the recorded command text, so
// auto mode degrades to a command-only card rather than losing the command.
func (d *Daemon) handleAutoCapture(req Request) Response {
	text := req.Args["text"]
	exit := strings.TrimSpace(req.Args["exit"])
	source := req.Args["source"]
	kittyWindow := req.Args["kitty-window"]
	kittyListen := req.Args["kitty-listen"]

	if exit != "0" {
		return ok("autocapture ignored: command did not exit 0")
	}
	if strings.TrimSpace(text) == "" {
		return ok("autocapture ignored: empty command")
	}

	cfg := d.cfg.Load()
	if !cfg.AutoCaptureEnabled() {
		return ok("autocapture ignored: auto mode disabled")
	}
	if cfg.AutoCaptureExcluded(text) {
		d.logger.Printf("autocapture: %q excluded by [auto].exclude", firstLineOf(text))
		return ok("autocapture ignored: command excluded")
	}

	// Snapshot the session pointer under the lock; the tmux/kitty capture
	// below is a subprocess and must never run while holding d.mu.
	d.mu.Lock()
	s := d.session
	d.mu.Unlock()
	if s == nil {
		return ok("autocapture ignored: no active session")
	}
	if s.Mode != ModeInventory || s.Queue == nil {
		return ok("autocapture ignored: session is not in inventory mode")
	}

	capture := text
	switch {
	case strings.HasPrefix(source, "%"):
		// tmux pane: the _hook-mark end phase wrote this command's row
		// record to the session log just before _hook-record ran, so the
		// last record is this command. Capture the full prompt+output.
		res, err := tmuxcap.CaptureN(d.sessionLogPath(s.Name), cfg.OutputIncluded(), 1)
		if err != nil {
			d.logger.Printf("autocapture: tmux capture for %s: %v (using command text)", s.Name, err)
		} else if strings.TrimSpace(res.Text) != "" {
			capture = res.Text
		}
	case kittyWindow != "":
		// Plain kitty terminal: append the command's output read back from
		// the window, like Alt+2 does. Best-effort — a dead window keeps
		// just the command text.
		if out, err := tmuxcap.KittyOutput(kittyWindow, kittyListen); err == nil {
			if out = strings.TrimRight(out, "\n"); out != "" {
				capture = text + "\n" + out
			}
		}
	}

	if err := s.Queue.AppendCode(capture); err != nil {
		d.logger.Printf("autocapture: enqueue: %v", err)
		return fail(fmt.Sprintf("queue command: %v", err))
	}
	d.logger.Printf("autocapture: queued %q (%d pending)", firstLineOf(text), s.Queue.Len())
	return ok(fmt.Sprintf("queued command (%d pending)", s.Queue.Len()))
}

// firstLineOf returns the first non-empty line of s for log messages
// (commands can be multi-line; one line keeps the log readable).
func firstLineOf(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// activeQueueLocked returns the active session's inventory queue, or the
// response to send when there's no session / no inventory queue. The bool is
// false in the failure case. d.mu must be held.
func (d *Daemon) activeQueueLocked() (*inventory.Queue, Response, bool) {
	if d.session == nil {
		return nil, fail("no active session"), false
	}
	if d.session.Mode != ModeInventory || d.session.Queue == nil {
		return nil, fail(fmt.Sprintf("session %q is not in inventory mode", d.session.Name)), false
	}
	return d.session.Queue, Response{}, true
}

// sessionLogPath returns the marker-record log path for a session:
// <session_root>/logs/<name>/markers.logs. Every completed command run
// while the session is active is recorded here as a row/tty/ktty record
// (see ActiveSessionPath). The per-command human-readable transcript lives
// next to it in commands.logs.
func (d *Daemon) sessionLogPath(name string) string {
	return filepath.Join(d.sessionRoot, "logs", name, "markers.logs")
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

// modeFilePath is where the session's mode lives (<session>/ .snapshell-mode).
// It rides inside the session folder so a resumed session — after a daemon
// restart or a stop/start cycle — remembers its original mode.
func modeFilePath(sessionDir string) string {
	return filepath.Join(sessionDir, ".snapshell-mode")
}

// readMode returns the mode a session folder was started with. A session
// folder without the marker (created before inventory mode existed) is
// normal mode.
func readMode(sessionDir string) SessionMode {
	data, err := os.ReadFile(modeFilePath(sessionDir))
	if err != nil {
		return ModeNormal
	}
	if strings.TrimSpace(string(data)) == string(ModeInventory) {
		return ModeInventory
	}
	return ModeNormal
}

// writeMode records the mode a session is running in.
func writeMode(sessionDir string, m SessionMode) error {
	return os.WriteFile(modeFilePath(sessionDir), []byte(string(m)+"\n"), 0o600)
}

// entryCount counts the entries appended to blog.md so far. Entries are
// blank-line-separated top-level blocks: an image line (or its caption),
// a code fence, or a note paragraph. Blank lines inside code fences must
// not split an entry, so the scan tracks fence state.
func entryCount(sessionDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "blog.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return countEntries(string(data)), nil
}

// countEntries is the entry counter's pure core. The first non-blank line
// is the "# <name>" header and is skipped; a blank line outside a code
// fence ends the current entry; any line of ≥3 backticks opens a fence
// (tracking its run length so only the matching closing fence exits), and
// inside a fence nothing is treated as a boundary.
func countEntries(s string) int {
	count := 0
	inEntry := false
	fenceLen := 0
	headerSkipped := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if !headerSkipped {
			if trimmed != "" {
				headerSkipped = true
				if run := backtickRun(trimmed); run >= 3 {
					fenceLen = run
				}
			}
			continue
		}
		if trimmed == "" {
			if fenceLen == 0 {
				inEntry = false
			}
			continue
		}
		run := backtickRun(trimmed)
		if fenceLen > 0 {
			if run >= fenceLen {
				fenceLen = 0
			}
			continue
		}
		if !inEntry {
			count++
			inEntry = true
		}
		if run >= 3 {
			fenceLen = run
		}
	}
	return count
}

// backtickRun returns the length of the leading run of backtick characters
// on a line.
func backtickRun(line string) int {
	n := 0
	for n < len(line) && line[n] == '`' {
		n++
	}
	return n
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
// its own goroutine per firing (see onHotkey). In inventory mode captures
// queue silently instead of popping the caption window; normal mode keeps
// today's behavior unchanged.
func (d *Daemon) dispatchCapture(kind string, s *Session) {
	if s.Mode == ModeInventory {
		d.dispatchInventoryCapture(kind, s)
		return
	}
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

// dispatchInventoryCapture handles hotkey firings for an inventory session.
// Screenshot, command, and selection captures run their normal capture
// mechanics but skip the popup and land in the pending queue; the raw note
// hotkey is a no-op (standalone notes are written from the review TUI only).
func (d *Daemon) dispatchInventoryCapture(kind string, s *Session) {
	switch kind {
	case "screenshot":
		d.captureScreenshot(s)
	case "code":
		d.captureCode(s)
	case "selection":
		d.captureSelection(s)
	case "note":
		d.logger.Printf("inventory: note hotkey ignored in inventory mode")
		_ = notify.Send("snapshell", "in inventory mode, notes are written from the review TUI — run 'snapshell inventory'")
	default:
		d.logger.Printf("capture: unhandled hotkey kind %q", kind)
	}
}

// enqueueImage adds a captured screenshot to the active session's pending
// queue (inventory mode).
func (d *Daemon) enqueueImage(s *Session, relPath string) {
	d.enqueue(s, func(q *inventory.Queue) error { return q.AppendImage(relPath) })
}

// enqueueCode adds captured command text to the active session's pending
// queue (inventory mode).
func (d *Daemon) enqueueCode(s *Session, text string) {
	d.enqueue(s, func(q *inventory.Queue) error { return q.AppendCode(text) })
}

// enqueue runs a queue mutation and reports the outcome with a notification
// so a silent capture is never silent about landing.
func (d *Daemon) enqueue(s *Session, mutate func(*inventory.Queue) error) {
	if s.Queue == nil {
		d.logger.Printf("inventory: no pending queue for session %s", s.Name)
		_ = notify.Send("snapshell", "no pending queue for this session — is it in inventory mode?")
		return
	}
	if err := mutate(s.Queue); err != nil {
		d.logger.Printf("inventory: enqueue: %v", err)
		_ = notify.Send("snapshell", "failed to queue capture: "+err.Error())
		return
	}
	n := s.Queue.Len()
	d.logger.Printf("inventory: queued capture, %d pending", n)
	_ = notify.Send("snapshell", fmt.Sprintf("captured — %d pending", n))
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

	// Inventory mode: skip the popup entirely, queue the file as a pending
	// card for the review TUI.
	if s.Mode == ModeInventory {
		d.enqueueImage(s, res.RelPath)
		return
	}

	// The caption window runs inside this capture goroutine, so a slow or
	// ignored dialog never blocks the daemon or the next hotkey press.
	// The dialog failing must not lose the screenshot that was just taken:
	// it is still appended, just without a caption.
	if err := popup.Capture(popup.ModeImage, s.Dir, res.RelPath, "", d.cfg.Load().Popup.Width, d.cfg.Load().Popup.Height, d.cfg.Load().Popup.Font, d.cfg.Load().Popup.Position, d.cfg.Load().Themes.Name, 1, d.cfg.Load().CaptionAfter()); err != nil {
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
// recorded command) → popup caption → entry appended to blog.md. A digit
// pressed right after Alt+2 (1-9) captures that many commands together.
func (d *Daemon) captureCode(s *Session) {
	count := d.commandCount()
	res, err := tmuxcap.CaptureN(d.sessionLogPath(s.Name), d.cfg.Load().OutputIncluded(), count)
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
		d.appendCodeEntryOrQueue(s, text, "lastcommand", 1)
		return
	}
	d.appendCodeEntryOrQueue(s, res.Text, "tmux", res.Count)
}

// appendCodeEntryOrQueue is the shared tail of the code capture paths. In
// inventory mode the captured text is queued as a pending card instead of
// showing the caption window; normal mode keeps the popup + blog-append
// flow. count is how many commands the capture spans (code mode only).
func (d *Daemon) appendCodeEntryOrQueue(s *Session, text, source string, count int) {
	if s.Mode == ModeInventory {
		if strings.TrimSpace(text) == "" {
			d.logger.Printf("inventory capture %s: empty capture, nothing queued", source)
			return
		}
		d.enqueueCode(s, text)
		return
	}
	d.appendCodeEntry(s, text, source, popup.ModeCode, count)
}

// commandCount blocks briefly after Alt+2 waiting for a digit (1-9) that
// sets how many recent commands to capture at once. It returns 1 — the
// default — when no digit is pressed in time, when hotkeys are disabled
// (tests), or when the digit listener can't start. Only called from within
// a capture goroutine, so the wait never blocks the hotkey event loop.
func (d *Daemon) commandCount() int {
	if d.hotkeysDisabled {
		return 1
	}
	digit, err := hotkeys.WaitForDigit(d.cfg.Load().CountTimeout())
	if err != nil {
		d.logger.Printf("command count: %v", err)
		return 1
	}
	if digit > 0 {
		d.logger.Printf("command count: capturing last %d commands", digit)
		return digit
	}
	return 1
}

// appendCodeEntry is the shared tail of the code/selection capture paths:
// show the caption window for the captured text, then append it to blog.md.
// count is how many commands the capture spans (code mode only); the popup
// reflects it in its title when it's more than one.
func (d *Daemon) appendCodeEntry(s *Session, text, source, mode string, count int) {
	if strings.TrimSpace(text) == "" {
		d.logger.Printf("capture %s: empty capture, no entry added", source)
		return
	}

	// Same reasoning as the image flow: the captured command text is
	// valuable on its own, so if the caption window can't spawn the entry
	// is still appended without a caption.
	if err := popup.Capture(mode, s.Dir, "", text, d.cfg.Load().Popup.Width, d.cfg.Load().Popup.Height, d.cfg.Load().Popup.Font, d.cfg.Load().Popup.Position, d.cfg.Load().Themes.Name, count, d.cfg.Load().CaptionAfter()); err != nil {
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
	if err := popup.Capture(popup.ModeNote, s.Dir, "", "", d.cfg.Load().Popup.Width, d.cfg.Load().Popup.Height, d.cfg.Load().Popup.Font, d.cfg.Load().Popup.Position, d.cfg.Load().Themes.Name, 1, false); err != nil {
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
	if s.Mode == ModeInventory {
		d.enqueueCode(s, text)
		return
	}
	d.appendCodeEntry(s, text, "selection", popup.ModeSelection, 1)
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
		// The X event loop goroutine must not be able to hold the daemon
		// hostage: if unregister never completes (blocked X event loop),
		// the process must still exit so the PID/socket are released.
		done := make(chan struct{})
		go func() {
			d.unregHook()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			d.logger.Printf("hotkey unregister timed out after 2s — continuing shutdown without it")
		}
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
