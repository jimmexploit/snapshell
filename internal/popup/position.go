package popup

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// position describes where to move the dialog window. Either a named
// preset (resolved against the current screen size) or explicit pixel
// coordinates from the screen's top-left corner.
type position struct {
	preset string // "" when explicit pixels are set
	x, y   int
}

// presets index a 3x3 grid: row = top/center/bottom, column =
// left/center/right.
var presets = []string{
	"top-left", "top-center", "top-right",
	"center-left", "center", "center-right",
	"bottom-left", "bottom-center", "bottom-right",
}

var presetIndex = func() map[string]int {
	m := make(map[string]int, len(presets))
	for i, p := range presets {
		m[p] = i
	}
	return m
}()

// parsePosition turns a config string into a position. Accepts a named
// preset or "X,Y" pixels (both non-negative).
func parsePosition(s string) (position, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return position{}, fmt.Errorf("empty popup position")
	}
	if i, ok := presetIndex[s]; ok {
		return position{preset: s, x: i}, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return position{}, fmt.Errorf("invalid popup position %q — use a preset (%s) or \"X,Y\" pixels", s, strings.Join(presets, ", "))
	}
	x, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return position{}, fmt.Errorf("invalid popup position %q: bad X coordinate", s)
	}
	y, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return position{}, fmt.Errorf("invalid popup position %q: bad Y coordinate", s)
	}
	if x < 0 || y < 0 {
		return position{}, fmt.Errorf("invalid popup position %q: coordinates must be non-negative", s)
	}
	return position{x: x, y: y}, nil
}

// resolve maps a preset to screen pixels. Explicit pixel positions pass
// through unchanged. winW/winH is the dialog's own size, needed to keep a
// preset on-screen.
func (p position) resolve(screenW, screenH, winW, winH int) (x, y int) {
	if p.preset == "" {
		return p.x, p.y
	}
	idx := p.x // preset index stashed in x by parsePosition
	row, col := idx/3, idx%3
	switch col {
	case 1:
		x = (screenW - winW) / 2
	case 2:
		x = screenW - winW
	}
	switch row {
	case 1:
		y = (screenH - winH) / 2
	case 2:
		y = screenH - winH
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

// moveDialog finds the zenity window by its title and slides it into the
// configured position. It is best-effort: runs in its own goroutine, polls
// for up to 5s for the window to map, and gives up silently if it never
// appears (the dialog itself is unaffected).
func moveDialog(title, posCfg string, winW, winH int) error {
	pos, err := parsePosition(posCfg)
	if err != nil {
		return err
	}

	geom, err := exec.Command("xdotool", "getdisplaygeometry").Output()
	if err != nil {
		return fmt.Errorf("query display geometry: %v", err)
	}
	var screenW, screenH int
	if f := strings.Fields(string(geom)); len(f) == 2 {
		screenW, _ = strconv.Atoi(f[0])
		screenH, _ = strconv.Atoi(f[1])
	}
	if screenW <= 0 || screenH <= 0 {
		return fmt.Errorf("unexpected xdotool getdisplaygeometry output %q", strings.TrimSpace(string(geom)))
	}
	x, y := pos.resolve(screenW, screenH, winW, winH)
	sx, sy := strconv.Itoa(x), strconv.Itoa(y)

	// The dialog takes a moment to map; poll until xdotool sees it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("xdotool", "search", "--name", title).Output()
		if err == nil {
			if wid := strings.Fields(string(out)); len(wid) > 0 {
				return settleAndMove(wid[0], sx, sy)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil // window never appeared; nothing to move
}

// settleAndMove slides the dialog into place, then checks it actually
// stuck: zenity may still be mapping/re-placing the window when we first
// move it, overwriting our coordinates. Re-moves until the reported
// position matches or the attempt times out. Best-effort.
func settleAndMove(id, sx, sy string) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := exec.Command("xdotool", "windowmove", id, sx, sy).Run(); err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
		px, py, err := windowPos(id)
		if err == nil && px == sx && py == sy {
			return nil
		}
		if time.Now().After(deadline) {
			return nil
		}
	}
}

// windowPos reads the current window position from
// `xdotool getwindowgeometry`, returned as the raw "X,Y" string fields.
func windowPos(id string) (x, y string, err error) {
	out, err := exec.Command("xdotool", "getwindowgeometry", id).Output()
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Position:") {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(line, "Position:"))
		if len(f) == 0 {
			break
		}
		xy := strings.Split(strings.TrimSuffix(f[0], ","), ",")
		if len(xy) == 2 {
			return xy[0], xy[1], nil
		}
	}
	return "", "", fmt.Errorf("no Position line in xdotool getwindowgeometry output %q", string(out))
}
