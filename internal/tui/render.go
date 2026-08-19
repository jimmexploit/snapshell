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

// blogImageBlock is one screenshot in the render layout: where in the
// viewport content its first (anchor) line sits, how many rows the block
// occupies, and the geometry needed to re-place it cropped while the page
// scrolls.
type blogImageBlock struct {
	line  int    // content line of the block's anchor line
	rows  int    // total rows the block occupies in the content
	rel   string // relative image path
	abs   string // absolute image path
	imgW  int    // image pixel width
	imgH  int    // image pixel height
	dispW int    // display width in cells (estimated, for alignment)
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
// joined with a blank line so the text never sits glued to an image. It also
// records where each image block sits in the content (m.renderImgBlocks) so
// the scroll-aware render can crop and re-place them per frame.
func (m *model) composeRender(md string, w int) string {
	segs := splitRenderSegs(md)
	if len(segs) == 0 {
		return ""
	}
	_, _, paneH := m.paneDims()
	var parts []string
	var blocks []blogImageBlock
	line := 0
	for i, seg := range segs {
		if i > 0 {
			line++ // the blank line separating adjacent parts
		}
		if seg.image != "" {
			blk, part := m.renderImageBlock(seg.image, w, paneH)
			blk.line = line
			line += blk.rows
			parts = append(parts, part)
			if blk.rows > 0 {
				blocks = append(blocks, blk)
			}
			continue
		}
		part := ""
		if m.renderer != nil {
			if out, err := m.renderer.Render(seg.text); err == nil {
				part = trimBlankLines(out)
			}
		}
		if part == "" {
			part = seg.text
		}
		parts = append(parts, part)
		line += len(linesOf(part))
	}
	m.renderImgBlocks = blocks
	return strings.Join(parts, "\n\n")
}

// renderImageBlock renders one image reference as a kitty graphics block
// sized to fit a w×h pane: a full-width transmit-escape line followed by
// blank full-width lines reserving the image's rows, so the diff renderer
// never erases the placement. The escape line is offset horizontally per
// [inventory].blog_image_align. Falls back to a dim path label when the image
// can't be rendered in-terminal (not in kitty, missing file, not PNG).
func (m model) renderImageBlock(rel string, w, h int) (blogImageBlock, string) {
	abs := rel
	if !filepath.IsAbs(rel) {
		abs = filepath.Join(m.dir, rel)
	}
	label := kittyPadLine(dimStyle.Render("[image: "+rel+"]"), w)
	rows := m.blogImageRows(abs, w, h)
	if rows <= 0 {
		return blogImageBlock{abs: abs}, label
	}
	esc, err := kittyBlogEscape(abs, rows)
	if err != nil {
		return blogImageBlock{abs: abs}, label
	}
	cfg, _, err := imageDecode(abs)
	if err != nil {
		return blogImageBlock{abs: abs}, label
	}
	dispW := blogImageWidth(rows, cfg.Width, cfg.Height, w)
	blk := blogImageBlock{rows: rows, rel: rel, abs: abs, imgW: cfg.Width, imgH: cfg.Height, dispW: dispW}
	pad := strings.Repeat(" ", w)
	lines := []string{kittyPadLine(strings.Repeat(" ", m.blogAlignLead(dispW))+esc, w)}
	for i := 0; i < rows-1; i++ {
		lines = append(lines, pad)
	}
	return blk, strings.Join(lines, "\n")
}

// blogImageWidth estimates how many terminal cells wide the image renders at
// the given row count, using the same cell ratio as the fit math (measured
// from the terminal, 0.5 as fallback). kitty derives the exact width from the
// (cropped) aspect ratio; this estimate only feeds horizontal alignment, so a
// cell or two of error just nudges centering.
func blogImageWidth(rows, imgW, imgH, paneW int) int {
	if rows < 1 || imgW < 1 || imgH < 1 {
		return 1
	}
	d := int(float64(rows) * float64(imgW) / float64(imgH) / cellRatio())
	if d < 1 {
		return 1
	}
	if d > paneW {
		return paneW
	}
	return d
}

// blogAlignLead returns how many leading spaces shift the blog screenshot
// right per [inventory].blog_image_align: the edge padding for "left", centered
// for "center", flush-right minus the edge padding for "right". An image wider
// than the pane stays at the left edge. The padding is skipped for "center"
// so a centered screenshot is never nudged off-center.
func (m model) blogAlignLead(dispW int) int {
	padding := m.opts.BlogImagePadding
	if padding < 0 {
		padding = 0
	}
	switch m.opts.BlogImageAlign {
	case "center":
		if d := (m.width - dispW) / 2; d > 0 {
			return d
		}
	case "right":
		if d := m.width - dispW - padding; d > 0 {
			return d
		}
	default:
		if padding > 0 {
			return padding
		}
	}
	return 0
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
	rows := kittyFitRowsRatio(cfg.Width, cfg.Height, w, h, cellRatio())
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
