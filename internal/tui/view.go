package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"snapshell/internal/blog"
	"snapshell/internal/inventory"
)

// Palette and styles. Muted by default; the selection and status lines carry
// the only strong color so the user's eye lands on the right thing.
var (
	colDim    = lipgloss.Color("240")
	colMuted  = lipgloss.Color("245")
	colAccent = lipgloss.Color("39")
	colGreen  = lipgloss.Color("42")
	colRed    = lipgloss.Color("196")
	colWarn   = lipgloss.Color("214")
)

var (
	titleStyle  = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	footerStyle = lipgloss.NewStyle().Foreground(colDim)
	dimStyle    = lipgloss.NewStyle().Foreground(colMuted)
	greenStyle  = lipgloss.NewStyle().Foreground(colGreen)
	redStyle    = lipgloss.NewStyle().Foreground(colRed)
	warnStyle   = lipgloss.NewStyle().Foreground(colWarn).Bold(true)
	selStyle    = lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("15"))
)

// View renders the whole screen: two columns (detail left, list right) plus
// a keybinding footer, or the distinct full-width states (note input, blog
// render view). The kitty frame helpers keep the terminal's displayed image
// in sync with whichever card is selected.
func (m model) View() string {
	if m.st == stateRender {
		return kittyFrameNoImage() + m.renderPane() + "\n" + m.footer()
	}
	if m.st == stateImage {
		return m.imageViewPane() + "\n" + m.footer()
	}
	if m.st == stateNote {
		return kittyFrameNoImage() + m.notePane() + "\n" + m.footer()
	}
	detailW, listW, paneH := m.paneDims()
	detail := m.detailPane(detailW, paneH)
	list := m.listPane(listW, paneH)
	prefix := ""
	if !m.showsImage() {
		prefix = kittyFrameNoImage()
	}
	return prefix + lipgloss.JoinHorizontal(lipgloss.Top, detail, list) + "\n" + m.footer()
}

// showsImage reports whether the current view displays a screenshot via the
// kitty graphics protocol — only the full-screen stateImage view does, so
// every other frame carries the delete escape that clears a stale image.
func (m model) showsImage() bool {
	return m.st == stateImage
}

// detailPane picks the left-column content by state.
func (m model) detailPane(w, h int) string {
	switch m.st {
	case stateCaption:
		return m.captionPane(w, h)
	case stateDiscard:
		return m.discardPane(w, h)
	default:
		return m.previewPane(w, h)
	}
}

// previewPane is the default detail view: the selected card's content.
// Code cards show the captured text verbatim (scrollable); image cards show
// a path/dimensions label — the screenshot itself is opened on Enter
// (full-screen in kitty, or in the external viewer), never drawn inline.
func (m model) previewPane(w, h int) string {
	if len(m.cards) == 0 {
		return fillPane(dimStyle.Render(
			"Nothing pending yet.\n\n"+
				"Captures from Alt+1 (screenshot), Alt+2 (command) and Alt+4 (selection)\n"+
				"land here silently. Write them up, then quit and run more commands."), w, h)
	}
	c := m.cards[m.sel]
	header := titleStyle.Render(kindLabel(c.Kind)) + "  " +
		dimStyle.Render(cardLabel(c)) + "  " + dimStyle.Render(relTime(c.Created))
	if c.Kind == inventory.KindImage {
		return fillPane(header+"\n\n"+imageLabel(c)+"\n\n"+dimStyle.Render("Press Enter to view"), w, h)
	}
	// The viewport fills the pane below the header.
	return fillPane(header+"\n\n"+m.detailVP.View(), w, h)
}

