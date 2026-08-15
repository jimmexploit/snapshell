package screenshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFlameshot is a shell script that emulates `flameshot gui --path <p>`:
// it writes dummy bytes to the --path value and exits 0. When given the
// extra arg "CANCEL", it writes nothing (cancel simulation).
const fakeFlameshot = `#!/bin/sh
prev=""
for a in "$@"; do
  if [ "$prev" = "--path" ]; then echo -n "FAKEPNG" > "$a"; fi
  prev="$a"
done
exit 0
`

// fakeFlameshotCancel writes nothing, like a cancelled selection.
const fakeFlameshotCancel = `#!/bin/sh
exit 0
`

// fakeMateScreenshot exits 0, mimicking a successful area capture (the
// actual image is pulled from the clipboard by xclip).
const fakeMateScreenshot = `#!/bin/sh
exit 0
`

// fakeXclip writes dummy PNG bytes to stdout.
const fakeXclip = `#!/bin/sh
echo -n "FAKEPNG"
`

func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakePath isolates the process PATH to just binDir so tests never find the
// real flameshot/mate-screenshot installed on the system.
func fakePath(t *testing.T, binDir string) {
	t.Helper()
	t.Setenv("PATH", binDir)
}

func TestCaptureWithFlameshot(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, binDir, "flameshot", fakeFlameshot)
	fakePath(t, binDir)

	sessionDir := t.TempDir()
	os.MkdirAll(filepath.Join(sessionDir, "attachments"), 0o700)

	res, err := Capture(sessionDir, "flameshot", 1, nil)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if res.Cancelled {
		t.Fatal("expected success, got cancelled")
	}
	if res.RelPath != "attachments/001.png" {
		t.Fatalf("unexpected relpath %q", res.RelPath)
	}
	data, err := os.ReadFile(res.AbsPath)
	if err != nil || string(data) != "FAKEPNG" {
		t.Fatalf("file not written correctly: %q err=%v", data, err)
	}
}

func TestCaptureCancelled(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, binDir, "flameshot", fakeFlameshotCancel)
	fakePath(t, binDir)

	sessionDir := t.TempDir()
	os.MkdirAll(filepath.Join(sessionDir, "attachments"), 0o700)

	res, err := Capture(sessionDir, "flameshot", 2, nil)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if !res.Cancelled {
		t.Fatal("expected cancelled when no file written")
	}
	if _, statErr := os.Stat(filepath.Join(sessionDir, "attachments", "002.png")); !os.IsNotExist(statErr) {
		t.Fatal("no file should exist after cancel")
	}
}

func TestCaptureFallbackToMateScreenshot(t *testing.T) {
	binDir := t.TempDir()
	// flameshot missing; mate-screenshot + xclip present.
	writeScript(t, binDir, "mate-screenshot", fakeMateScreenshot)
	writeScript(t, binDir, "xclip", fakeXclip)
	fakePath(t, binDir)

	sessionDir := t.TempDir()
	os.MkdirAll(filepath.Join(sessionDir, "attachments"), 0o700)

	var warned []string
	res, err := Capture(sessionDir, "flameshot", 3, func(w string) { warned = append(warned, w) })
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "flameshot not found") {
		t.Fatalf("expected fallback warning, got %v", warned)
	}
	if res.Cancelled {
		t.Fatal("expected success via fallback")
	}
}

func TestCaptureNoToolAvailable(t *testing.T) {
	binDir := t.TempDir() // empty
	fakePath(t, binDir)

	sessionDir := t.TempDir()
	os.MkdirAll(filepath.Join(sessionDir, "attachments"), 0o700)

	_, err := Capture(sessionDir, "flameshot", 1, nil)
	if err == nil {
		t.Fatal("expected error when no tool available")
	}
	for _, want := range []string{"flameshot", "mate-screenshot"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should name %s: %v", want, err)
		}
	}
}

func TestResolveCustomToolMissing(t *testing.T) {
	binDir := t.TempDir()
	fakePath(t, binDir)

	_, err := resolveTool("scrot", nil)
	if err == nil || !strings.Contains(err.Error(), "scrot") {
		t.Fatalf("expected error naming custom tool, got %v", err)
	}
}

func TestNumberFormatting(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, binDir, "flameshot", fakeFlameshot)
	fakePath(t, binDir)

	sessionDir := t.TempDir()
	os.MkdirAll(filepath.Join(sessionDir, "attachments"), 0o700)

	res, err := Capture(sessionDir, "flameshot", 12, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.RelPath != "attachments/012.png" {
		t.Fatalf("expected zero-padded name, got %q", res.RelPath)
	}
}
