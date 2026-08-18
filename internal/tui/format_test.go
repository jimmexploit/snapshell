package tui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"snapshell/internal/inventory"
)

func TestKindIcon(t *testing.T) {
	if kindIcon(inventory.KindImage) != "📷" {
		t.Fatalf("image icon = %q", kindIcon(inventory.KindImage))
	}
	if kindIcon(inventory.KindCode) != "❯" {
		t.Fatalf("code icon = %q", kindIcon(inventory.KindCode))
	}
}

func TestCardLabel(t *testing.T) {
	img := inventory.Card{Kind: inventory.KindImage, Path: "attachments/007.png"}
	if got := cardLabel(img); got != "007.png" {
		t.Fatalf("image label = %q, want filename only", got)
	}
	code := inventory.Card{Kind: inventory.KindCode, Text: "nmap -sV -p- 10.10.10.5\nHost is up (0.04s latency)."}
	if got := cardLabel(code); got != "nmap -sV -p- 10.10.10.5" {
		t.Fatalf("code label = %q", got)
	}
	long := inventory.Card{Kind: inventory.KindCode, Text: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nx"}
	if got := cardLabel(long); len([]rune(got)) > 41 {
		t.Fatalf("long label not truncated: %q (len %d)", got, len([]rune(got)))
	}
}

func TestFirstLine(t *testing.T) {
	if firstLine("a\nb") != "a" {
		t.Fatal("firstLine should stop at the newline")
	}
	if firstLine("no newline") != "no newline" {
		t.Fatal("firstLine should return the whole string without a newline")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Fatal("short strings pass through unchanged")
	}
	got := truncate("abcdefghij", 6)
	if got != "abcde…" {
		t.Fatalf("truncate = %q, want abcde…", got)
	}
	// Multibyte safety: truncating a string of CJK glyphs must not split a
	// rune.
	wide := "日本語のテキストです"
	got = truncate(wide, 4)
	if len([]rune(got)) != 4 {
		t.Fatalf("wide truncate produced %d runes, want 4", len([]rune(got)))
	}
}

func TestRelTime(t *testing.T) {
	now := time.Now()
	if relTime(now) != "just now" {
		t.Fatalf("relTime(now) = %q", relTime(now))
	}
	if relTime(now.Add(-3*time.Minute)) != "3m ago" {
		t.Fatalf("relTime(3m) = %q", relTime(now.Add(-3*time.Minute)))
	}
	if relTime(now.Add(-5*time.Hour)) != "5h ago" {
		t.Fatalf("relTime(5h) = %q", relTime(now.Add(-5*time.Hour)))
	}
	if relTime(now.Add(-3*24*time.Hour)) == "just now" {
		t.Fatal("old timestamp should not read as just now")
	}
}

func TestImageLabel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "attachments", "001.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 320, 200))); err != nil {
		t.Fatal(err)
	}
	f.Close()

	c := inventory.Card{Kind: inventory.KindImage, Path: "attachments/001.png", AbsPath: path}
	got := imageLabel(c)
	want := "attachments/001.png — 320x200"
	if got != want {
		t.Fatalf("imageLabel = %q, want %q", got, want)
	}

	// Missing file falls back to the path.
	c.AbsPath = filepath.Join(dir, "nope.png")
	if got := imageLabel(c); got != c.Path {
		t.Fatalf("imageLabel for missing file = %q, want path only", got)
	}
}

func TestPreviewHead(t *testing.T) {
	text := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm"
	head := previewHead(text)
	if len(linesOf(head)) != 9 {
		t.Fatalf("previewHead lines = %d, want 9 (8 + ellipsis): %q", len(linesOf(head)), head)
	}
	if got := previewHead("single"); got != "single" {
		t.Fatalf("short previewHead = %q", got)
	}
}

func TestOpenImageMissingViewer(t *testing.T) {
	err := openImage("definitely-not-a-real-viewer-xyz", time.Second, "/tmp/whatever.png")
	if err == nil {
		t.Fatal("openImage with a missing configured viewer should fail")
	}
}
