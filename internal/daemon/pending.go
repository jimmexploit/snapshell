package daemon

import (
	"encoding/json"
	"fmt"
	"os"
)

// PendingCapture is a capture waiting for its caption, written by the
// daemon and consumed by `snapshell internal-popup-inline` (invoked by the
// shell hook at the next prompt). One pending capture at a time — a newer
// one overwrites an older one.
type PendingCapture struct {
	Mode       string `json:"mode"`        // popup mode: image, code, note
	File       string `json:"file"`        // image: relative attachment; code: temp text file; note: ""
	SessionDir string `json:"session_dir"` // session folder (blog.md lives here)
}

// WritePending stages a capture for inline captioning. The write is atomic
// (temp file + rename) so the shell hook never reads a half-written
// request.
func WritePending(p PendingCapture) error {
	if err := os.MkdirAll(StateDir(), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(StateDir(), ".pending-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, PendingPath())
}

// ReadPending returns the pending capture, or ok=false when none is
// staged.
func ReadPending() (PendingCapture, bool, error) {
	data, err := os.ReadFile(PendingPath())
	if err != nil {
		if os.IsNotExist(err) {
			return PendingCapture{}, false, nil
		}
		return PendingCapture{}, false, fmt.Errorf("read pending capture: %v", err)
	}
	var p PendingCapture
	if err := json.Unmarshal(data, &p); err != nil {
		return PendingCapture{}, false, fmt.Errorf("parse pending capture: %v", err)
	}
	return p, true, nil
}

// ClearPending removes a consumed pending capture. A missing file is not
// an error.
func ClearPending() {
	_ = os.Remove(PendingPath())
}
