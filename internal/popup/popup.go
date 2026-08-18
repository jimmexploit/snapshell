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
	ModeImage     = "image"
	ModeCode      = "code"
	ModeNote      = "note"
	ModeSelection = "selection"
)

// zenityExtraExit is zenity's documented exit code when an extra button
// (see --extra-button) is clicked rather than OK or Cancel. Real zenity
// 4.1.90 does NOT honour it for --text-info dialogs — it exits 1 with the
// button's label on stdout instead — so detection also checks the stdout
// content; see resultFromExit.
const zenityExtraExit = 5

// extraButtonLabel is the --extra-button label used by code mode. When it
// is clicked, zenity prints this exact string to stdout.
const extraButtonLabel = "Cancel"

// Result carries what the form produced.
type Result struct {
	// Text is the caption (image/code modes) or the note text (note mode),
	// trimmed. May be empty.
	Text string
	// Submitted is true when the user pressed the save button; false when
	// they cancelled, closed the window, or it timed out.
	Submitted bool
	// Aborted is true only when the user pressed the extra "Cancel" button
	// (image/code modes): the capture is discarded entirely — for an image
	// the screenshot file is deleted too — no entry appended.
	Aborted bool
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
// default font). position moves the dialog to a spot on screen after it
// spawns (a preset like "center"/"top-right" or explicit pixels
// "120,80"); empty leaves placement to the window manager. theme is a GTK
// theme applied to the dialog via the GTK_THEME environment variable
// ("Sweet", "Sweet:dark", ...); empty uses the system's default theme.
//
// Empty or cancelled submit = "skip caption" for image/code (the entry is
// still appended — losing an already-taken screenshot because the caption
// window was dismissed would be a bad outcome) but discards note mode
// entirely, since nothing was captured yet beyond the text itself. Image
// and code modes additionally offer a "Cancel" button that aborts the
// whole capture — for a screenshot the captured file is deleted too — the
// only path where a dismissed dialog loses the capture.
//
// Capture returns an error only on an infrastructure failure (zenity
// missing, dialog failed to launch, a configured position that can't be
// applied) — in that case nothing is appended and the caller decides how
// to fall back. A user pressing cancel is not an error.
//
// count is the number of commands this capture spans (code mode only): for
// count > 1 the window title reports it ("snapshell - command ×2"), so the
// user can see the count prefix took effect. Other modes ignore it.
func Capture(mode, sessionDir, file, text string, width, height int, font, position, theme string, count int) error {
	res, err := askDialog(mode, sessionDir, file, text, width, height, font, position, theme, count)
	if err != nil {
		return err
	}
	return applyResult(mode, res, file, text, sessionDir)
}

// askDialog launches the zenity window and returns what the user did.
func askDialog(mode, sessionDir, file, text string, width, height int, font, position, theme string, count int) (Result, error) {
	bin, err := resolveZenity()
	if err != nil {
		return Result{}, err
	}

	// A configured position must be valid before the dialog opens; missing
	// xdotool is a loud, actionable error naming the binary (the repo's
	// subprocess rule) rather than a silently unmoved window.
	if position != "" {
		if _, err := exec.LookPath("xdotool"); err != nil {
			return Result{}, fmt.Errorf("xdotool not found on PATH — required to position the caption window (popup.position is set)")
		}
		if _, err := parsePosition(position); err != nil {
			return Result{}, err
		}
	}

	cmd := exec.Command(bin, zenityArgs(mode, sessionDir, file, text, width, height, font, count)...)
	if theme != "" {
		// GTK_THEME re-themes the dialog at spawn time; a missing theme
		// falls back to the system default silently (GTK's own behavior).
		cmd.Env = append(os.Environ(), "GTK_THEME="+theme)
	}
	if position != "" {
		// The dialog spawns unmoved; a background helper slides it into
		// place as soon as it maps. Best-effort beyond the upfront
		// validation — if the window never appears the move is simply
		// skipped, the dialog itself is unaffected.
		winW, winH := width, height
		if winW <= 0 {
			winW = 560
		}
		if winH <= 0 {
			winH = 320
		}
		go moveDialog(dialogTitle(mode, count), position, winW, winH)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()

	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return resultFromExit(code, out.String()), nil
}

// resultFromExit maps a zenity exit code and stdout to a Result, keeping
// the pure code→result mapping unit-testable. zenity exits 0 on the
// OK/save button (stdout holds the edited caption text) and 1 on
// cancel/Esc (here the "Skip"/"Discard" button). An extra button is
// documented to exit 5, but real zenity 4.1.90 exits 1 and prints the
// button's label to stdout — so the abort signal is "non-zero exit AND
// stdout is exactly the extra-button label". Any other non-zero code
// (timeout, crash) is treated as a plain cancel.
func resultFromExit(code int, out string) Result {
	text := strings.TrimSpace(out)
	aborted := code == zenityExtraExit || (code != 0 && text == extraButtonLabel)
	if aborted {
		return Result{Text: text, Aborted: true}
	}
	return Result{Text: text, Submitted: code == 0}
}

// zenityArgs builds the zenity argv for a mode.
//
// Every mode is a `--text-info --editable` text area: the caption/note
// input fills the window and wraps, so the user can always see everything
// they type. A `--forms` single-line entry was tried but it can't grow,
// and zenity just leaves the rest of the window as dead space.
func zenityArgs(mode, sessionDir, file, text string, width, height int, font string, count int) []string {
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
			"--title="+dialogTitle(mode, count),
			"--text="+escapeMarkup(label),
			"--ok-label=Save",
			"--cancel-label=Skip",
			// Same three-button contract as code mode: Save keeps it with a
			// caption, Skip keeps it without, Cancel deletes the screenshot
			// and adds nothing.
			"--extra-button=Cancel",
		)
	case ModeCode, ModeSelection:
		return append(args, "--text-info", "--editable",
			"--title="+dialogTitle(mode, count),
			"--text="+escapeMarkup(truncatePreview(text)),
			"--ok-label=Save",
			"--cancel-label=Skip",
			// The one "discard the capture entirely" path for code mode:
			// Save keeps it with a caption, Skip keeps it without, Cancel
			// throws it away (no blog.md entry).
			"--extra-button=Cancel",
		)
	case ModeNote:
		return append(args, "--text-info", "--editable",
			"--title="+dialogTitle(mode, count),
			"--text=Write your note below, then press Save.",
			"--ok-label=Save",
			"--cancel-label=Discard",
		)
	default:
		return append(args, "--text-info", "--editable",
			"--title="+dialogTitle(mode, count),
			"--ok-label=Save",
			"--cancel-label=Skip",
		)
	}
}