// imageRows returns how many terminal rows the image should occupy in the
// full-screen view, or 0 when it can't be rendered in-terminal (not running
// in kitty, missing file, not PNG, or too small to fit). Callers fall back
// to the external viewer when this is 0.
func (m model) imageRows(c inventory.Card) int {
	dbg := os.Getenv("SNAPSHELL_KITTY_DEBUG")
	logf := func(format string, a ...any) {
		if dbg != "" {
			os.WriteFile(dbg, []byte(fmt.Sprintf(format, a...)+"\n"), 0o644)
		}
	}
	if c.AbsPath == "" {
		logf("imageRows: AbsPath empty")
		return 0
	}
	if !kittyEnabled() {
		logf("imageRows: kitty disabled (TERM=%q KITTY_WINDOW_ID=%q)", os.Getenv("TERM"), os.Getenv("KITTY_WINDOW_ID"))
		return 0
	}
	cfg, format, err := imageDecode(c.AbsPath)
	if err != nil {
		logf("imageRows: imageDecode error: %v", err)
		return 0
	}
	if format != "png" {
		logf("imageRows: format is %q, want png", format)
		return 0
	}
	_, _, paneH := m.paneDims()
	rows := kittyFitRows(cfg.Width, cfg.Height, m.width, paneH)
	if rows <= 0 {
		logf("imageRows: kittyFitRows=0 (w=%d h=%d img=%dx%d)", m.width, paneH, cfg.Width, cfg.Height)
	}
	return rows
}

// imageMode resolves the configured screenshot display backend.
func (m model) imageMode() string {
	switch m.opts.ImageMode {
	case "external":
		return "external"
	default:
		return "kitty"
	}
}

// imageViewPane is the full-screen kitty image view (stateImage): the
// graphics escape on the first line, then blank lines reserving the image's
// rows. Every line is padded to the full terminal width so the renderer
// never appends an erase-to-end-of-line sequence over the image cells —
// kitty treats such an erase as a request to remove the image placement.
func (m model) imageViewPane() string {
	_, _, paneH := m.paneDims()
	if len(m.cards) == 0 {
		return fillPane("", m.width, paneH)
	}
	c := m.cards[m.sel]
	rows := m.imageRows(c)
	if rows <= 0 {
		return fillPane("", m.width, paneH)
	}
	pad := strings.Repeat(" ", m.width)
	lines := []string{kittyPadLine(kittyFrameForImage(c.AbsPath, rows), m.width)}
	for i := 0; i < rows-1; i++ {
		lines = append(lines, pad)
	}
	for len(lines) < paneH {
		lines = append(lines, pad)
	}
	return strings.Join(lines, "\n")
}

// kittyPadLine returns s followed by enough spaces to reach exactly w
// display cells, so the renderer sees a full-width line and skips its erase
// sequence.
func kittyPadLine(s string, w int) string {
	return s + strings.Repeat(" ", w)
}

// captionPane shows the caption textarea with a debounced preview of the
// blog entry it will produce.
func (m model) captionPane(w, h int) string {
	if len(m.cards) == 0 {
		return fillPane("", w, h)
	}
	c := m.cards[m.sel]
	var lines []string
	lines = append(lines, titleStyle.Render("Caption")+"  "+dimStyle.Render(cardLabel(c)))
	lines = append(lines, m.caption.View())
	lines = append(lines, "")
	lines = append(lines, titleStyle.Render("Preview"))
	lines = append(lines, m.captionPreviewText(c))
	return fillPane(strings.Join(lines, "\n"), w, h)
}

// captionPreviewText renders what committing would produce: the caption line
// (or a note that it will be appended as-is) plus the entry skeleton.
func (m model) captionPreviewText(c inventory.Card) string {
	cap := strings.TrimSpace(m.captionPreview)
	switch c.Kind {
	case inventory.KindImage:
		if cap == "" {
			return "(no caption — appended as-is)\n\n![](" + c.Path + ")"
		}
		return cap + "\n\n![](" + c.Path + ")"
	default:
		head := previewHead(c.Text)
		if cap == "" {
			return "(no caption — appended as-is)\n\n```" + blog.DetectLang(c.Text) + "\n" + head + "\n```"
		}
		return cap + "\n\n```" + blog.DetectLang(c.Text) + "\n" + head + "\n```"
	}
}

