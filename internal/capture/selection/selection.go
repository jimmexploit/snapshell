package selection

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrEmpty is returned when neither the primary selection nor the clipboard
// holds any text.
var ErrEmpty = errors.New("nothing selected and clipboard empty")

// Read returns the currently selected text (X11 PRIMARY selection), falling
// back to the clipboard when nothing is selected. Multi-line text keeps its
// newlines; only a single trailing newline is trimmed. Both empty → ErrEmpty.
func Read() (string, error) {
	bin, err := exec.LookPath("xclip")
	if err != nil {
		return "", fmt.Errorf("xclip not found on PATH — required for selection/clipboard capture")
	}
	if text, ok, err := read(bin, "primary"); err != nil {
		return "", err
	} else if ok {
		return text, nil
	}
	if text, ok, err := read(bin, "clipboard"); err != nil {
		return "", err
	} else if ok {
		return text, nil
	}
	return "", ErrEmpty
}

// read fetches one X11 selection. ok=false means the selection is empty or
// holds something xclip can't deliver as text — the normal "nothing
// selected" case, not a failure.
func read(bin, name string) (text string, ok bool, err error) {
	out, err := exec.Command(bin, "-selection", name, "-o").Output()
	if err != nil {
		// xclip exits non-zero when the selection is unavailable/empty.
		if _, isExit := err.(*exec.ExitError); isExit {
			return "", false, nil
		}
		return "", false, fmt.Errorf("run xclip for %s selection: %v", name, err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return "", false, nil
	}
	return strings.TrimRight(string(out), "\n"), true, nil
}
