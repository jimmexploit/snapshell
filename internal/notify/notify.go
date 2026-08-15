package notify

import (
	"fmt"
	"os/exec"
	"strings"
)

// Send shows a desktop notification via notify-send. It returns an error
// (naming the binary) only if notify-send is missing or failed, so callers
// can log it; it never panics.
func Send(summary, body string) error {
	bin, err := exec.LookPath("notify-send")
	if err != nil {
		return fmt.Errorf("notify-send not found on PATH — cannot surface notification")
	}

	args := []string{summary}
	if strings.TrimSpace(body) != "" {
		args = append(args, "--expire-time=8000", body)
	}

	if out, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("notify-send failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
