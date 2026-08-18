package tui

import (
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"snapshell/internal/inventory"
)

// kindIcon is the one-character glyph used in the card list.
func kindIcon(k inventory.Kind) string {
	if k == inventory.KindImage {
		return "📷"
	}
	return "❯"
}

// kindLabel is the word used in the detail header.
func kindLabel(k inventory.Kind) string {
	if k == inventory.KindImage {
		return "Screenshot"
	}
	return "Command"
}

// cardLabel is the short one-line label for a card: the filename for
// images, the first line of the captured command for code.
func cardLabel(c inventory.Card) string {
	if c.Kind == inventory.KindImage {
		return filepath.Base(c.Path)
	}
	return truncate(firstLine(c.Text), 40)
}

// firstLine returns the first line of s (the whole string if single-line).
func firstLine(s string) string {
	i := strings.IndexAny(s, "\n")
	if i < 0 {
		return s
	}
	return s[:i]
}

// truncate shortens s to at most n runes, appending an ellipsis. It is
// rune-aware so multibyte glyphs aren't cut in half.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// relTime renders a timestamp as a short relative label.
func relTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("Jan 2")
	}
}

// imageLabel describes a screenshot card: relative path + pixel dimensions
// read from the file header. Missing/unreadable files fall back to the path.
func imageLabel(c inventory.Card) string {
	if c.AbsPath == "" {
		return c.Path
	}
	dim, err := imageSize(c.AbsPath)
	if err != nil {
		return c.Path
	}
	return fmt.Sprintf("%s — %dx%d", c.Path, dim.Width, dim.Height)
}

func imageSize(path string) (image.Config, error) {
	cfg, _, err := imageDecode(path)
	return cfg, err
}

// imageDecode reads just the image header, returning its config and format
// ("png", "jpeg", ...) without decoding the whole bitmap.
func imageDecode(path string) (image.Config, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return image.Config{}, "", err
	}
	defer f.Close()
	return image.DecodeConfig(f)
}

// previewHead is the (short) code sample shown in the caption preview.
func previewHead(text string) string {
	lines := linesOf(strings.TrimRight(text, "\n"))
	if len(lines) > 8 {
		lines = append(lines[:8], "…")
	}
	return strings.Join(lines, "\n")
}
