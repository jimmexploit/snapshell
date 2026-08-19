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
	return buildKittyEscapeID(path, rows, 1)
}

// buildKittyEscapeID is buildKittyEscape with an explicit image id; id == 0
// omits the i= field so kitty auto-assigns a fresh id per transmit. The
// inventory preview always reuses id 1 (one image on screen at a time); the
// blog render leaves it unset so several screenshots can be on screen at
// once without clobbering each other's data.
func buildKittyEscapeID(path string, rows int, id uint32) (string, error) {
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
	idPart := ""
	if id > 0 {
		idPart = fmt.Sprintf("i=%d,", id)
	}
	return fmt.Sprintf("\x1b_Ga=T,f=100,t=f,q=2,%sr=%d,C=1;%s\x1b\\", idPart, rows, encoded), nil
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

// kittyBlogEscape builds the escape for one blog screenshot with no explicit
// image id, so kitty assigns a fresh one per transmit (the blog page can
// show several screenshots at once, unlike the single inventory preview).
// The "b@" memo prefix keeps it distinct from the i=1 inventory escapes.
func kittyBlogEscape(path string, rows int) (string, error) {
	key := "b@" + path + "@" + strconv.Itoa(rows)
	imgMemo.mu.Lock()
	defer imgMemo.mu.Unlock()
	if imgMemo.key == key {
		return imgMemo.ansi, nil
	}
	ansi, err := buildKittyEscapeID(path, rows, 0)
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

// kittyFrameNoImage returns the escape that clears any image kitty is
// displaying, or "" when not running in kitty. Call it on every frame that
// does not display an image so a stale screenshot can't linger over other
// panes. The delete is emitted unconditionally (not gated on our own
// bookkeeping of what we last transmitted) with the explicit d=A form so a
// stale placement is cleared even if our transmit/display state ever falls
// out of sync with kitty's actual display; deleting when nothing is shown is
// a no-op.
func kittyFrameNoImage() string {
	if !kittyEnabled() {
		return ""
	}
	imgScreen.mu.Lock()
	defer imgScreen.mu.Unlock()
	imgScreen.key = ""
	return "\x1b_Ga=d,d=A,q=2\x1b\\"
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
