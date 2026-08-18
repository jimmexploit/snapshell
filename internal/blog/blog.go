package blog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EntryKind identifies which blog entry type to render.
type EntryKind int

const (
	KindImage EntryKind = iota
	KindCode
	KindNote
)

// Entry carries everything needed to render one blog entry. Only the fields
// relevant to the entry's Kind are used.
type Entry struct {
	Kind      EntryKind
	Caption   string // may be empty (Image/Code entries)
	ImagePath string // relative path, Image entries only
	CodeText  string // Code entries only
	NoteText  string // Note entries only
	// CaptionAfter places the caption below the image/code block instead of
	// above it (the default). Notes have no caption and ignore it.
	CaptionAfter bool
}

// Append writes one formatted entry to <sessionDir>/blog.md. It is the only
// write path into blog.md in the whole codebase.
func Append(sessionDir string, e Entry) error {
	path := filepath.Join(sessionDir, "blog.md")

	if err := ensureHeader(path, sessionDir); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open blog.md: %w", err)
	}
	defer f.Close()

	body, err := render(e)
	if err != nil {
		return err
	}

	// Exactly one blank line separates entries.
	if _, err := fmt.Fprintf(f, "\n%s\n", body); err != nil {
		return fmt.Errorf("append to blog.md: %w", err)
	}
	return nil
}

// ensureHeader creates blog.md with its header if it does not exist yet.
// The header name is the session dir's base name.
func ensureHeader(path, sessionDir string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	header := fmt.Sprintf("# %s\n", filepath.Base(sessionDir))
	return os.WriteFile(path, []byte(header), 0o600)
}

func render(e Entry) (string, error) {
	switch e.Kind {
	case KindImage:
		if e.ImagePath == "" {
			return "", fmt.Errorf("image entry without ImagePath")
		}
		return renderBlock(e, "![]("+e.ImagePath+")"), nil
	case KindCode:
		return renderBlock(e, codeFence(e.CodeText)), nil
	case KindNote:
		if e.NoteText == "" {
			return "", fmt.Errorf("note entry without NoteText")
		}
		return e.NoteText, nil
	default:
		return "", fmt.Errorf("unknown entry kind %d", e.Kind)
	}
}

// renderBlock places the entry's optional caption above or below the image
// line / code fence per Entry.CaptionAfter, each on its own line. An empty
// caption renders just the block.
func renderBlock(e Entry, block string) string {
	caption := strings.TrimRight(strings.TrimSpace(e.Caption), "\n")
	if caption == "" {
		return block
	}
	if e.CaptionAfter {
		return block + "\n" + caption
	}
	return caption + "\n" + block
}

// codeFence wraps code in a language-tagged fence chosen by DetectLang. If
// the code itself contains a run of backticks, the fence is widened past
// the longest run so the block never breaks early.
func codeFence(text string) string {
	fenceLen := 3
	for _, line := range strings.Split(text, "\n") {
		run := len(line) - len(strings.TrimLeft(line, "`"))
		if run >= fenceLen {
			fenceLen = run + 1
		}
	}
	fence := strings.Repeat("`", fenceLen)
	body := text
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return fence + DetectLang(text) + "\n" + body + fence
}
