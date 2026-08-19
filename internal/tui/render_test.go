package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"snapshell/internal/inventory"
)

func TestSplitRenderSegs(t *testing.T) {
	md := "# Recon\n\n- scanned port 80\n\n![](attachments/001.png)\n\n- checked the banner\n\n![](attachments/002.png)"
	segs := splitRenderSegs(md)
	if len(segs) != 4 {
		t.Fatalf("got %d segments, want 4 (text/image/text/image): %+v", len(segs), segs)
	}
	if segs[0].text == "" || segs[0].image != "" {
		t.Fatalf("segment 0 should be text: %+v", segs[0])
	}
	if segs[1].text != "" || segs[1].image != "attachments/001.png" {
		t.Fatalf("segment 1 should be the first image: %+v", segs[1])
	}
	if segs[3].image != "attachments/002.png" {
		t.Fatalf("segment 3 should be the second image: %+v", segs[3])
	}

	// Text with no images is a single segment.
	plain := splitRenderSegs("just\n\ntext")
	if len(plain) != 1 || plain[0].image != "" || !strings.Contains(plain[0].text, "just") {
		t.Fatalf("plain text should be one text segment: %+v", plain)
	}

	// An image reference not on its own line (inline in a paragraph) is
	// left in the text, since blog.md never emits it that way.
	inline := splitRenderSegs("see ![icon](attachments/icon.png) here")
	if len(inline) != 1 || inline[0].image != "" {
		t.Fatalf("inline image refs should stay in text: %+v", inline)
	}
}

func TestKittyBlogEscapeNoImageID(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	path := filepath.Join(t.TempDir(), "a.png")
	writePNG(t, path, 50, 40)

	esc, err := kittyBlogEscape(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	rest := strings.TrimPrefix(esc, "\x1b_G")
	ctrl, _, ok := strings.Cut(rest, ";")
	if !ok {
		t.Fatalf("escape missing ';': %q", esc)
	}
	if !strings.HasPrefix(ctrl, "a=T,f=100,t=f,q=2,r=10,C=1") {
		t.Fatalf("unexpected control data: %q", ctrl)
	}
	if strings.Contains(ctrl, "i=") {
		t.Fatalf("blog escape must not pin an image id so several screenshots coexist: %q", ctrl)
	}
	if !strings.HasSuffix(esc, "\x1b\\") {
		t.Fatalf("escape not ST-terminated: %q", esc)
	}
}

func TestComposeRenderImagesInline(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "attachments", "001.png")
	writePNG(t, imgPath, 100, 80)

	client := &fakeClient{listRes: ListResult{Dir: dir, Cards: []inventory.Card{}}}
	m := newModel(Options{Client: client})
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 60})
	m, _ = upd(t, m, listMsg{dir: dir, cards: []inventory.Card{}})

	md := "# Recon\n\n- scanned port 80\n\n![](attachments/001.png)\n\n- checked the banner"
	m, _ = upd(t, m, renderMsg{content: md, width: 100})

	content := m.renderVP.View()
	if !strings.Contains(content, "\x1b_Ga=T,f=100,t=f,q=2,r=") {
		t.Fatalf("render view should transmit the screenshot inline:\n%q", content)
	}
	if strings.Contains(content, "![](") {
		t.Fatalf("render view should not keep the raw image reference:\n%q", content)
	}
	if !strings.Contains(content, "•") {
		t.Fatalf("render view should keep glamour bullets in the text:\n%q", content)
	}
	// A blank line separates the image from the text above and below it, so
	// the screenshot never sits glued to the surrounding paragraphs. Checked
	// on the raw composed content (the viewport pads blank lines to width).
	raw := m.composeRender(md, 100)
	if !strings.Contains(raw, "\n\n\x1b_G") {
		t.Fatalf("composed content should put a blank line before the image:\n%q", raw)
	}
	lines := strings.Split(content, "\n")
	escIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "\x1b_G") {
			escIdx = i
			break
		}
	}
	if len(m.renderImgBlocks) == 0 {
		t.Fatal("render should record the image block layout")
	}
	if escIdx >= 0 {
		// The line after the block's last pad row is the blank separator.
		next := escIdx + m.renderImgBlocks[0].rows
		if next >= len(lines) || strings.TrimSpace(lines[next]) != "" {
			t.Fatalf("expected a blank line after the image block:\n%q", content)
		}
	}
	// The full frame also clears stale images before redrawing.
	if view := m.View(); !strings.Contains(view, "\x1b_Ga=d,d=A,q=2\x1b\\") {
		t.Fatalf("render frame should delete stale images:\n%q", view)
	}
}

func TestBlogImageRowsScale(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	imgPath := filepath.Join(t.TempDir(), "attachments", "001.png")
	writePNG(t, imgPath, 100, 100) // square: fit = full pane height

	m := newModel(Options{Client: &fakeClient{}})
	full := m.blogImageRows(imgPath, 100, 40)
	if full <= 0 {
		t.Fatal("full blog rows should be > 0 in kitty")
	}
	// The scale multiplier applies linearly to the pane fit.
	m.opts.BlogImageScale = 0.5
	if half := m.blogImageRows(imgPath, 100, 40); half != int(float64(full)*0.5+0.5) {
		t.Fatalf("50%% blog rows = %d, want %d", half, int(float64(full)*0.5+0.5))
	}
	// A scale so small it fits no row falls back to the dim label.
	m.opts.BlogImageScale = 0.001
	if m.blogImageRows(imgPath, 100, 40) != 0 {
		t.Fatal("tiny scale should render no rows (label fallback)")
	}
}

