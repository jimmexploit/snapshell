// Package tui is the review UI for inventory mode: a foreground, two-column
// terminal UI where pending captures are inspected, captioned, committed to
// blog.md, or discarded. It is a pure client of the daemon — every mutation
// goes over IPC, so the daemon stays the single writer of the queue and of
// blog.md. Only the read-only render view reads blog.md off disk directly.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"snapshell/internal/inventory"
)

// Client is the TUI's view of the daemon. cmd/snapshell implements it over
// the Unix socket; the TUI itself never touches IPC details.
type Client interface {
	// List returns the session dir plus its pending cards, oldest-first.
	List() (ListResult, error)
	Commit(id int, caption string) error
	Discard(id int) error
	Note(text string) error
}

// ListResult is what Client.List returns.
type ListResult struct {
	Dir   string
	Cards []inventory.Card
}

// Options configures one TUI run.
type Options struct {
	Client           Client
	ImageViewer      string        // binary to peek at screenshots; "" = xdg-open
	CloseDelay       time.Duration // how long an opened image stays up
	ImageMode        string        // "kitty" (in-terminal) or "external" viewer
	ImageScale       float64       // full-screen (tab) size multiplier: 1.0 = fit the pane
	ImageRender      string        // "tab" (Enter opens full-screen) or "inline" (preview pane)
	ImageInlineScale float64       // inline preview size multiplier: 0.5 = half the pane fit
}

// Run starts the review TUI in the foreground and blocks until the user
// quits. Quitting never loses data — every mutation is already durable on
// the daemon side.
func Run(opts Options) error {
	if opts.CloseDelay <= 0 {
		opts.CloseDelay = 5 * time.Second
	}
	if opts.ImageScale <= 0 {
		opts.ImageScale = 1
	}
	if opts.ImageInlineScale <= 0 {
		opts.ImageInlineScale = 0.5
	}
	if opts.ImageRender != "inline" {
		opts.ImageRender = "tab"
	}
	p := tea.NewProgram(newModel(opts), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("review TUI: %w", err)
	}
	return nil
}

// state is which UI mode the user is currently in. Each is a clearly
// distinct screen — do not conflate them.
type state int

const (
	stateBrowse  state = iota // card list + preview (default)
	stateCaption              // typing a caption for the selected card
	stateNote                 // typing a standalone note (full width)
	stateDiscard              // inline y/n confirmation
	stateRender               // full-screen read-only blog.md
	stateImage                // full-screen in-terminal screenshot (kitty)
)

// Messages.
type (
	listMsg struct {
		dir   string
		cards []inventory.Card
		err   error
	}
	tickMsg     struct{}
	debounceMsg struct{}
	renderMsg   struct {
		content string
		width   int
		err     error
	}
	opResultMsg struct {
		ok  bool
		err error
	}
	openErrMsg struct{ err string }
)

const (
	pollInterval  = 1500 * time.Millisecond // how often new cards are fetched
	debounceDelay = 350 * time.Millisecond  // pause before preview re-render
)

type model struct {
	opts Options

	width, height int
	dir           string
	cards         []inventory.Card
	sel           int
	st            state

	caption        textarea.Model
	captionPreview string
	note           textarea.Model
	notePreview    string

	// detailContent tracks what the detail viewport currently shows so a
	// poll refresh doesn't reset the user's scroll position.
	detailContent string
	detailVP      viewport.Model

	renderContent string
	renderVP      viewport.Model
	renderer      *glamour.TermRenderer
	renderWidth   int

	status    string
	statusErr bool
}

func newModel(opts Options) model {
	if opts.ImageScale <= 0 {
		opts.ImageScale = 1
	}
	if opts.ImageInlineScale <= 0 {
		opts.ImageInlineScale = 0.5
	}
	if opts.ImageRender != "inline" {
		opts.ImageRender = "tab"
	}
	caption := textarea.New()
	caption.Placeholder = "Caption (optional) — Ctrl+S appends, Esc cancels"
	caption.ShowLineNumbers = false
	caption.Prompt = ""
	caption.SetWidth(40)
	caption.SetHeight(4)

	note := textarea.New()
	note.Placeholder = "Write your note here…  Ctrl+S appends, Esc cancels"
	note.ShowLineNumbers = false
	note.Prompt = ""
	note.SetWidth(40)
	note.SetHeight(4)

	return model{
		opts:     opts,
		caption:  caption,
		note:     note,
		detailVP: viewport.New(40, 8),
		renderVP: viewport.New(40, 8),
	}
}

// Init primes the UI with the current queue and starts polling.
func (m model) Init() tea.Cmd {
	return tea.Batch(m.refreshList(), pollCmd())
}

func pollCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func debounceCmd() tea.Cmd {
	return tea.Tick(debounceDelay, func(time.Time) tea.Msg { return debounceMsg{} })
}

func (m model) refreshList() tea.Cmd {
	return func() tea.Msg {
		res, err := m.opts.Client.List()
		if err != nil {
			return listMsg{err: err}
		}
		return listMsg{dir: res.Dir, cards: res.Cards}
	}
}

