package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"snapshell/internal/inventory"
)

// openImageCmd returns a command that opens a screenshot in the external
// viewer (spawning the process) and schedules a best-effort auto-close.
func (m model) openImageCmd(c inventory.Card) tea.Cmd {
	return func() tea.Msg {
		if c.AbsPath == "" {
			return openErrMsg{err: "image file path unknown"}
		}
		if err := openImage(m.opts.ImageViewer, m.opts.CloseDelay, c.AbsPath); err != nil {
			return openErrMsg{err: err.Error()}
		}
		return nil
	}
}

// openImage launches the configured image viewer (or xdg-open by default)
// on the given file. The process is spawned fresh; after delay a goroutine
// tries to close it. Some default viewers hand off to an already-running
// background instance instead of spawning a killable process, in which case
// the auto-close won't actually happen — that's an acceptable limitation,
// not something to engineer around.
func openImage(configured string, delay time.Duration, abs string) error {
	bin := strings.TrimSpace(configured)
	if bin == "" {
		bin = "xdg-open"
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("no image viewer configured and xdg-open not found on PATH")
		}
	} else if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("image viewer %q not found on PATH", bin)
	}

	cmd := exec.Command(bin, abs)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open image with %s: %v", bin, err)
	}

	if delay <= 0 {
		delay = 5 * time.Second
	}
	go func() {
		time.Sleep(delay)
		// Best-effort: killing the process we spawned. A viewer that
		// already handed the file off won't die — fine.
		_ = cmd.Process.Kill()
		if bin == "xdg-open" {
			if wm, err := exec.LookPath("wmctrl"); err == nil {
				_ = exec.Command(wm, "-c", filepath.Base(abs)).Run()
			}
		}
	}()
	return nil
}
