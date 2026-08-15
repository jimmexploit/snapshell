// Package popup owns the floating caption/note window shown after a
// capture. It has two halves: Spawn launches the floating terminal that
// runs `snapshell internal-popup`, and Run is the huh TUI that runs inside
// that terminal and appends the finished entry to blog.md.
package popup

import (
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"

	"snapshell/internal/blog"
)

// Popup modes, matching the internal-popup --mode flag.
const (
	ModeImage = "image"
	ModeCode  = "code"
	ModeNote  = "note"
)

// Run collects the caption (image/code modes) or the note text (note mode)
// with a huh form and appends the finished entry to <sessionDir>/blog.md.
// It runs inside the spawned floating terminal.
//
// file is the captured image path relative to the session dir (image mode)
// or the temp file holding the captured command+output text (code mode);
// ignored in note mode.
//
// Empty submit = "skip caption" for image/code (the entry is still
// appended) but discards note mode entirely. Esc/cancel behaves the same
// way: image/code still append without a caption, note loses its text.
func Run(mode, file, sessionDir string) error {
	var caption string

	group := []*huh.Group{}

	switch mode {
	case ModeImage:
		group = append(group, huh.NewGroup(
			previewNote(describeImage(filepath.Join(sessionDir, file), file)),
			huh.NewText().Title("Caption (optional)").Placeholder("What did this screenshot capture?").Value(&caption),
		))
	case ModeCode:
		text, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("popup code mode: read captured text: %v", err)
		}
		defer os.Remove(file) // the temp capture file is ours to clean up
		group = append(group, huh.NewGroup(
			huh.NewNote().Title("Captured command").Description(string(text)),
			huh.NewText().Title("Caption (optional)").Placeholder("What does this command show?").Value(&caption),
		))
	case ModeNote:
		group = append(group, huh.NewGroup(
			huh.NewText().Title("Note").Placeholder("Type your note...").Value(&caption),
		))
	default:
		return fmt.Errorf("unknown popup mode %q", mode)
	}

	// huh's default quit binding is Ctrl+C; also accept Esc so the user can
	// cancel the caption window the same way they'd close any other window.
	// Both map to ErrUserAborted, handled below.
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "cancel"))

	form := huh.NewForm(group...).WithKeyMap(km)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			// Esc/close: image/code still record the capture (no caption);
			// note mode has nothing to record.
			if mode == ModeNote {
				return nil
			}
			caption = ""
		} else {
			return fmt.Errorf("popup form: %v", err)
		}
	}

	return appendResult(mode, caption, file, sessionDir)
}

// appendResult writes the finished entry to blog.md. Factored out of Run so
// tests can exercise the blog-append behaviour without a TTY.
func appendResult(mode, caption, file, sessionDir string) error {
	switch mode {
	case ModeImage:
		return blog.Append(sessionDir, blog.Entry{Kind: blog.KindImage, Caption: caption, ImagePath: file})
	case ModeCode:
		text, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read captured text: %v", err)
		}
		return blog.Append(sessionDir, blog.Entry{Kind: blog.KindCode, Caption: caption, CodeText: string(text)})
	case ModeNote:
		if strings.TrimSpace(caption) == "" {
			return nil // empty note = discard entirely
		}
		return blog.Append(sessionDir, blog.Entry{Kind: blog.KindNote, NoteText: caption})
	default:
		return fmt.Errorf("unknown popup mode %q", mode)
	}
}

// previewNote builds the non-interactive preview region shown above the
// caption field.
func previewNote(desc string) *huh.Note {
	return huh.NewNote().Title("Preview").Description(desc)
}

// describeImage renders the image-mode preview label, including pixel
// dimensions read from the PNG header when possible. readPath is where the
// file actually lives (absolute, resolved against the session dir);
// displayPath is the relative path shown to the user.
func describeImage(readPath, displayPath string) string {
	dim, err := imageSize(readPath)
	if err != nil {
		return "📷 " + displayPath
	}
	return fmt.Sprintf("📷 %s — %dx%d", displayPath, dim.Width, dim.Height)
}

func imageSize(path string) (image.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return image.Config{}, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	return cfg, err
}

// TempCodeFile writes captured command+output text to a temp file so the
// popup (a separate process) can read it. Returns the path. The popup
// removes the file after use.
func TempCodeFile(text string) (string, error) {
	f, err := os.CreateTemp("", "snapshell-code-*.txt")
	if err != nil {
		return "", fmt.Errorf("create temp capture file: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		return "", fmt.Errorf("write temp capture file: %v", err)
	}
	return filepath.Clean(f.Name()), nil
}
