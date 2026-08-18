package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// kittyEnabled reports whether the current terminal is kitty, the only
// terminal we emit the graphics protocol for. Everything else gets the
// plain-text image label instead.
func kittyEnabled() bool {
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	return strings.Contains(os.Getenv("TERM"), "kitty")
}

// kittyFitRows computes how many terminal rows the image should occupy so
// it fits inside a pane of w×h cells. Only the row count is sent to kitty
// (which derives the width from the image aspect ratio), so this estimate
// of the display width only exists to keep the image from spilling over the
// pane edge. cellRatio is the typical cell width/height (≈0.5).
func kittyFitRows(imgW, imgH, paneW, paneH int) int {
	if imgW <= 0 || imgH <= 0 || paneW <= 0 || paneH <= 0 {
		return 0
	}
	const cellRatio = 0.5
	rows := paneH
	if maxRowsByWidth := int(float64(paneW) * cellRatio * float64(imgH) / float64(imgW)); maxRowsByWidth < rows {
		rows = maxRowsByWidth
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

// buildKittyEscape renders the kitty graphics protocol sequence that
// transmits and displays the PNG at the current cursor position, scaled to
// rows terminal rows. The image itself is NOT streamed: kitty reads it from
// disk via the file transmission medium (t=f), so the escape is tiny and
// survives any connection, matching what `kitten icat` does by default.
// q=2 suppresses every response so the TUI's stdin stays clean.
func buildKittyEscape(path string, rows int) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(abs))
	if encoded == "" {
		return "", fmt.Errorf("empty path: %s", path)
	}
	return fmt.Sprintf("\x1b_Ga=T,f=100,t=f,q=2,i=1,r=%d,C=1;%s\x1b\\", rows, encoded), nil
}

// imgMemo caches the fully-built escape for a (path, rows) pair so a screen
// that keeps re-showing the same image doesn't re-read/re-encode the file.
var imgMemo = struct {
	mu   sync.Mutex
	key  string
	ansi string
}{}

func kittyImageEscape(path string, rows int) (string, error) {
	key := fmt.Sprintf("%s@%d", path, rows)
	imgMemo.mu.Lock()
	defer imgMemo.mu.Unlock()
	if imgMemo.key == key {
		return imgMemo.ansi, nil
	}
	ansi, err := buildKittyEscape(path, rows)
	if err != nil {
		return "", err
	}
	imgMemo.key = key
	imgMemo.ansi = ansi
	return ansi, nil
}

// imgScreen remembers which image is currently displayed on the terminal so
// the TUI emits the (expensive) transmit escape only when the image changes
// and a delete escape only when it leaves. It is package-level because the
// model's View() is a value receiver; one TUI per process makes this safe.
var imgScreen = struct {
	mu  sync.Mutex
	key string
}{}

// kittyFrameForImage returns the escape to emit this frame for the given
// image: a fresh transmit on every frame. bubbletea's diff renderer only
// re-writes a line whose bytes changed, so emitting the same escape every
// frame is free; deduplicating here would make line 2 flip from the escape
// to empty, forcing the renderer to erase it, which clears the image kitty
// already drew. Always emitting means any redraw of the pane region simply
// re-places the image. Not in kitty -> "" without touching state.
func kittyFrameForImage(path string, rows int) string {
	if !kittyEnabled() {
		return ""
	}
	key := fmt.Sprintf("%s@%d", path, rows)
	imgScreen.mu.Lock()
	defer imgScreen.mu.Unlock()
	ansi, err := kittyImageEscape(path, rows)
	if err != nil {
		imgScreen.key = ""
		if f := os.Getenv("SNAPSHELL_KITTY_DEBUG"); f != "" {
			os.WriteFile(f, []byte("err: "+err.Error()+"\n"), 0o644)
		}
		return ""
	}
	imgScreen.key = key
	if f := os.Getenv("SNAPSHELL_KITTY_DEBUG"); f != "" {
		os.WriteFile(f, []byte("emit rows="+strconv.Itoa(rows)+" path="+path+"\n"), 0o644)
	}
	return ansi
}

// kittyFrameNoImage returns the escape that clears the previously displayed
// image, or "" when nothing is on screen. Call it on any frame that does not
// display an image so a stale screenshot can't linger over other panes.
func kittyFrameNoImage() string {
	if !kittyEnabled() {
		return ""
	}
	imgScreen.mu.Lock()
	defer imgScreen.mu.Unlock()
	if imgScreen.key == "" {
		return ""
	}
	imgScreen.key = ""
	return "\x1b_Ga=d,q=2\x1b\\"
}

// resetKittyState clears the display/memo state (tests only).
func resetKittyState() {
	imgScreen.mu.Lock()
	imgScreen.key = ""
	imgScreen.mu.Unlock()
	imgMemo.mu.Lock()
	imgMemo.key = ""
	imgMemo.ansi = ""
	imgMemo.mu.Unlock()
}
