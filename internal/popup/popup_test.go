package popup

import (
	"os"
	"path/filepath"
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
	if !strings.Contains(string(data), "**rooted it**") {
		t.Fatalf("want caption line, got %q", data)
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
	for _, want := range []string{"**port scan**", "```console", "nmap -sV 10.10.11.5", "```"} {
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
	if !strings.Contains(string(data), "```console") || strings.Contains(string(data), "**") {
		t.Fatalf("cancelled code capture should append without a caption:\n%s", data)
	}
}

func TestApplyUnknownMode(t *testing.T) {
	if err := applyResult("bogus", Result{}, "", "", t.TempDir()); err == nil {
		t.Fatal("unknown mode should error")
	}
}

func TestZenityArgsImage(t *testing.T) {
	args := zenityArgs(ModeImage, "/sessions/box", "attachments/001.png", "", 520, 300)
	joined := strings.Join(args, " ")
	for _, want := range []string{"--forms", "--width", "520", "--height", "300",
		"--add-entry=Caption (optional)", "--ok-label=Save", "--cancel-label=Skip"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("image args missing %q: %v", want, args)
		}
	}
}

func TestZenityArgsCodeTruncatesPreview(t *testing.T) {
	long := strings.Repeat("x", 500)
	args := zenityArgs(ModeCode, "", "", long, 0, 0)
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
}

func TestZenityArgsNote(t *testing.T) {
	args := zenityArgs(ModeNote, "", "", "", 560, 400)
	joined := strings.Join(args, " ")
	for _, want := range []string{"--text-info", "--editable", "--ok-label=Save", "--cancel-label=Discard"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("note args missing %q: %v", want, args)
		}
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
