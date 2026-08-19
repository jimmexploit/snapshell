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
		return kittyFrameNoImage() + m.imageViewPane() + "\n" + m.footer()
	}
	if m.st == stateNote {
		return kittyFrameNoImage() + m.notePane() + "\n" + m.footer()
	}
	detailW, listW, paneH := m.paneDims()
	detail := m.detailPane(detailW, paneH)
	list := m.listPane(listW, paneH)
	// The delete escape always leads the frame — even when this frame shows
	// an image — so a screenshot from a previous state (e.g. the blog render,
	// which can place several at once) can never linger. Every frame that
	// shows an image re-transmits it (kittyFrameForImage / inlineImagePane),
	// so the delete-then-replace within one frame is invisible.
	return kittyFrameNoImage() + lipgloss.JoinHorizontal(lipgloss.Top, detail, list) + "\n" + m.footer()
}

// showsImage reports whether the current view displays a screenshot via the
// kitty graphics protocol: the full-screen stateImage view always does, and
// the browse/caption detail panes do when inline rendering is active for
// the selected image card. It is informational — the frame always leads with
// the delete escape regardless, and image-showing panes re-transmit on every
// frame.
func (m model) showsImage() bool {
	if m.st == stateImage {
		return true
	}
	if m.opts.ImageRender == "inline" && (m.st == stateBrowse || m.st == stateCaption) {
		if len(m.cards) > 0 && m.cards[m.sel].Kind == inventory.KindImage {
			detailW, _, paneH := m.paneDims()
			return m.imageRenderInline(m.cards[m.sel], detailW, paneH)
		}
	}
	return false
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
		if m.imageRenderInline(c, w, h-2) {
			// Inline render: the screenshot is the preview.
			return header + "\n\n" + m.inlineImagePane(c, w, h-2)
		}
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
	_, _, paneH := m.paneDims()
	return m.imageRowsInPane(c, m.width, paneH, m.opts.ImageScale)
}

