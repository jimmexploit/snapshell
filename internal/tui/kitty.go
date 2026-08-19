package tui

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
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
	return kittyFitRowsRatio(imgW, imgH, paneW, paneH, 0.5)
}

// kittyFitRowsRatio is kittyFitRows with an explicit cell width/height ratio;
// the blog render passes the measured cell ratio so its screenshots neither
// spill over the pane edge nor mis-center when the font's cells are wider
// than the ~0.5 guess.
func kittyFitRowsRatio(imgW, imgH, paneW, paneH int, cellRatio float64) int {
	if imgW <= 0 || imgH <= 0 || paneW <= 0 || paneH <= 0 || cellRatio <= 0 {
		return 0
	}
	rows := paneH
	if maxRowsByWidth := int(float64(paneW) * cellRatio * float64(imgH) / float64(imgW)); maxRowsByWidth < rows {
		rows = maxRowsByWidth
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

// cellSize caches the measured terminal cell width/height ratio, filled in
// once by queryCellSize() before the TUI starts. 0.5 is the fallback for a
// typical monospace cell when the terminal can't be queried.
var cellSize = struct {
	mu    sync.Mutex
	ratio float64
}{ratio: 0.5}

// cellRatio returns the measured cell width/height ratio.
func cellRatio() float64 {
	cellSize.mu.Lock()
	defer cellSize.mu.Unlock()
	return cellSize.ratio
}

func setCellRatio(r float64) {
	if r <= 0 {
		return
	}
	cellSize.mu.Lock()
	cellSize.ratio = r
	cellSize.mu.Unlock()
}

// queryCellSize measures the terminal's cell width/height ratio via the
// CSI 16 t query (cell size in pixels; kitty replies "CSI 6 ; H ; W t"). The
// blog alignment and fit math assume ~0.5 (a typical monospace cell); when
// the font's cells are wider, screenshots render slightly wider than that
// estimate, so a centered one drifts right and can clip at the pane edge.
// Measuring the real cell fixes both. It must run before bubbletea takes over
// stdin, and only when stdin is a terminal and the query will actually be
// answered (kitty, which sets KITTY_WINDOW_ID); any failure leaves the 0.5
// default. The read is time-boxed so a terminal that ignores the query can't
// hang the TUI.
func queryCellSize() {
	if !kittyEnabled() {
		return
	}
	f, err := os.Stdin.Stat()
	if err != nil || f.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return
	}
	defer term.Restore(fd, old)
	if _, err := os.Stdout.WriteString("\x1b[16t"); err != nil {
		return
	}
	resp := make(chan string, 1)
	go func() {
		s, err := bufio.NewReader(os.Stdin).ReadString('t')
		if err == nil {
			resp <- s
		}
	}()
	select {
	case s := <-resp:
		setCellRatio(parseCellSize(s))
	case <-time.After(300 * time.Millisecond):
	}
}

// parseCellSize extracts the width/height ratio from the CSI 16 t response
// "CSI 6 ; <height> ; <width> t" (the trailing t is the terminator byte the
// reader stopped at).
func parseCellSize(resp string) float64 {
	rest, ok := strings.CutPrefix(resp, "\x1b[6;")
	if !ok {
		return 0
	}
	rest = strings.TrimSuffix(rest, "t")
	parts := strings.Split(rest, ";")
	if len(parts) < 2 {
		return 0
	}
	h, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	w, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil || h <= 0 || w <= 0 {
		return 0
	}
	return w / h
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

// buildKittyEscapeCrop renders the kitty graphics sequence that transmits the
// PNG and displays only the vertical slice of its pixel rows [y, y+h) at the
// cursor, scaled to rows terminal rows. While the blog page scrolls, an image
// whose top has moved above the window is re-placed with those hidden rows
// cropped away, so the screenshot fades off the top edge one row at a time
// instead of vanishing whole. The width is derived by kitty from the cropped
// aspect ratio (only rows is sent), matching the no-crop escape's width.
func buildKittyEscapeCrop(path string, rows, y, h int) (string, error) {
	if rows < 1 || y < 0 || h < 1 {
		return "", fmt.Errorf("kitty crop escape: rows=%d y=%d h=%d out of range", rows, y, h)
	}
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
	return fmt.Sprintf("\x1b_Ga=T,f=100,t=f,q=2,r=%d,y=%d,h=%d,C=1;%s\x1b\\", rows, y, h, encoded), nil
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
