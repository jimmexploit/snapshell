package screenshot

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result describes a completed screenshot capture.
type Result struct {
	AbsPath   string // absolute path of the saved PNG
	RelPath   string // path relative to the session dir (attachments/NNN.png)
	Cancelled bool   // true when the user aborted region selection (no file)
}

// Capture runs the configured screenshot tool in region-select mode and
// confirms the file landed at <sessionDir>/attachments/<NNN>.png. It blocks
// until the user finishes (region-select tools are interactive; no timeout
// is imposed — callers must run it in a goroutine).
//
// tool is the configured value from config ([screenshot].tool). Resolution
// order: configured tool → (if it was the default flameshot) mate-screenshot
// → error naming everything tried. onWarn is called for one-time fallback
// warnings.
func Capture(sessionDir, tool string, num int, onWarn func(string)) (Result, error) {
	name := fmt.Sprintf("%03d.png", num)
	rel := filepath.Join("attachments", name)
	abs := filepath.Join(sessionDir, rel)

	bin, err := resolveTool(tool, onWarn)
	if err != nil {
		return Result{}, err
	}

	switch bin {
	case "flameshot":
		// flameshot gui blocks until the user saves or cancels. It writes
		// the file itself to --path.
		cmd := exec.Command(bin, "gui", "--path", abs)
		cmd.Run() // exit code is not meaningful for cancel-vs-save; file presence decides
	case "mate-screenshot":
		// This mate-screenshot version has no --file flag, so capture to
		// the clipboard and pull the PNG out with xclip.
		cmd := exec.Command(bin, "-a", "-c")
		if err := cmd.Run(); err != nil {
			return Result{Cancelled: true}, nil // user cancelled area select
		}
		if err := clipboardToFile(abs); err != nil {
			return Result{}, err
		}
	default:
		return Result{}, fmt.Errorf("unhandled screenshot tool %q", bin)
	}

	return finalize(abs, rel)
}

// finalize decides success vs cancellation by checking the output file.
func finalize(abs, rel string) (Result, error) {
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Cancelled: true}, nil
		}
		return Result{}, fmt.Errorf("stat captured file: %w", err)
	}
	if fi.Size() == 0 {
		os.Remove(abs)
		return Result{Cancelled: true}, nil
	}
	return Result{AbsPath: abs, RelPath: rel}, nil
}

func clipboardToFile(abs string) error {
	bin, err := exec.LookPath("xclip")
	if err != nil {
		return fmt.Errorf("xclip not found on PATH — required to save mate-screenshot captures")
	}
	out, err := exec.Command(bin, "-selection", "clipboard", "-t", "image/png", "-o").Output()
	if err != nil {
		// No image in the clipboard is the normal outcome when the user
		// cancels the area select.
		return fmt.Errorf("retrieve screenshot from clipboard: %v", err)
	}
	if len(out) == 0 {
		return fmt.Errorf("clipboard contained no image")
	}
	return os.WriteFile(abs, out, 0o600)
}

// resolveTool picks the binary to run, applying the documented fallback.
func resolveTool(configured string, onWarn func(string)) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		configured = "flameshot"
	}

	if _, err := exec.LookPath(configured); err == nil {
		return configured, nil
	}

	if configured == "flameshot" {
		if _, err := exec.LookPath("mate-screenshot"); err == nil {
			if onWarn != nil {
				onWarn("flameshot not found on PATH — falling back to mate-screenshot")
			}
			return "mate-screenshot", nil
		}
		return "", fmt.Errorf("cannot capture screenshot: none of flameshot, mate-screenshot found on PATH — install one")
	}

	return "", fmt.Errorf("configured screenshot tool %q not found on PATH", configured)
}
