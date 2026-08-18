package popup

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestApplyNote(t *testing.T) {
	dir := t.TempDir()
	res := Result{Text: "found port 445 open", Submitted: true}
	if err := applyResult(ModeNote, res, "", "", dir); err != nil {
		t.Fatalf("applyResult: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	if !strings.Contains(string(data), "found port 445 open") {
		t.Fatalf("blog.md = %q, want the note text", data)
	}
	if strings.Contains(string(data), "**") {
		t.Fatalf("note entry must not have a caption line, got %q", data)
	}
}

func TestApplyEmptyOrCancelledNoteDiscards(t *testing.T) {
	for name, res := range map[string]Result{
		"empty submitted":    {Text: "   ", Submitted: true},
		"cancelled":          {Text: "note that was typed", Submitted: false},
		"cancelled and text": {Text: "", Submitted: false},
	} {
		dir := t.TempDir()
		if err := applyResult(ModeNote, res, "", "", dir); err != nil {
			t.Fatalf("%s: applyResult: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "blog.md")); !os.IsNotExist(err) {
			t.Fatalf("%s: empty/cancelled note must not create blog.md", name)
		}
	}
}

func TestApplyImageWithCaption(t *testing.T) {
	dir := t.TempDir()
	res := Result{Text: "rooted it", Submitted: true}
	if err := applyResult(ModeImage, res, "attachments/001.png", "", dir); err != nil {
		t.Fatalf("applyResult: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	if !strings.Contains(string(data), "rooted it\n") {
		t.Fatalf("want caption line, got %q", data)
	}
	if strings.Contains(string(data), "**") {
		t.Fatalf("caption must not be bolded, got %q", data)
	}
	if !strings.Contains(string(data), "![](attachments/001.png)") {
		t.Fatalf("want relative image path, got %q", data)
	}
}

func TestApplyImageNoCaption(t *testing.T) {
	// Cancelling the caption window still records the screenshot.
	for name, res := range map[string]Result{
		"empty submitted": {Text: "", Submitted: true},
		"cancelled":       {Text: "", Submitted: false},
	} {
		dir := t.TempDir()
		if err := applyResult(ModeImage, res, "attachments/001.png", "", dir); err != nil {
			t.Fatalf("%s: applyResult: %v", name, err)
		}
		data, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
		if strings.Contains(string(data), "**") {
			t.Fatalf("%s: empty caption must be omitted, got %q", name, data)
		}
		if !strings.Contains(string(data), "![](attachments/001.png)") {
			t.Fatalf("%s: image must still be appended, got %q", name, data)
		}
	}
}

func TestApplyCode(t *testing.T) {
	dir := t.TempDir()
	res := Result{Text: "port scan", Submitted: true}
	text := "┌─[root@box]# nmap -sV 10.10.11.5\n22/tcp open  ssh\n"
	if err := applyResult(ModeCode, res, "", text, dir); err != nil {
		t.Fatalf("applyResult: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	for _, want := range []string{"port scan\n", "```bash", "nmap -sV 10.10.11.5", "```"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("code entry missing %q:\n%s", want, data)
		}
	}
}

func TestApplyCodeCancelledStillAppends(t *testing.T) {
	dir := t.TempDir()
	if err := applyResult(ModeCode, Result{Submitted: false}, "", "whoami\nroot\n", dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	if !strings.Contains(string(data), "```text") || strings.Contains(string(data), "**") {
		t.Fatalf("cancelled code capture should append without a caption:\n%s", data)
	}
}

func TestApplyImageAbortedDeletesScreenshot(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "attachments", "001.png")
	os.MkdirAll(filepath.Dir(img), 0o700)
	os.WriteFile(img, []byte("fake png"), 0o600)

	// The extra "Cancel" button must delete the already-captured screenshot
	// AND leave no blog.md entry behind.
	if err := applyResult(ModeImage, Result{Submitted: false, Aborted: true}, "attachments/001.png", "", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(img); !os.IsNotExist(err) {
		t.Fatalf("cancelled screenshot file must be deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "blog.md")); !os.IsNotExist(err) {
		t.Fatalf("cancelled image capture must not create blog.md")
	}
}

func TestApplyImageAbortedMissingFileIsFine(t *testing.T) {
	// Deleting a screenshot whose file was never written must not error.
	dir := t.TempDir()
	if err := applyResult(ModeImage, Result{Aborted: true}, "attachments/001.png", "", dir); err != nil {
		t.Fatalf("abort with missing file should be a no-op, got %v", err)
	}
}

func TestApplyCodeAbortedDiscards(t *testing.T) {
	dir := t.TempDir()
	// The extra "Cancel" button must throw the capture away entirely — no
	// blog.md at all, unlike Save (keeps with caption) and Skip (keeps
	// without).
	if err := applyResult(ModeCode, Result{Submitted: false, Aborted: true}, "", "whoami\nroot\n", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blog.md")); !os.IsNotExist(err) {
		t.Fatalf("aborted code capture must not create blog.md")
	}
}

func TestResultFromExit(t *testing.T) {
	cases := []struct {
		name string
		code int
		out  string
		want Result
	}{
		{"save", 0, "port scan", Result{Text: "port scan", Submitted: true}},
		{"save with empty caption", 0, "", Result{Submitted: true}},
		// zenity 4.1.90 exits 1 AND prints the label for the extra button.
		{"cancel extra button real zenity", 1, "Cancel", Result{Text: "Cancel", Aborted: true}},
		// documented behaviour for other zenity versions.
		{"cancel extra button documented", 5, "Cancel", Result{Text: "Cancel", Aborted: true}},
		// A caption that happens to equal the label must NOT abort when saved.
		{"save caption equals label", 0, "Cancel", Result{Text: "Cancel", Submitted: true}},
		{"skip", 1, "", Result{Submitted: false}},
		{"timeout/crash", 255, "", Result{Submitted: false}},
	}
	for _, tc := range cases {
		if got := resultFromExit(tc.code, tc.out); got != tc.want {
			t.Errorf("%s: resultFromExit(%d, %q) = %+v, want %+v", tc.name, tc.code, tc.out, got, tc.want)
		}
	}
}

func TestApplyUnknownMode(t *testing.T) {
	if err := applyResult("bogus", Result{}, "", "", t.TempDir()); err == nil {
		t.Fatal("unknown mode should error")
	}
}

func TestZenityArgsImage(t *testing.T) {
	args := zenityArgs(ModeImage, "/sessions/box", "attachments/001.png", "", 520, 300, "Sans 14", 1)
	joined := strings.Join(args, " ")
	// The caption input is a text area (--text-info --editable) that fills
	// the window, so the user can see everything they type.
	for _, want := range []string{"--text-info", "--editable", "--width", "520", "--height", "300",
		"--font", "Sans 14", "--ok-label=Save", "--cancel-label=Skip", "--extra-button=Cancel"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("image args missing %q: %v", want, args)
		}
	}
}

func TestZenityArgsNoFontWhenEmpty(t *testing.T) {
	args := zenityArgs(ModeImage, "/sessions/box", "attachments/001.png", "", 520, 300, "", 1)
	if strings.Contains(strings.Join(args, " "), "--font") {
		t.Fatalf("empty font must not emit --font: %v", args)
	}
}

func TestZenityArgsCodeTruncatesPreview(t *testing.T) {
	long := strings.Repeat("x", 500)
	args := zenityArgs(ModeCode, "", "", long, 0, 0, "", 1)
	found := false
	for _, a := range args {
		if strings.HasPrefix(a, "--text=") {
			if len(a) > 500 || !strings.HasSuffix(a, "…") {
				t.Fatalf("code preview should be truncated: %q", a)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("code args missing a --text label: %v", args)
	}
	if !slices.Contains(args, "--extra-button=Cancel") {
		t.Fatalf("code mode must offer the third Cancel button: %v", args)
	}
}

func TestZenityArgsNote(t *testing.T) {
	args := zenityArgs(ModeNote, "", "", "", 560, 400, "Sans 13", 1)
	joined := strings.Join(args, " ")
	for _, want := range []string{"--text-info", "--editable", "--font", "Sans 13",
		"--ok-label=Save", "--cancel-label=Discard"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("note args missing %q: %v", want, args)
		}
	}
	if slices.Contains(args, "--extra-button") {
		t.Fatalf("note mode must not have the code-mode Cancel button: %v", args)
	}
}

func TestEscapeMarkup(t *testing.T) {
	got := escapeMarkup("a <b> & \"c\"")
	if got != "a &lt;b&gt; &amp; \"c\"" {
		t.Fatalf("escapeMarkup = %q", got)
	}
}

func TestTruncatePreview(t *testing.T) {
	if got := truncatePreview("  short \n"); got != "short" {
		t.Fatalf("short preview should be trimmed, got %q", got)
	}
	long := strings.Repeat("y", 500)
	got := truncatePreview(long)
	if got != strings.Repeat("y", 400)+"\n…" {
		t.Fatalf("long preview should be 400 chars + ellipsis, got %q", got)
	}
}

func TestResolveZenityMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty
	if _, err := resolveZenity(); err == nil {
		t.Fatal("missing zenity should error")
	}
}

func TestDialogTitleCountSuffix(t *testing.T) {
	// The Alt+2 command-count suffix appears only for code mode with a
	// count above 1; everything else keeps the plain title.
	cases := []struct {
		mode  string
		count int
		want  string
	}{
		{ModeCode, 1, "snapshell - command"},
		{ModeCode, 2, "snapshell - command ×2"},
		{ModeCode, 9, "snapshell - command ×9"},
		{ModeCode, 0, "snapshell - command"}, // 0/negative = default, no suffix
		{ModeImage, 3, "snapshell - screenshot"},
		{ModeNote, 3, "snapshell - note"},
		{ModeSelection, 3, "snapshell - selected text"},
		{"bogus", 2, "snapshell"},
	}
	for _, tc := range cases {
		if got := dialogTitle(tc.mode, tc.count); got != tc.want {
			t.Errorf("dialogTitle(%q, %d) = %q, want %q", tc.mode, tc.count, got, tc.want)
		}
	}
}

func TestZenityArgsCodeCountTitle(t *testing.T) {
	// The count must reach the actual zenity --title (single source of
	// truth shared with the position mover).
	args := zenityArgs(ModeCode, "", "", "whoami\nroot\n", 0, 0, "", 3)
	if !slices.Contains(args, "--title=snapshell - command ×3") {
		t.Fatalf("code args missing the ×3 title: %v", args)
	}
	// And a plain Alt+2 (count 1) stays the plain title.
	args = zenityArgs(ModeCode, "", "", "whoami\nroot\n", 0, 0, "", 1)
	if !slices.Contains(args, "--title=snapshell - command") {
		t.Fatalf("code args missing the plain title: %v", args)
	}
}
