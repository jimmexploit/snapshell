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
	"strings"
	"time"

	bubbleskey "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"snapshell/internal/blog"
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
	BlogImageScale   float64       // "view blog" screenshot size multiplier: 1.0 = fit the render pane
	BlogImageAlign   string        // "view blog" screenshot horizontal position: "left", "center", "right"
	BlogImagePadding int           // edge gap in cells for left/right blog alignment (0 = flush)
	Keys             Keys          // key bindings; zero value = DefaultKeys
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
	if opts.BlogImageScale <= 0 {
		opts.BlogImageScale = 1
	}
	if opts.BlogImageAlign != "center" && opts.BlogImageAlign != "right" {
		opts.BlogImageAlign = "left"
	}
	if opts.ImageRender != "inline" {
		opts.ImageRender = "tab"
	}
	if opts.Keys.Quit == nil {
		opts.Keys = DefaultKeys()
	}
	// Measure the terminal's real cell ratio (kitty CSI 16 t) before bubbletea
	// owns stdin, so centered/right-aligned blog screenshots place exactly.
	queryCellSize()
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
	keys Keys

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
	// poll refresh doesn't reset the user's scroll position. detailRaw is
	// the uncolored card text that produced it, so unchanged content skips
	// re-highlighting on every poll.
	detailContent string
	detailRaw     string
	detailVP      viewport.Model

	renderContent   string
	renderVP        viewport.Model
	renderImgBlocks []blogImageBlock
	renderer        *glamour.TermRenderer
	renderWidth     int

	// previewRenderer renders the caption/note markdown previews. It wraps
	// at construction time (like renderer), so it is cached per width and
	// rebuilt on resize and on the typing debounce.
	previewRenderer    *glamour.TermRenderer
	previewRenderWidth int

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
	if opts.BlogImageScale <= 0 {
		opts.BlogImageScale = 1
	}
	if opts.BlogImageAlign != "center" && opts.BlogImageAlign != "right" {
		opts.BlogImageAlign = "left"
	}
	if opts.ImageRender != "inline" {
		opts.ImageRender = "tab"
	}
	if opts.Keys.Quit == nil {
		opts.Keys = DefaultKeys()
	}
	caption := textarea.New()
	caption.Placeholder = "Caption (optional) — Ctrl+S appends, Esc cancels"
	caption.ShowLineNumbers = false
	caption.Prompt = ""
	caption.SetWidth(40)
	caption.SetHeight(4)
	bindWordKeys(&caption.KeyMap)

	note := textarea.New()
	note.Placeholder = "Write your note here…  Ctrl+S appends, Esc cancels"
	note.ShowLineNumbers = false
	note.Prompt = ""
	note.SetWidth(40)
	note.SetHeight(4)
	bindWordKeys(&note.KeyMap)

	return model{
		opts:     opts,
		keys:     opts.Keys,
		caption:  caption,
		note:     note,
		detailVP: viewport.New(40, 8),
		renderVP: viewport.New(40, 8),
	}
}

// bindWordKeys extends the textarea keymap with ctrl+left/right as
// word-by-word movement, in addition to the bubbles defaults (alt+left/right
// and alt+b/alt+f). bubbletea maps the CSI sequences kitty and other
// terminals send for ctrl+arrow (e.g. \x1b[1;5D) to the ctrl+left/ctrl+right
// keys.
func bindWordKeys(km *textarea.KeyMap) {
	km.WordBackward = bubbleskey.NewBinding(
		bubbleskey.WithKeys("alt+left", "alt+b", "ctrl+left"),
		bubbleskey.WithHelp("alt+left / ctrl+left", "word backward"),
	)
	km.WordForward = bubbleskey.NewBinding(
		bubbleskey.WithKeys("alt+right", "alt+f", "ctrl+right"),
		bubbleskey.WithHelp("alt+right / ctrl+right", "word forward"),
	)
}

// ensurePreviewRenderer builds the markdown renderer for the caption/note
// previews when it is missing or was constructed for a different width
// (glamour wraps at construction time). Pick dark/light from the terminal's
// actual background, like the blog renderer.
func (m *model) ensurePreviewRenderer(w int) {
	if m.previewRenderer != nil && m.previewRenderWidth == w {
		return
	}
	style := "dark"
	if !lipgloss.HasDarkBackground() {
		style = "light"
	}
	if r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(w)); err == nil {
		m.previewRenderer = r
		m.previewRenderWidth = w
	}
}

// mdPreview renders markdown preview text at width w so the user sees the
// rendered result (bullet lists, bold, code blocks, …) instead of raw
// markup. Falls back to plain word-wrapped text when no renderer is cached
// for this width (e.g. before the first debounce after a resize) or glamour
// fails.
func (m model) mdPreview(s string, w int) string {
	if m.previewRenderer != nil && m.previewRenderWidth == w {
		if out, err := m.previewRenderer.Render(s); err == nil {
			return strings.TrimSuffix(out, "\n")
		}
	}
	return wrapText(s, w)
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
		// The preview renderers wrap at construction time; drop them so the
		// next debounce rebuilds at the new width.
		m.previewRenderer = nil
		m.previewRenderWidth = 0
		if m.inputActive() {
			w := m.width
			if m.st == stateCaption {
				w, _, _ = m.paneDims()
			}
			m.ensurePreviewRenderer(w)
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
		w := m.width
		if m.st == stateCaption {
			w, _, _ = m.paneDims()
		}
		m.ensurePreviewRenderer(w)
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
			m.renderVP.SetContent(m.composeRender(msg.content, msg.width))
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
// without resetting scroll when the same card is still selected. Code cards
// are syntax-highlighted (matching the glamour palette) so the preview reads
// like the rendered blog.
func (m *model) setDetailContent() {
	content := ""
	raw := ""
	if len(m.cards) > 0 && m.cards[m.sel].Kind == inventory.KindCode {
		raw = m.cards[m.sel].Text
		content = highlightCode(raw, blog.DetectLang(raw))
	}
	if raw != m.detailRaw {
		m.detailRaw = raw
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