// imageRowsInPane returns how many terminal rows the image should occupy to
// fit a w×h pane at the given scale multiplier, or 0 when it can't be
// rendered in-terminal (not running in kitty, missing file, not PNG, or too
// small to fit).
func (m model) imageRowsInPane(c inventory.Card, w, h int, scale float64) int {
	dbg := os.Getenv("SNAPSHELL_KITTY_DEBUG")
	logf := func(format string, a ...any) {
		if dbg != "" {
			os.WriteFile(dbg, []byte(fmt.Sprintf(format, a...)+"\n"), 0o644)
		}
	}
	if c.AbsPath == "" {
		logf("imageRowsInPane: AbsPath empty")
		return 0
	}
	if !kittyEnabled() {
		logf("imageRowsInPane: kitty disabled (TERM=%q KITTY_WINDOW_ID=%q)", os.Getenv("TERM"), os.Getenv("KITTY_WINDOW_ID"))
		return 0
	}
	cfg, format, err := imageDecode(c.AbsPath)
	if err != nil {
		logf("imageRowsInPane: imageDecode error: %v", err)
		return 0
	}
	if format != "png" {
		logf("imageRowsInPane: format is %q, want png", format)
		return 0
	}
	rows := kittyFitRows(cfg.Width, cfg.Height, w, h)
	if rows > 0 && scale < 1 {
		// [inventory].image_scale_percent: render the image proportionally
		// smaller than the pane fit (aspect ratio preserved by kitty
		// deriving the width from the rows). Too small to fit even one row
		// falls back to the external viewer.
		rows = int(float64(rows)*scale + 0.5)
		if rows < 1 {
			rows = 0
		}
	}
	if rows <= 0 {
		logf("imageRowsInPane: kittyFitRows=0 (w=%d h=%d img=%dx%d)", w, h, cfg.Width, cfg.Height)
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

// imageRenderInline reports whether the [inventory].image_render=inline
// preview is active and can render the given card in-terminal inside a w×h
// pane (kitty mode only; the external viewer path is untouched).
func (m model) imageRenderInline(c inventory.Card, w, h int) bool {
	return m.opts.ImageRender == "inline" && m.imageMode() == "kitty" && m.inlineImageRows(c, w, h) > 0
}

// inlineImageRows returns how many terminal rows the inline preview image
// should occupy in a w×h pane (default half the fit, hard-capped at 65%),
// or 0 when it can't be rendered in-terminal.
func (m model) inlineImageRows(c inventory.Card, w, h int) int {
	return m.imageRowsInPane(c, w, h, m.opts.ImageInlineScale)
}

// inlineImagePane renders the inline screenshot in the detail pane: the
// kitty graphics escape on its own line, then blank lines reserving the
// image's rows, all padded to the pane width so the renderer never erases
// the placement. Falls back to a plain label when the image can't be
// rendered in-terminal.
func (m model) inlineImagePane(c inventory.Card, w, h int) string {
	rows := m.inlineImageRows(c, w, h)
	if rows <= 0 {
		return fillPane(imageLabel(c)+"\n\n"+dimStyle.Render("Press Enter to view"), w, h)
	}
	pad := strings.Repeat(" ", w)
	lines := []string{kittyPadLine(kittyFrameForImage(c.AbsPath, rows), w)}
	for i := 0; i < rows-1; i++ {
		lines = append(lines, pad)
	}
	for len(lines) < h {
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
	if m.imageRenderInline(c, w, h) {
		// Inline render: the screenshot is the preview, so keep it on screen
		// while captioning instead of swapping in the text preview — there's
		// nothing meaningful to preview for an image. The label stays so the
		// user understands this area is the live preview.
		lines = append(lines, titleStyle.Render("Preview"))
		avail := h - len(lines) - 1 // title, textarea, blank, label + breathing room
		if avail < 1 {
			avail = 1
		}
		lines = append(lines, m.inlineImagePane(c, w, avail))
		return fillPane(strings.Join(lines, "\n"), w, h)
	}
	lines = append(lines, titleStyle.Render("Preview"))
	lines = append(lines, m.mdPreview(m.captionPreviewText(c), w))
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
		lines = append(lines, m.mdPreview(m.notePreview, m.width))
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
	out := m.renderVP.View()
	if len(m.renderImgBlocks) == 0 {
		return out
	}
	return m.patchRenderImages(out, m.renderVP.YOffset, paneH)
}

// patchRenderImages rewrites the viewport lines that carry each image block
// with a per-frame transmit escape so a screenshot that is only partially in
// view is cropped to the visible part. The render frame deletes every kitty
// placement first, so a scrolled image must be re-placed every frame; cropping
// away the hidden rows lets the top fade off one row at a time instead of the
// whole image vanishing the moment its anchor line scrolls above the window.
// It also clamps an image whose bottom would spill over the pane (covering the
// footer) to the rows actually visible.
func (m model) patchRenderImages(out string, offset, paneH int) string {
	if !kittyEnabled() {
		return out
	}
	lines := strings.Split(out, "\n")
	for _, blk := range m.renderImgBlocks {
		relTop := blk.line - offset
		relBot := relTop + blk.rows
		if relBot <= 0 || relTop >= paneH {
			continue // scrolled entirely out of view
		}
		skipTop, skipBot := 0, 0
		if relTop < 0 {
			skipTop = -relTop
		}
		if relBot > paneH {
			skipBot = relBot - paneH
		}
		visRows := blk.rows - skipTop - skipBot
		if visRows < 1 {
			continue
		}
		cropY := skipTop * blk.imgH / blk.rows
		cropH := visRows * blk.imgH / blk.rows
		if cropH < 1 {
			cropH = 1
		}
		esc, err := buildKittyEscapeCrop(blk.abs, visRows, cropY, cropH)
		if err != nil {
			continue
		}
		// The image is anchored to the first visible row of its block: the
		// block's anchor line while still on screen, pane row 0 once its top
		// has scrolled above the window.
		idx := 0
		if relTop >= 0 {
			idx = relTop
		}
		if idx < len(lines) {
			lines[idx] = kittyPadLine(strings.Repeat(" ", m.blogAlignLead(blk.dispW))+esc, m.width)
		}
	}
	return strings.Join(lines, "\n")
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
		return fmt.Sprintf("%s save caption · %s cancel", keyLabel(m.keys.Submit), keyLabel(m.keys.Cancel))
	case stateNote:
		return fmt.Sprintf("%s save note · %s cancel", keyLabel(m.keys.Submit), keyLabel(m.keys.Cancel))
	case stateDiscard:
		return fmt.Sprintf("%s yes, discard permanently · %s / %s no", keyLabel(m.keys.Confirm), keyLabel(m.keys.Decline), keyLabel(m.keys.Cancel))
	case stateRender:
		return fmt.Sprintf("%s/%s/%s/%s scroll · %s/%s back · %s quit",
			keyLabel(m.keys.Up), keyLabel(m.keys.Down), keyLabel(m.keys.PageUp), keyLabel(m.keys.PageDown),
			keyLabel(m.keys.Blog), keyLabel(m.keys.Cancel), keyLabel(m.keys.Quit))
	case stateImage:
		return fmt.Sprintf("%s/%s back · %s quit", keyLabel(m.keys.Blog), keyLabel(m.keys.Cancel), keyLabel(m.keys.Quit))
	default:
		hint := fmt.Sprintf("%s move · %s append as-is · %s caption · %s discard · %s note · %s view blog · %s quit",
			keyLabel(m.keys.Up), keyLabel(m.keys.Append), keyLabel(m.keys.Caption), keyLabel(m.keys.Discard),
			keyLabel(m.keys.Note), keyLabel(m.keys.Blog), keyLabel(m.keys.Quit))
		if len(m.cards) > 0 {
			if m.cards[m.sel].Kind == inventory.KindCode {
				hint += fmt.Sprintf(" · %s/%s scroll preview", keyLabel(m.keys.PageUp), keyLabel(m.keys.PageDown))
			} else {
				hint += fmt.Sprintf(" · %s view image", keyLabel(m.keys.Open))
			}
		}
		return hint
	}
}

// keyLabel renders a binding list for the footer ("↑/k", "q/ctrl+c",
// "PgUp/PgDn", ...). Keys are shown as the terminal reports them, prettified.
func keyLabel(list []string) string {
	labels := make([]string, 0, len(list))
	for _, k := range list {
		labels = append(labels, keyLabelOne(k))
	}
	return strings.Join(labels, "/")
}

func keyLabelOne(k string) string {
	switch k {
	case "up":
		return "↑"
	case "down":
		return "↓"
	case "left":
		return "←"
	case "right":
		return "→"
	case "esc":
		return "Esc"
	case "enter":
		return "Enter"
	case "pgup":
		return "PgUp"
	case "pgdown":
		return "PgDn"
	case "space":
		return "Space"
	}
	parts := strings.Split(k, "+")
	for i, p := range parts {
		parts[i] = keyTitle(p)
	}
	return strings.Join(parts, "+")
}

// keyTitle capitalizes a single key name for display ("ctrl" -> "Ctrl",
// "s" -> "S", "f5" -> "F5").
func keyTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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

// wrapText soft-wraps s to at most w display cells per line, preserving
// existing line breaks. Words longer than w are hard-split, so no output
// line can ever exceed w — an unwrapped line in a side-by-side column would
// otherwise widen the column and push its sibling off screen.
func wrapText(s string, w int) string {
	if w < 1 {
		w = 1
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		out = append(out, wrapLine(line, w)...)
	}
	return strings.Join(out, "\n")
}

// wrapLine wraps a single newline-free line to at most w cells.
func wrapLine(line string, w int) []string {
	if lipgloss.Width(line) <= w {
		return []string{line}
	}
	var lines []string
	var cur []rune
	curW := 0
	flush := func() {
		lines = append(lines, string(cur))
		cur, curW = nil, 0
	}
	for _, word := range strings.Fields(line) {
		wordR := []rune(word)
		ww := lipgloss.Width(word)
		if curW > 0 {
			if curW+1+ww <= w {
				cur = append(cur, ' ')
				cur = append(cur, wordR...)
				curW += 1 + ww
				continue
			}
			flush()
		}
		// The word doesn't fit on an empty line; hard-split it.
		for lipgloss.Width(string(wordR)) > w {
			i := 0
			pw := 0
			for i < len(wordR) && pw+lipgloss.Width(string(wordR[i])) <= w {
				pw += lipgloss.Width(string(wordR[i]))
				i++
			}
			if i == 0 {
				i = 1
			}
			lines = append(lines, string(wordR[:i]))
			wordR = wordR[i:]
		}
		if len(wordR) > 0 {
			cur = append(cur, wordR...)
			curW = lipgloss.Width(string(wordR))
		}
	}
	if len(cur) > 0 {
		flush()
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
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
