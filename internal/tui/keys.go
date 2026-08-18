package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"snapshell/internal/inventory"
)

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
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.sel > 0 {
			m.sel--
			m.setDetailContent()
		}
	case "down", "j":
		if m.sel < len(m.cards)-1 {
			m.sel++
			m.setDetailContent()
		}
	case "pgup":
		if m.selectedCode() {
			m.detailVP.HalfPageUp()
		}
	case "pgdown":
		if m.selectedCode() {
			m.detailVP.HalfPageDown()
		}
	case "a":
		// Append as-is: commit the selected card with no caption.
		if len(m.cards) > 0 {
			id := m.cards[m.sel].ID
			return m, opCmd(func() error { return m.opts.Client.Commit(id, "") })
		}
	case "c":
		if len(m.cards) > 0 {
			m.st = stateCaption
			m.caption.SetValue("")
			m.captionPreview = ""
			m.caption.Focus()
		}
	case "d":
		if len(m.cards) > 0 {
			m.st = stateDiscard
		}
	case "n":
		m.st = stateNote
		m.note.SetValue("")
		m.notePreview = ""
		m.note.Focus()
	case "v":
		m.st = stateRender
		m.renderVP.GotoTop()
		return m, m.refreshRender()
	case "enter":
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

// captionKey handles typing a caption for the selected card. Ctrl+S submits
// (via IPC commit); Esc cancels without touching the card.
func (m model) captionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.st = stateBrowse
		m.caption.Blur()
		return m, nil
	case "ctrl+s":
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

// noteKey handles typing a standalone note. Ctrl+S submits (straight to
// blog.md via IPC, never a queued card); Esc discards the typed text.
func (m model) noteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.st = stateBrowse
		m.note.Blur()
		return m, nil
	case "ctrl+s":
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

// discardKey asks for an inline y/n confirmation. Only y actually calls the
// IPC discard; n/Esc cancels.
func (m model) discardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "Y":
		if len(m.cards) > 0 {
			id := m.cards[m.sel].ID
			return m, opCmd(func() error { return m.opts.Client.Discard(id) })
		}
		return m, nil
	case "n", "N", "esc", "q":
		m.st = stateBrowse
		return m, nil
	}
	return m, nil
}

// renderKey scrolls the read-only blog.md view; v/Esc return to the list.
func (m model) renderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "v", "esc":
		m.st = stateBrowse
		return m, nil
	}
	var cmd tea.Cmd
	m.renderVP, cmd = m.renderVP.Update(msg)
	return m, cmd
}

// imageKey handles the full-screen kitty image view (stateImage); the stale
// image is cleared by the delete escape the next browse frame emits. Esc/v
// return to the list, q quits.
func (m model) imageKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "v", "esc":
		m.st = stateBrowse
		return m, nil
	}
	return m, nil
}
