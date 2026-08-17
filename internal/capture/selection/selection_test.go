package selection

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeXclip installs an xclip shim into binDir and prepends binDir to PATH.
// The shim prints the configured body for each selection, or exits 1 (like
// real xclip with an empty selection) when a ".fail" marker file for that
// selection exists. failDir must be writable; pass the binDir itself.
func fakeXclip(t *testing.T, binDir, primary, clipboard string, failPrimary, failClipboard bool) {
	t.Helper()
	script := "#!/bin/sh\n" +
		"case \"$2\" in\n" +
		"  primary)\n" +
		"    if [ -e \"$SNAPSHELL_FAKE/primary.fail\" ]; then printf 'target STRING not available' >&2; exit 1; fi\n" +
		"    printf '%s\\n' " + quote(primary) + "\n" +
		"    ;;\n" +
		"  clipboard)\n" +
		"    if [ -e \"$SNAPSHELL_FAKE/clipboard.fail\" ]; then printf 'target STRING not available' >&2; exit 1; fi\n" +
		"    printf '%s\\n' " + quote(clipboard) + "\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(binDir, "xclip"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if failPrimary {
		os.WriteFile(filepath.Join(binDir, "primary.fail"), nil, 0o600)
	}
	if failClipboard {
		os.WriteFile(filepath.Join(binDir, "clipboard.fail"), nil, 0o600)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("SNAPSHELL_FAKE", binDir)
}

func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestReadPrimary(t *testing.T) {
	dir := t.TempDir()
	fakeXclip(t, dir, "selected-line\n", "", false, false)
	got, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if got != "selected-line" {
		t.Fatalf("got %q, want %q", got, "selected-line")
	}
}

func TestReadFallsBackToClipboard(t *testing.T) {
	dir := t.TempDir()
	fakeXclip(t, dir, "", "clip-line-1\nclip-line-2\n", false, false)
	got, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if got != "clip-line-1\nclip-line-2" {
		t.Fatalf("got %q", got)
	}
}

func TestReadEmptySelectionThenClipboard(t *testing.T) {
	dir := t.TempDir()
	// primary read exits 1 (empty selection), clipboard has text.
	fakeXclip(t, dir, "", "from-clipboard\n", true, false)
	got, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-clipboard" {
		t.Fatalf("got %q", got)
	}
}

func TestReadBothEmpty(t *testing.T) {
	dir := t.TempDir()
	fakeXclip(t, dir, "", "", true, true)
	_, err := Read()
	if !errors.Is(err, ErrEmpty) {
		t.Fatalf("err = %v, want ErrEmpty", err)
	}
}

func TestReadMissingXclip(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Read()
	if err == nil || !strings.Contains(err.Error(), "xclip not found on PATH") {
		t.Fatalf("err = %v, want xclip-not-found error", err)
	}
}
