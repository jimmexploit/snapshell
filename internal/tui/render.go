// Blog render helpers: the read-only "view blog" page splits blog.md at
// screenshot references and renders them inline via the kitty graphics
// protocol, with the text between them rendered by glamour. Everything else
// in the TUI keeps rendering through glamour alone.

package tui

import (
	"path/filepath"
	"regexp"
	"strings"
)

// imageRefRe matches a standalone markdown image reference on its own line,
// the exact form blog.md's writer emits for screenshots.
var imageRefRe = regexp.MustCompile(`^!\[[^\]]*\]\(([^)\s]+)\)$`)

// renderSeg is one piece of the blog render: either a run of markdown text
// or a single image reference.
type renderSeg struct {
	text  string
	image string // relative image path when non-empty
}

// splitRenderSegs splits the markdown at standalone image references. glamour
// renders ![..](..) as a bare link line, so image refs are pulled out and
// rendered via the kitty protocol instead; the surrounding text is untouched
// and keeps its paragraph spacing.
func splitRenderSegs(md string) []renderSeg {
	var segs []renderSeg
	var text []string
	flush := func() {
		for len(text) > 0 && strings.TrimSpace(text[0]) == "" {
			text = text[1:]
		}
		for len(text) > 0 && strings.TrimSpace(text[len(text)-1]) == "" {
			text = text[:len(text)-1]
		}
		if len(text) > 0 {
			segs = append(segs, renderSeg{text: strings.Join(text, "\n")})
			text = nil
		}
	}
	for _, line := range strings.Split(md, "\n") {
		if m := imageRefRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			flush()
			segs = append(segs, renderSeg{image: m[1]})
			continue
		}
		text = append(text, line)
	}
	flush()
	return segs
}

// composeRender turns the raw blog.md into the render viewport content: text
// segments rendered with glamour and screenshot references rendered inline
// via the kitty graphics protocol, sized to fit the render pane. Segments are
// joined with a single newline.
func (m model) composeRender(md string, w int) string {
	segs := splitRenderSegs(md)
	if len(segs) == 0 {
		return ""
	}
	_, _, paneH := m.paneDims()
	var parts []string
	for _, seg := range segs {
		if seg.image != "" {
			parts = append(parts, m.renderImageBlock(seg.image, w, paneH))
			continue
		}
		if m.renderer != nil {
			if out, err := m.renderer.Render(seg.text); err == nil {
				parts = append(parts, trimBlankLines(out))
				continue
			}
		}
		parts = append(parts, seg.text)
	}
	return strings.Join(parts, "\n")
}

// renderImageBlock renders one image reference as a kitty graphics block
// sized to fit a w×h pane: a full-width transmit-escape line followed by
// blank full-width lines reserving the image's rows, so the diff renderer
// never erases the placement. Falls back to a dim path label when the image
// can't be rendered in-terminal (not in kitty, missing file, not PNG).
func (m model) renderImageBlock(rel string, w, h int) string {
	abs := rel
	if !filepath.IsAbs(rel) {
		abs = filepath.Join(m.dir, rel)
	}
	rows := m.blogImageRows(abs, w, h)
	if rows <= 0 {
		return kittyPadLine(dimStyle.Render("[image: "+rel+"]"), w)
	}
	esc, err := kittyBlogEscape(abs, rows)
	if err != nil {
		return kittyPadLine(dimStyle.Render("[image: "+rel+"]"), w)
	}
	pad := strings.Repeat(" ", w)
	lines := []string{kittyPadLine(esc, w)}
	for i := 0; i < rows-1; i++ {
		lines = append(lines, pad)
	}
	return strings.Join(lines, "\n")
}

// blogImageRows returns how many terminal rows the image at abs should
// occupy to fit a w×h pane, scaled by the [inventory].blog_image_scale_percent
// multiplier, or 0 when it can't be rendered in-terminal. Too small to fit
// even one row falls back to the label.
func (m model) blogImageRows(abs string, w, h int) int {
	if !kittyEnabled() {
		return 0
	}
	cfg, format, err := imageDecode(abs)
	if err != nil || format != "png" {
		return 0
	}
	rows := kittyFitRows(cfg.Width, cfg.Height, w, h)
	if scale := m.opts.BlogImageScale; scale > 0 && scale < 1 {
		rows = int(float64(rows)*scale + 0.5)
	}
	if rows < 1 {
		return 0
	}
	return rows
}

// trimBlankLines removes leading and trailing whitespace-only lines from
// glamour output so adjacent segments join without piling up blank rows.
func trimBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