// dialogTitle returns the window title for a mode. The position mover
// finds the dialog by this title, so it must match what zenity shows.
// Titles use a plain hyphen after "snapshell" (no em dash), and the label
// describes the thing being captured, not the action.
//
// count is how many commands the capture spans (code mode only): when it is
// 1 (the default) the plain title is shown; when it is more than one a
// multiplication-sign suffix reports the count ("snapshell - command ×2"),
// so a multi-command Alt+2 capture is visibly different from a single one.
// The position mover searches the window title by substring, so the suffix
// doesn't break positioning.
func dialogTitle(mode string, count int) string {
	title := ""
	switch mode {
	case ModeImage:
		title = "snapshell - screenshot"
	case ModeCode:
		title = "snapshell - command"
	case ModeNote:
		title = "snapshell - note"
	case ModeSelection:
		title = "snapshell - selected text"
	default:
		title = "snapshell"
	}
	if mode == ModeCode && count > 1 {
		return title + " ×" + strconv.Itoa(count)
	}
	return title
}

// applyResult writes the finished entry to blog.md. Factored out of
// Capture so tests can exercise the blog-append behaviour without a live
// dialog.
func applyResult(mode string, res Result, file, text, sessionDir string) error {
	switch mode {
	case ModeImage:
		if res.Aborted {
			// The user pressed Cancel: discard the whole capture, including
			// the screenshot already written to attachments/. A cancelled
			// capture must leave no trace — not even a stray file.
			if err := os.Remove(filepath.Join(sessionDir, file)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove cancelled screenshot: %w", err)
			}
			return nil
		}
		return blog.Append(sessionDir, blog.Entry{Kind: blog.KindImage, Caption: res.Text, ImagePath: file})
	case ModeCode, ModeSelection:
		if res.Aborted {
			return nil // the user pressed Cancel: discard the capture
		}
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
