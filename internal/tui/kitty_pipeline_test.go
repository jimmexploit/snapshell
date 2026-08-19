package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"snapshell/internal/inventory"
)

// TestKittyEscapeThroughRealRenderer drives the real bubbletea program into
// the image-selected state and captures what the standard renderer actually
// writes to the terminal, verifying the kitty graphics escape arrives intact.
func TestKittyEscapeThroughRealRenderer(t *testing.T) {
	if !kittyEnabled() {
		t.Skip("not in kitty")
	}
	dir := t.TempDir()
	pngPath := filepath.Join(dir, "big.png")
	writeBigPNG(t, pngPath, 1600, 1200)

	client := &fakeClient{listRes: ListResult{
		Dir: dir,
		Cards: []inventory.Card{
			{ID: 1, Kind: inventory.KindCode, Text: "whoami\njimmex", Created: time.Now().Add(-2 * time.Minute)},
			{ID: 2, Kind: inventory.KindImage, Path: "attachments/big.png", AbsPath: pngPath, Created: time.Now()},
		},
	}}
	m := newModel(Options{Client: client})
	out := &bytes.Buffer{}

	// First, check the pre-renderer View() output in isolation: browsing an
	// image card shows a plain label, Enter switches to the full-screen view
	// that carries the escape.
	{
		m2, _ := upd(t, m, tea.WindowSizeMsg{Width: 140, Height: 50})
		lmsg := m2.refreshList()()
		m3, _ := upd(t, m2, lmsg)
		m4, _ := upd(t, m3, tea.KeyMsg{Type: tea.KeyDown})
		if view := m4.View(); bytes.Contains([]byte(view), []byte("\x1b_Ga=T")) {
			t.Fatalf("browse view should not transmit the image inline in tab mode:\n%q", view)
		}
		m5, _ := upd(t, m4, tea.KeyMsg{Type: tea.KeyEnter})
		if m5.st != stateImage {
			t.Fatalf("Enter should enter stateImage, got %d", m5.st)
		}
		view := m5.View()
		t.Logf("view len=%d; first bytes: %q", len(view), truncated([]byte(view), 200))
		checkEscape(t, []byte(view), "View()")
	}

	p := tea.NewProgram(m, tea.WithInput(strings.NewReader("")), tea.WithOutput(out), tea.WithAltScreen(), tea.WithoutSignals())

	go func() {
		time.Sleep(100 * time.Millisecond)
		p.Send(tea.WindowSizeMsg{Width: 140, Height: 50})
		time.Sleep(100 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeyDown}) // select the image card
		time.Sleep(200 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeyEnter}) // full-screen image view
		time.Sleep(300 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	}()
	_, err := p.Run()
	if err != nil {
		t.Fatalf("program: %v", err)
	}

	data := out.Bytes()
	// The escape must be contiguous: walk all chunks from the first \x1b_G
	// until the final m=0 chunk's ST terminator, requiring no newline inside
	// (a newline would corrupt kitty's APC payload).
	checkEscape(t, data, "renderer output")

	if os.Getenv("SNAPSHELL_DUMP_RENDER") != "" {
		if err := os.WriteFile("/tmp/snapshell_render.bin", data, 0o644); err != nil {
			t.Logf("dump: %v", err)
		}
	}
}

func writeBigPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(7))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func truncated(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// checkEscape verifies a single kitty file-medium graphics escape appears in
// data intact: well-formed control data, ST-terminated, no newlines inside.
func checkEscape(t *testing.T, data []byte, label string) {
	t.Helper()
	firstG := bytes.Index(data, []byte("\x1b_Ga=T"))
	if firstG < 0 {
		t.Fatalf("%s: no graphics escape emitted", label)
	}
	semi := bytes.Index(data[firstG:], []byte(";"))
	if semi < 0 {
		t.Fatalf("%s: escape has no ';'", label)
	}
	header := string(data[firstG+3 : firstG+semi])
	t.Logf("%s: control data: %s", label, header)
	if !strings.HasPrefix(header, "a=T,f=100,t=f,q=2,i=1,r=") {
		t.Fatalf("%s: unexpected control data: %q", label, header)
	}
	st := bytes.Index(data[firstG+semi:], []byte("\x1b\\"))
	if st < 0 {
		t.Fatalf("%s: escape not ST-terminated", label)
	}
	esc := data[firstG : firstG+semi+st+2]
	if bytes.Contains(esc, []byte("\n")) {
		t.Fatalf("%s: escape contains newline", label)
	}
	if bytes.Count(esc, []byte("\x1b_G")) != 1 {
		t.Fatalf("%s: expected single-chunk escape, got %d",
			label, bytes.Count(esc, []byte("\x1b_G")))
	}
	t.Logf("%s: escape intact: %d bytes", label, len(esc))
}