// refreshRender reads blog.md off disk for the read-only render view. This
// is the TUI's one direct file read — it is non-mutating. The current
// terminal width rides along so the markdown can be wrapped to fit.
func (m model) refreshRender() tea.Cmd {
	return func() tea.Msg {
		if m.dir == "" {
			return renderMsg{content: m.renderContent, width: m.width}
		}
		data, err := os.ReadFile(filepath.Join(m.dir, "blog.md"))
		if err != nil {
			if os.IsNotExist(err) {
				return renderMsg{content: "", width: m.width}
			}
			return renderMsg{err: err, width: m.width}
		}
		return renderMsg{content: string(data), width: m.width}
	}
}

func opCmd(f func() error) tea.Cmd {
	return func() tea.Msg {
		if err := f(); err != nil {
			return opResultMsg{err: err}
		}
		return opResultMsg{ok: true}
	}
}

func (m *model) setStatus(s string, isErr bool) {
	m.status = s
	m.statusErr = isErr
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		if m.st == stateRender {
			// Re-render the markdown at the new width.
			return m, m.refreshRender()
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case listMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		m.dir = msg.dir
		m.cards = msg.cards
		m.clampSel()
		m.setDetailContent()
		return m, nil
	case tickMsg:
		// Pause polling while the user is mid-input so an incoming update
		// can't yank focus or reflow the list out from under them.
		if m.inputActive() {
			return m, pollCmd()
		}
		return m, tea.Batch(m.refreshList(), pollCmd())
	case debounceMsg:
		// Debounced live preview: only after the user pauses typing.
		m.captionPreview = m.caption.Value()
		m.notePreview = m.note.Value()
		return m, nil
	case renderMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		// Build the markdown renderer lazily, and only when the width
		// changes (glamour wraps at renderer-construction time). glamour's
		// WithAutoStyle is broken (the "auto" style isn't embedded), so
		// pick dark/light from the terminal's actual background.
		if m.renderer == nil || msg.width != m.renderWidth {
			style := "dark"
			if !lipgloss.HasDarkBackground() {
				style = "light"
			}
			if r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(msg.width)); err == nil {
				m.renderer = r
				m.renderWidth = msg.width
			}
		}
		changed := msg.content != m.renderContent
		m.renderContent = msg.content
		if changed || msg.width != m.renderWidth {
			content := msg.content
			if m.renderer != nil {
				if out, err := m.renderer.Render(content); err == nil {
					content = out
				}
			}
			m.renderVP.SetContent(content)
			m.renderVP.GotoTop()
		}
		return m, nil
	case opResultMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			return m, nil
		}
		m.setStatus("", false)
		// Leave the transient input states on success.
		switch m.st {
		case stateCaption:
			m.st = stateBrowse
			m.caption.Blur()
		case stateNote:
			m.st = stateBrowse
			m.note.Blur()
		case stateDiscard:
			m.st = stateBrowse
		}
		return m, tea.Batch(m.refreshList(), m.refreshRender())
	case openErrMsg:
		m.setStatus(msg.err, true)
		return m, nil
	}
	return m, nil
}

// inputActive reports whether the user is mid-typing/mid-confirmation, i.e.
// polling should pause.
func (m model) inputActive() bool {
	return m.st == stateCaption || m.st == stateNote || m.st == stateDiscard
}

func (m *model) clampSel() {
	if len(m.cards) == 0 {
		m.sel = 0
		return
	}
	if m.sel < 0 {
		m.sel = 0
	}
	if m.sel >= len(m.cards) {
		m.sel = len(m.cards) - 1
	}
}

// setDetailContent keeps the detail viewport in sync with the selected card,
// without resetting scroll when the same card is still selected.
func (m *model) setDetailContent() {
	content := ""
	if len(m.cards) > 0 && m.cards[m.sel].Kind == inventory.KindCode {
		content = m.cards[m.sel].Text
	}
	if content != m.detailContent {
		m.detailContent = content
		m.detailVP.SetContent(content)
		m.detailVP.GotoTop()
	}
}

// resize lays out the components for the current terminal size.
func (m *model) resize() {
	detailW, listW, paneH := m.paneDims()
	_ = listW

	m.caption.SetWidth(detailW)
	m.caption.SetHeight(min(8, max(2, paneH/3)))

	// The note input is full-width, per the spec.
	m.note.SetWidth(m.width)
	m.note.SetHeight(min(8, max(2, m.height/3)))

	m.detailVP.Width = detailW
	m.detailVP.Height = max(1, paneH-2)
	m.renderVP.Width = m.width
	m.renderVP.Height = max(1, paneH)
}

// paneDims returns the detail/list column widths and the shared pane height
// (terminal height minus the footer).
func (m model) paneDims() (detailW, listW, paneH int) {
	footerLines := len(linesOf(m.footerText()))
	paneH = m.height - footerLines
	if paneH < 1 {
		paneH = 1
	}
	listW = m.width / 3
	if listW < 20 {
		listW = 20
	}
	detailW = m.width - listW
	if detailW < 1 {
		detailW = 1
	}
	return detailW, listW, paneH
}