// notePane is the full-width standalone-note input, not tied to any card.
func (m model) notePane() string {
	_, _, paneH := m.paneDims()
	var lines []string
	lines = append(lines, titleStyle.Render("Standalone note"))
	lines = append(lines, m.note.View())
	lines = append(lines, "")
	lines = append(lines, titleStyle.Render("Preview"))
	if strings.TrimSpace(m.notePreview) == "" {
		lines = append(lines, dimStyle.Render("(nothing written yet — Ctrl+S saves, Esc cancels)"))
	} else {
		lines = append(lines, m.notePreview)
	}
	return fillPane(strings.Join(lines, "\n"), m.width, paneH)
}

// discardPane is the inline y/n confirmation — no separate popup.
func (m model) discardPane(w, h int) string {
	if len(m.cards) == 0 {
		return fillPane("", w, h)
	}
	c := m.cards[m.sel]
	extra := ""
	if c.Kind == inventory.KindImage {
		extra = " The file will be deleted too."
	}
	body := warnStyle.Render("Discard "+cardLabel(c)+"?") +
		"\n\nThis cannot be undone." + extra + "\n\ny to confirm · n / Esc to cancel"
	return fillPane(body, w, h)
}

// renderPane is the toggled full-screen read-only view of blog.md.
func (m model) renderPane() string {
	_, _, paneH := m.paneDims()
	if m.renderContent == "" {
		return fillPane(dimStyle.Render("No entries in blog.md yet — commit some cards or write a note."), m.width, paneH)
	}
	return m.renderVP.View()
}

// listPane renders the right column: one line per pending card, oldest
// first, selected card highlighted.
func (m model) listPane(w, h int) string {
	var lines []string
	lines = append(lines, titleStyle.Render("Pending")+"  "+dimStyle.Render(fmtCount(len(m.cards))))
	if len(m.cards) == 0 {
		lines = append(lines, dimStyle.Render("no pending captures"))
		return fillPane(strings.Join(lines, "\n"), w, h)
	}
	for i, c := range m.cards {
		row := kindIcon(c.Kind) + " " + cardLabel(c) + " " + relTime(c.Created)
		if i == m.sel {
			lines = append(lines, clipWidth(selStyle.Render(row), w))
		} else {
			lines = append(lines, clipWidth(dimStyle.Render(row), w))
		}
	}
	return fillPane(strings.Join(lines, "\n"), w, h)
}

// footer renders the current-state keybindings plus any transient status
// (errors in red).
func (m model) footer() string {
	var lines []string
	if m.status != "" {
		if m.statusErr {
			lines = append(lines, redStyle.Render("⚠ "+m.status))
		} else {
			lines = append(lines, greenStyle.Render(m.status))
		}
	}
	lines = append(lines, footerStyle.Render(m.footerText()))
	return strings.Join(lines, "\n")
}

func (m model) footerText() string {
	switch m.st {
	case stateCaption:
		return "Ctrl+S save caption · Esc cancel"
	case stateNote:
		return "Ctrl+S save note · Esc cancel"
	case stateDiscard:
		return "y yes, discard permanently · n / Esc no"
	case stateRender:
		return "↑/↓ / PgUp / PgDn scroll · v / Esc back · q quit"
	case stateImage:
		return "Esc / v back · q quit"
	default:
		hint := "↑/↓ move · a append as-is · c caption · d discard · n note · v view blog · q quit"
		if len(m.cards) > 0 {
			if m.cards[m.sel].Kind == inventory.KindCode {
				hint += " · PgUp / PgDn scroll preview"
			} else {
				hint += " · Enter view image"
			}
		}
		return hint
	}
}

// fillPane pads content to a fixed w×h box so the layout never shifts when
// content changes (poll updates, shorter lists, ...).
func fillPane(s string, w, h int) string {
	lines := linesOf(s)
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i := range lines {
		if lipgloss.Width(lines[i]) < w {
			lines[i] += strings.Repeat(" ", w-lipgloss.Width(lines[i]))
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// clipWidth truncates a string to at most w display cells.
func clipWidth(s string, w int) string {
	if w < 0 {
		w = 0
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

func linesOf(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func fmtCount(n int) string {
	if n == 1 {
		return "1 pending"
	}
	return strconv.Itoa(n) + " pending"
}
