package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"snapshell/internal/inventory"
)

// Keys is the review TUI's key bindings. Each field lists every key name
// (as bubbletea reports them: "q", "ctrl+c", "up", "enter", "esc", "pgup",
// "y", ...) that triggers the action. A zero Options.Keys falls back to
// DefaultKeys. ctrl+c is always bound as the quit/interrupt key in every
// state and is not part of these lists.
type Keys struct {
	Quit     []string
	Up       []string
	Down     []string
	PageUp   []string
	PageDown []string
	Append   []string
	Caption  []string
	Discard  []string
	Note     []string
	Blog     []string
	Open     []string
	Submit   []string
	Cancel   []string
	Confirm  []string
	Decline  []string
}

// DefaultKeys returns the built-in bindings.
func DefaultKeys() Keys {
	return Keys{
		Quit:     []string{"q", "ctrl+c"},
		Up:       []string{"up", "k"},
		Down:     []string{"down", "j"},
		PageUp:   []string{"pgup"},
		PageDown: []string{"pgdown"},
		Append:   []string{"a"},
		Caption:  []string{"c"},
		Discard:  []string{"d"},
		Note:     []string{"n"},
		Blog:     []string{"v"},
		Open:     []string{"enter"},
		Submit:   []string{"ctrl+s"},
		Cancel:   []string{"esc"},
		Confirm:  []string{"y", "Y"},
		Decline:  []string{"n", "N"},
	}
}

// hit reports whether msg is one of the keys in list.
func (m model) hit(list []string, msg tea.KeyMsg) bool {
	for _, k := range list {
		if k == msg.String() {
			return true
		}
	}
	return false
}

// isQuit reports whether msg quits the TUI: the configured Quit keys, or
// ctrl+c, which is always bound as the terminal interrupt regardless of the
// user's bindings.
func (m model) isQuit(msg tea.KeyMsg) bool {
	return msg.String() == "ctrl+c" || m.hit(m.keys.Quit, msg)
}

// handleKey routes a key press by the current UI state.
func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.st {
	case stateCaption:
		return m.captionKey(msg)
	case stateNote:
		return m.noteKey(msg)
	case stateDiscard:
		return m.discardKey(msg)
	case stateRender:
		return m.renderKey(msg)
	case stateImage:
		return m.imageKey(msg)
	default:
		return m.browseKey(msg)
	}
}

func (m model) browseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.isQuit(msg):
		return m, tea.Quit
	case m.hit(m.keys.Up, msg):
		if m.sel > 0 {
			m.sel--
			m.setDetailContent()
		}
	case m.hit(m.keys.Down, msg):
		if m.sel < len(m.cards)-1 {
			m.sel++
			m.setDetailContent()
		}
	case m.hit(m.keys.PageUp, msg):
		if m.selectedCode() {
			m.detailVP.HalfPageUp()
		}
	case m.hit(m.keys.PageDown, msg):
		if m.selectedCode() {
			m.detailVP.HalfPageDown()
		}
	case m.hit(m.keys.Append, msg):
		// Append as-is: commit the selected card with no caption.
		if len(m.cards) > 0 {
			id := m.cards[m.sel].ID
			return m, opCmd(func() error { return m.opts.Client.Commit(id, "") })
		}
	case m.hit(m.keys.Caption, msg):
		if len(m.cards) > 0 {
			m.st = stateCaption
			m.caption.SetValue("")
			m.captionPreview = ""
			m.caption.Focus()
		}
	case m.hit(m.keys.Discard, msg):
		if len(m.cards) > 0 {
			m.st = stateDiscard
		}
	case m.hit(m.keys.Note, msg):
		m.st = stateNote
		m.note.SetValue("")
		m.notePreview = ""
		m.note.Focus()
	case m.hit(m.keys.Blog, msg):
		m.st = stateRender
		m.renderVP.GotoTop()
		return m, m.refreshRender()
	case m.hit(m.keys.Open, msg):
		if len(m.cards) > 0 && m.cards[m.sel].Kind == inventory.KindImage {
			if m.imageMode() == "kitty" && m.imageRows(m.cards[m.sel]) > 0 {
				m.st = stateImage
				return m, nil
			}
			return m, m.openImageCmd(m.cards[m.sel])
		}
	}
	return m, nil
}

func (m model) selectedCode() bool {
	return len(m.cards) > 0 && m.cards[m.sel].Kind == inventory.KindCode
}

// captionKey handles typing a caption for the selected card. Submit (default
// Ctrl+S) submits via IPC commit; Cancel (default Esc) cancels without
// touching the card. ctrl+c always quits.
func (m model) captionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "ctrl+c":
		return m, tea.Quit
	case m.hit(m.keys.Cancel, msg):
		m.st = stateBrowse
		m.caption.Blur()
		return m, nil
	case m.hit(m.keys.Submit, msg):
		if len(m.cards) > 0 {
			id := m.cards[m.sel].ID
			caption := m.caption.Value()
			return m, opCmd(func() error { return m.opts.Client.Commit(id, caption) })
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.caption, cmd = m.caption.Update(msg)
	if m.caption.Value() != m.captionPreview {
		return m, tea.Batch(cmd, debounceCmd())
	}
	return m, cmd
}

// noteKey handles typing a standalone note. Submit (default Ctrl+S) submits
// straight to blog.md via IPC, never a queued card; Cancel (default Esc)
// discards the typed text.
func (m model) noteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "ctrl+c":
		return m, tea.Quit
	case m.hit(m.keys.Cancel, msg):
		m.st = stateBrowse
		m.note.Blur()
		return m, nil
	case m.hit(m.keys.Submit, msg):
		text := m.note.Value()
		return m, opCmd(func() error { return m.opts.Client.Note(text) })
	}

	var cmd tea.Cmd
	m.note, cmd = m.note.Update(msg)
	if m.note.Value() != m.notePreview {
		return m, tea.Batch(cmd, debounceCmd())
	}
	return m, cmd
}

// discardKey asks for an inline confirmation. Confirm (default y/Y) actually
// calls the IPC discard; Decline (default n/N) or Cancel (default Esc) backs
// out without touching the card.
func (m model) discardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "ctrl+c":
		return m, tea.Quit
	case m.hit(m.keys.Confirm, msg):
		if len(m.cards) > 0 {
			id := m.cards[m.sel].ID
			return m, opCmd(func() error { return m.opts.Client.Discard(id) })
		}
		return m, nil
	case m.hit(m.keys.Decline, msg) || m.hit(m.keys.Cancel, msg):
		m.st = stateBrowse
		return m, nil
	}
	return m, nil
}

// renderKey scrolls the read-only blog.md view; Blog or Cancel return to the
// list.
func (m model) renderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.isQuit(msg):
		return m, tea.Quit
	case m.hit(m.keys.Blog, msg) || m.hit(m.keys.Cancel, msg):
		m.st = stateBrowse
		return m, nil
	}
	var cmd tea.Cmd
	m.renderVP, cmd = m.renderVP.Update(msg)
	return m, cmd
}

// imageKey handles the full-screen kitty image view (stateImage); the stale
// image is cleared by the delete escape the next browse frame emits. Blog or
// Cancel return to the list, Quit quits.
func (m model) imageKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.isQuit(msg):
		return m, tea.Quit
	case m.hit(m.keys.Blog, msg) || m.hit(m.keys.Cancel, msg):
		m.st = stateBrowse
		return m, nil
	}
	return m, nil
}