func TestRenderImageBlockFallback(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	dir := t.TempDir()
	m := newModel(Options{Client: &fakeClient{}})
	m.dir = dir

	// Missing file -> dim label, no escape.
	if blk, got := m.renderImageBlock("attachments/nope.png", 100, 40); strings.Contains(got, "\x1b_G") {
		t.Fatalf("missing file should fall back to a label:\n%q", got)
	} else if !strings.Contains(got, "[image: attachments/nope.png]") {
		t.Fatalf("missing file should show the dim label:\n%q", got)
	} else if blk.rows != 0 {
		t.Fatalf("missing file block should have no rows, got %d", blk.rows)
	}

	// Non-PNG ref -> label too.
	bad := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(bad, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if blk, got := m.renderImageBlock("note.txt", 100, 40); strings.Contains(got, "\x1b_G") {
		t.Fatalf("non-PNG should fall back to a label:\n%q", got)
	} else if blk.rows != 0 {
		t.Fatalf("non-PNG block should have no rows, got %d", blk.rows)
	}
}

func TestPatchRenderImagesScrollFade(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "attachments", "001.png")
	writePNG(t, imgPath, 100, 100)

	m := newModel(Options{Client: &fakeClient{}})
	m.dir = dir
	m.width = 100

	blk := blogImageBlock{line: 2, rows: 20, abs: imgPath, imgW: 100, imgH: 100, dispW: 40}

	// Bottom cut: the anchor line is still on screen but the image would
	// spill past the pane (over the footer); clamp to the visible rows.
	m.renderImgBlocks = []blogImageBlock{blk}
	out := m.patchRenderImages(strings.Repeat("\n", 20), 0, 20)
	if line := strings.Split(out, "\n")[2]; !strings.Contains(line, "r=18,y=0,h=90,C=1") {
		t.Fatalf("bottom-cut escape should clamp to the 18 visible rows:\n%q", line)
	}

	// Top cut: the anchor line has scrolled above the window; the first
	// visible row (pane row 0) must carry a crop escape hiding the rows that
	// scrolled out, so the image fades off instead of vanishing.
	out = m.patchRenderImages(strings.Repeat("\n", 20), 5, 20)
	if line := strings.Split(out, "\n")[0]; !strings.Contains(line, "r=17,y=15,h=85,C=1") {
		t.Fatalf("top-cut escape should crop the 3 hidden rows:\n%q", line)
	}

	// Fully visible: no crop, the whole image re-placed on its anchor line.
	blk.rows = 10
	blk.dispW = 20
	m.renderImgBlocks = []blogImageBlock{blk}
	out = m.patchRenderImages(strings.Repeat("\n", 20), 0, 20)
	if line := strings.Split(out, "\n")[2]; !strings.Contains(line, "r=10,y=0,h=100,C=1") {
		t.Fatalf("full-image escape should show all rows:\n%q", line)
	}

	// Scrolled entirely past the image: nothing placed.
	out = m.patchRenderImages(strings.Repeat("\n", 20), 15, 20)
	if strings.Contains(out, "\x1b_Ga=T") {
		t.Fatalf("scrolled-past image should not be placed:\n%q", out)
	}

	// Not in kitty: no placement escapes at all.
	blk.rows = 20
	m.renderImgBlocks = []blogImageBlock{blk}
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-256color")
	out = m.patchRenderImages(strings.Repeat("\n", 20), 5, 20)
	if strings.Contains(out, "\x1b_Ga=T") {
		t.Fatalf("non-kitty render should not emit placement escapes:\n%q", out)
	}
}

func TestBlogImageAlign(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "001.png")
	writePNG(t, imgPath, 100, 100)

	// 100x100 image in a 100x40 pane fits at 40 rows -> 80 display cells.
	m := newModel(Options{Client: &fakeClient{}, BlogImageAlign: "center"})
	m.dir = dir
	m.width = 100
	blk, part := m.renderImageBlock("001.png", 100, 40)
	if blk.dispW != 80 {
		t.Fatalf("dispW = %d, want 80", blk.dispW)
	}
	first := strings.Split(part, "\n")[0]
	// Center is never nudged by padding: lead = (100-80)/2 = 10.
	if lead := strings.Index(first, "\x1b_G"); lead != 10 {
		t.Fatalf("centered escape should start at column 10, got %d:\n%q", lead, first)
	}

	// Right align keeps the default 2-cell edge padding.
	m.opts.BlogImagePadding = 2
	m.opts.BlogImageAlign = "right"
	_, part = m.renderImageBlock("001.png", 100, 40)
	if lead := strings.Index(strings.Split(part, "\n")[0], "\x1b_G"); lead != 18 {
		t.Fatalf("right-aligned escape should start at column 18 (100-80-2), got %d", lead)
	}

	// Left align keeps the default 2-cell edge padding.
	m.opts.BlogImageAlign = "left"
	_, part = m.renderImageBlock("001.png", 100, 40)
	if lead := strings.Index(strings.Split(part, "\n")[0], "\x1b_G"); lead != 2 {
		t.Fatalf("left-aligned escape should start at column 2, got %d", lead)
	}

	// An explicit 0 padding puts it flush.
	m.opts.BlogImagePadding = 0
	_, part = m.renderImageBlock("001.png", 100, 40)
	if lead := strings.Index(strings.Split(part, "\n")[0], "\x1b_G"); lead != 0 {
		t.Fatalf("left-aligned flush escape should start at column 0, got %d", lead)
	}
}
