package popup

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// defaultTerminal is the preferred popup terminal when none is supplied by
// the caller (config resolves the real value in production).
const defaultTerminal = "alacritty"

// Spawn launches a floating terminal running
//
//	<bin> internal-popup --mode <mode> --file <file> --session-dir <dir>
//
// and returns once the terminal is launched — the daemon must not block on
// the user finishing the form (the popup process appends to blog.md itself
// and exits, closing the window).
//
// term is the terminal emulator binary resolved by config ("" lets Spawn
// resolve its own fallback, for standalone use). widthCells/heightCells
// are the configured [popup].width_cells/height_cells and are handed to
// the emulator as its window dimensions (columns/lines) plus used for
// pixel sizing in positionPopup.
func Spawn(selfBin, mode, file, sessionDir, term string, widthCells, heightCells int) error {
	if term == "" {
		resolved, err := resolveTerminal(defaultTerminal)
		if err != nil {
			return err
		}
		term = resolved
	}

	args := []string{}
	if term == "alacritty" || term == "kitty" {
		args = append(args, "--class", "snapshell-popup", "--title", "snapshell")
	} else if term == "xterm" {
		args = append(args, "-name", "snapshell-popup", "-T", "snapshell")
	}
	args = append(args, dimensionsFlags(term, widthCells, heightCells)...)
	args = append(args, "-e", selfBin, "internal-popup",
		"--mode", mode, "--file", file, "--session-dir", sessionDir)

	cmd := exec.Command(term, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch popup terminal %s: %v", term, err)
	}
	// Reap the child without blocking the daemon.
	go cmd.Wait()

	// Position the window in the background too: xdotool search polls for
	// the window to appear, which can take a moment — the daemon must not
	// block on it (a slow popup must never delay the next hotkey press).
	go positionPopup("snapshell-popup", widthCells, heightCells)
	return nil
}

// dimensionsFlags returns the per-emulator flags that set the window's
// size in cell columns×lines. Exact flag names vary by emulator, so this
// is popup's concern, not config's.
func dimensionsFlags(term string, cols, lines int) []string {
	if cols <= 0 || lines <= 0 {
		return nil
	}
	switch term {
	case "alacritty":
		return []string{"--dimensions", fmt.Sprintf("%dx%d", cols, lines)}
	case "kitty":
		return []string{
			"-o", fmt.Sprintf("window.dimensions.columns=%d", cols),
			"-o", fmt.Sprintf("window.dimensions.lines=%d", lines),
		}
	case "xterm":
		return []string{"-geometry", fmt.Sprintf("%dx%d", cols, lines)}
	}
	return nil
}

// resolveTerminal picks a terminal emulator that is actually installed.
func resolveTerminal(configured string) (string, error) {
	for _, name := range []string{configured, "alacritty", "kitty", "xterm"} {
		if name == "" {
			continue
		}
		if _, err := exec.LookPath(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("no popup terminal found on PATH (tried alacritty, kitty, xterm) — install one")
}

// positionPopup finds the spawned window by WM_CLASS and moves/resizes it
// toward the screen center. Best effort: missing xdotool (or a window that
// never appears) just leaves the popup where the terminal put it.
//
// widthCells/heightCells are the configured cell dimensions; a terminal
// cell is approximated at 8×16 px for the xdotool pixel math.
func positionPopup(class string, widthCells, heightCells int) {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return
	}
	winW, winH := 800, 480
	if widthCells > 0 && heightCells > 0 {
		winW = widthCells * 8
		winH = heightCells * 16
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("xdotool", "search", "--class", class).Output()
		if err == nil {
			if wins := strings.Fields(string(out)); len(wins) > 0 {
				w := wins[0]
				_ = exec.Command("xdotool", "windowsize", w, strconv.Itoa(winW), strconv.Itoa(winH)).Run()
				if geo, err := exec.Command("xdotool", "getdisplaygeometry").Output(); err == nil {
					var sw, sh int
					if _, err := fmt.Sscanf(string(geo), "%d %d", &sw, &sh); err == nil && sw > 0 && sh > 0 {
						x, y := (sw-winW)/2, (sh-winH)/2
						if x < 0 {
							x = 0
						}
						if y < 0 {
							y = 0
						}
						_ = exec.Command("xdotool", "windowmove", w, strconv.Itoa(x), strconv.Itoa(y)).Run()
					}
				}
				// Float over whatever has focus (the browser, a shell, ...).
				_ = exec.Command("xdotool", "windowactivate", w).Run()
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}
