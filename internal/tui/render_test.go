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
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
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
	if got := m.renderImageBlock("attachments/nope.png", 100, 40); strings.Contains(got, "\x1b_G") {
		t.Fatalf("missing file should fall back to a label:\n%q", got)
	} else if !strings.Contains(got, "[image: attachments/nope.png]") {
		t.Fatalf("missing file should show the dim label:\n%q", got)
	}

	// Non-PNG ref -> label too.
	bad := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(bad, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := m.renderImageBlock("note.txt", 100, 40); strings.Contains(got, "\x1b_G") {
		t.Fatalf("non-PNG should fall back to a label:\n%q", got)
	}
}
