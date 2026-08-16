// Package popup owns the floating caption/note window shown after a
// capture. It spawns a zenity GTK form dialog (a real popup window — no
// TUI), collects the caption or note text, and appends the finished entry
// to blog.md.
package popup

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"snapshell/internal/blog"
)

// Popup modes.
const (
	ModeImage = "image"
	ModeCode  = "code"
	ModeNote  = "note"
)

// Result carries what the form produced.
type Result struct {
	// Text is the caption (image/code modes) or the note text (note mode),
	// trimmed. May be empty.
	Text string
	// Submitted is true when the user pressed the save button; false when
	// they cancelled, closed the window, or it timed out.
	Submitted bool
}

// Capture shows the caption/note window for a capture and appends the
// finished entry to <sessionDir>/blog.md. It blocks until the dialog
// closes, so callers must run it in their own goroutine (the daemon does —
// a slow/ignored popup must never block the next hotkey press).
//
// file is the captured image path relative to the session dir (image
// mode); text is the captured command+output text (code mode) or ignored
// (note mode). width/height size the dialog in pixels (0 = zenity's own
// choice); font is a Pango font description for the text area ("" = the
// default font).
//
// Empty or cancelled submit = "skip caption" for image/code (the entry is
// still appended — losing an already-taken screenshot because the caption
// window was dismissed would be a bad outcome) but discards note mode
// entirely, since nothing was captured yet beyond the text itself.
//
// Capture returns an error only on an infrastructure failure (zenity
// missing, dialog failed to launch) — in that case nothing is appended and
// the caller decides how to fall back. A user pressing cancel is not an
// error.
func Capture(mode, sessionDir, file, text string, width, height int, font string) error {
	res, err := askDialog(mode, sessionDir, file, text, width, height, font)
	if err != nil {
		return err
	}
	return applyResult(mode, res, file, text, sessionDir)
}

// askDialog launches the zenity window and returns what the user did.
func askDialog(mode, sessionDir, file, text string, width, height int, font string) (Result, error) {
	bin, err := resolveZenity()
	if err != nil {
		return Result{}, err
	}

	cmd := exec.Command(bin, zenityArgs(mode, sessionDir, file, text, width, height, font)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()

	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	// zenity exits 0 on the OK/save button and 1 on cancel/Esc; anything
	// else (timeout, crash) is treated as cancelled — still a no-op for
	// image/code, a discard for notes.
	return Result{Text: strings.TrimSpace(out.String()), Submitted: code == 0}, nil
}

// zenityArgs builds the zenity argv for a mode.
//
// Every mode is a `--text-info --editable` text area: the caption/note
// input fills the window and wraps, so the user can always see everything
// they type. A `--forms` single-line entry was tried but it can't grow,
// and zenity just leaves the rest of the window as dead space.
func zenityArgs(mode, sessionDir, file, text string, width, height int, font string) []string {
	args := []string{}
	if width > 0 {
		args = append(args, "--width", strconv.Itoa(width))
	}
	if height > 0 {
		args = append(args, "--height", strconv.Itoa(height))
	}
	if font != "" {
		args = append(args, "--font", font)
	}

	switch mode {
	case ModeImage:
		label := describeImage(filepath.Join(sessionDir, file), file)
		return append(args, "--text-info", "--editable",
			"--title=snapshell — add screenshot",
			"--text="+escapeMarkup(label),
			"--ok-label=Save",
			"--cancel-label=Skip",
		)
	case ModeCode:
		return append(args, "--text-info", "--editable",
			"--title=snapshell — add command",
			"--text="+escapeMarkup(truncatePreview(text)),
			"--ok-label=Save",
			"--cancel-label=Skip",
		)
	case ModeNote:
		return append(args, "--text-info", "--editable",
			"--title=snapshell — note",
			"--text=Write your note below, then press Save.",
			"--ok-label=Save",
			"--cancel-label=Discard",
		)
	default:
		return append(args, "--text-info", "--editable",
			"--title=snapshell",
			"--ok-label=Save",
			"--cancel-label=Skip",
		)
	}
}

// applyResult writes the finished entry to blog.md. Factored out of
// Capture so tests can exercise the blog-append behaviour without a live
// dialog.
func applyResult(mode string, res Result, file, text, sessionDir string) error {
	switch mode {
	case ModeImage:
		return blog.Append(sessionDir, blog.Entry{Kind: blog.KindImage, Caption: res.Text, ImagePath: file})
	case ModeCode:
		return blog.Append(sessionDir, blog.Entry{Kind: blog.KindCode, Caption: res.Text, CodeText: text})
	case ModeNote:
		if !res.Submitted || strings.TrimSpace(res.Text) == "" {
			return nil // cancelled or empty note = discard entirely
		}
		return blog.Append(sessionDir, blog.Entry{Kind: blog.KindNote, NoteText: res.Text})
	default:
		return fmt.Errorf("unknown popup mode %q", mode)
	}
}

// resolveZenity finds a usable zenity binary, erroring with a specific
// message naming the missing binary (subprocess deps always fail loudly,
// never silently).
func resolveZenity() (string, error) {
	bin, err := exec.LookPath("zenity")
	if err != nil {
		return "", fmt.Errorf("the caption window needs zenity, but 'zenity' was not found on PATH — install it (e.g. apt install zenity) and retry")
	}
	return bin, nil
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

// imageSize reads the dimensions from an image file header.
func imageSize(path string) (image.Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return image.Config{}, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	return cfg, err
}

// truncatePreview shortens the captured command+output for the dialog's
// label so the window doesn't grow to the size of a full tmux dump. The
// full text is what lands in blog.md.
func truncatePreview(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= 400 {
		return trimmed
	}
	return trimmed[:400] + "\n…"
}

// escapeMarkup neutralizes Pango markup characters in dynamic label text —
// zenity parses labels as Pango markup, so a captured '&' or '<' would
// otherwise garble the window.
func escapeMarkup(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
