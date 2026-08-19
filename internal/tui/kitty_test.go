package tui

import (
	"encoding/base64"
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

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writePNGAt(t, path, newRGBA(w, h))
}

// writePNGAt encodes img as a PNG at path (parent dir must exist).
func writePNGAt(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// newRGBA returns an image whose pixels vary, so the PNG isn't trivially
// compressible.
func newRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(rng.Intn(256)),
				G: uint8(rng.Intn(256)),
				B: uint8(rng.Intn(256)),
				A: 255,
			})
		}
	}
	return img
}

func TestKittyFitRows(t *testing.T) {
	// Wide image in a 60x30 pane fits rows capped by width.
	got := kittyFitRows(1200, 600, 60, 30)
	if got <= 0 || got > 30 {
		t.Fatalf("wide image rows = %d, want within pane", got)
	}
	if got > 60/2*600/1200+1 {
		t.Fatalf("wide image rows = %d, not capped by width", got)
	}
	// Square image uses the full pane height.
	if got := kittyFitRows(100, 100, 60, 20); got != 20 {
		t.Fatalf("square image rows = %d, want 20", got)
	}
	// Degenerate inputs produce no image.
	if got := kittyFitRows(0, 100, 60, 20); got != 0 {
		t.Fatalf("zero-width image rows = %d, want 0", got)
	}
}

func TestBuildKittyEscapeFileMedium(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.png")
	writePNGAt(t, path, newRGBA(400, 300))

	ansi, err := buildKittyEscape(path, 12)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ansi, "\x1b_G") {
		t.Fatalf("escape should start with ESC_G, got %q", ansi[:min(8, len(ansi))])
	}
	if !strings.HasSuffix(ansi, "\x1b\\") {
		t.Fatal("escape should be a single ST-terminated sequence")
	}

	// One escape, no chunking (file medium is a single tiny sequence).
	if strings.Count(ansi, "\x1b_G") != 1 {
		t.Fatalf("expected single chunk, got %q", ansi)
	}

	rest := strings.TrimPrefix(ansi, "\x1b_G")
	ctrl, payload, ok := strings.Cut(rest, ";")
	if !ok {
		t.Fatal("escape missing ';'")
	}
	payload = strings.TrimSuffix(payload, "\x1b\\")
	// Control data: transmit+display, PNG, file medium, fixed id, suppressed
	// responses, transient hint, rows target, no cursor move.
	wantCtrl := "a=T,f=100,t=f,q=2,i=1,r=12,C=1"
	if ctrl != wantCtrl {
		t.Fatalf("control data = %q, want %q", ctrl, wantCtrl)
	}

	// Payload is the base64 of the absolute path.
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != abs {
		t.Fatalf("payload decodes to %q, want %q", decoded, abs)
	}
}

func TestBuildKittyEscapeMissingFile(t *testing.T) {
	if _, err := buildKittyEscape("/nonexistent/nope.png", 5); err == nil {
		t.Fatal("missing file should error")
	}
}

func TestKittyFrameStateMachine(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	path := filepath.Join(t.TempDir(), "a.png")
	writePNG(t, path, 50, 50)

	// First frame shows the image → transmit escape.
	first := kittyFrameForImage(path, 10)
	if first == "" || !strings.HasPrefix(first, "\x1b_G") {
		t.Fatalf("first frame should transmit, got %q", first[:min(8, len(first))])
	}
	// Same image, same size -> re-transmit (the frame must always emit so the
	// diff renderer never has to erase the escape line).
	if again := kittyFrameForImage(path, 10); again != first {
		t.Fatal("unchanged image should re-transmit the same escape")
	}
	// Leaving the image → delete escape (emitted on every no-image frame, so
	// a stale placement can never linger even if our bookkeeping and kitty's
	// actual display ever drift apart).
	del := kittyFrameNoImage()
	if del != "\x1b_Ga=d,d=A,q=2\x1b\\" {
		t.Fatalf("delete escape = %q", del)
	}
	if again := kittyFrameNoImage(); again != del {
		t.Fatal("no-image frames should always carry the delete escape")
	}
	// Coming back re-transmits (memoized build is a no-op but the frame
	// must emit).
	if back := kittyFrameForImage(path, 10); back == "" {
		t.Fatal("returning to the image should re-transmit")
	}
}

func TestKittyFrameDisabled(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-256color")
	defer resetKittyState()
	// No kitty env: helpers return "" and never mutate state.
	if got := kittyFrameForImage("/whatever.png", 3); got != "" {
		t.Fatalf("disabled kittyFrameForImage = %q", got)
	}
	if got := kittyFrameNoImage(); got != "" {
		t.Fatalf("disabled kittyFrameNoImage = %q", got)
	}
}

func TestImageScaleShrinksRows(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	imgPath := filepath.Join(t.TempDir(), "attachments", "001.png")
	writePNG(t, imgPath, 100, 100) // square image: fit = full pane height

	cards := []inventory.Card{
		{ID: 1, Kind: inventory.KindImage, Path: "attachments/001.png", AbsPath: imgPath, Created: time.Now()},
	}
	m, _ := setupModel(t, cards)
	m = step(t, m, m.refreshList())
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})

	full := m.imageRows(cards[0])
	if full <= 0 {
		t.Fatal("full-size rows should be > 0 in kitty")
	}
	m.opts.ImageScale = 0.5
	if half := m.imageRows(cards[0]); half != int(float64(full)*0.5+0.5) {
		t.Fatalf("50%% scale rows = %d, want %d", half, int(float64(full)*0.5+0.5))
	}
	// Scale 100 (or unset) restores the full fit.
	m.opts.ImageScale = 1
	if got := m.imageRows(cards[0]); got != full {
		t.Fatalf("100%% scale rows = %d, want %d", got, full)
	}
	// A scale so small there's not even one row → unrenderable → external
	// fallback (0 rows).
	m.opts.ImageScale = 0.01
	if got := m.imageRows(cards[0]); got != 0 {
		t.Fatalf("1%% scale should be unrenderable, got %d rows", got)
	}
}

func TestKittyPadLine(t *testing.T) {
	esc := "\x1b_Gx;y\x1b\\"
	got := kittyPadLine(esc, 10)
	if len(got) != len(esc)+10 {
		t.Fatalf("padded line len = %d, want %d", len(got), len(esc)+10)
	}
	if !strings.HasSuffix(got, strings.Repeat(" ", 10)) {
		t.Fatalf("padded line should end in exactly %d spaces", 10)
	}
}
