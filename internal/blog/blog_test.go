package blog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEntries(t *testing.T, dir string, entries ...Entry) string {
	t.Helper()
	for _, e := range entries {
		if err := Append(dir, e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "blog.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestHeaderCreatedOnce(t *testing.T) {
	dir := t.TempDir()
	out := writeEntries(t, dir, Entry{Kind: KindNote, NoteText: "hello"})
	if !strings.HasPrefix(out, "# "+filepath.Base(dir)+"\n") {
		t.Fatalf("missing header, got:\n%s", out)
	}
	// Second append must not duplicate the header.
	out = writeEntries(t, dir, Entry{Kind: KindNote, NoteText: "world"})
	if strings.Count(out, "# ") != 1 {
		t.Fatalf("header duplicated:\n%s", out)
	}
}

func TestImageEntryNoCaption(t *testing.T) {
	dir := t.TempDir()
	out := writeEntries(t, dir, Entry{Kind: KindImage, ImagePath: "attachments/001.png"})
	if strings.Contains(out, "**") {
		t.Fatalf("no caption should mean no bold line:\n%s", out)
	}
	if !strings.Contains(out, "![](attachments/001.png)") {
		t.Fatalf("missing image line:\n%s", out)
	}
	if strings.Contains(out, "<!--") {
		t.Fatalf("no timestamp comment should be emitted:\n%s", out)
	}
}

func TestImageEntryWithCaption(t *testing.T) {
	dir := t.TempDir()
	out := writeEntries(t, dir, Entry{Kind: KindImage, Caption: "port 80 open", ImagePath: "attachments/002.png"})
	if !strings.Contains(out, "port 80 open\n") {
		t.Fatalf("caption line missing:\n%s", out)
	}
	if strings.Contains(out, "**") {
		t.Fatalf("caption must not be bolded:\n%s", out)
	}
	if !strings.Contains(out, "![](attachments/002.png)") {
		t.Fatalf("image line missing:\n%s", out)
	}
	// caption must sit directly above the image
	if idx := strings.Index(out, "port 80 open\n"); !strings.HasPrefix(out[idx:], "port 80 open\n![](attachments/002.png)") {
		t.Fatalf("caption not directly above image:\n%s", out)
	}
}

func TestCodeEntry(t *testing.T) {
	dir := t.TempDir()
	text := "$ nmap -sV 10.10.10.1\nPORT   STATE SERVICE\n80/tcp open  http\n"
	out := writeEntries(t, dir, Entry{Kind: KindCode, Caption: "scan", CodeText: text})
	if !strings.Contains(out, "```bash\n"+text+"```") {
		t.Fatalf("code block malformed:\n%s", out)
	}
	if !strings.Contains(out, "scan\n```bash") {
		t.Fatalf("caption not directly above code block:\n%s", out)
	}
}

func TestCodeEntryCaptionBelow(t *testing.T) {
	dir := t.TempDir()
	text := "$ whoami\nroot\n"
	out := writeEntries(t, dir, Entry{Kind: KindCode, Caption: "whoami says root", CodeText: text, CaptionAfter: true})
	if !strings.Contains(out, "```bash\n"+text+"```\nwhoami says root") {
		t.Fatalf("caption not directly below code block:\n%s", out)
	}
}

func TestImageEntryCaptionBelow(t *testing.T) {
	dir := t.TempDir()
	out := writeEntries(t, dir, Entry{Kind: KindImage, Caption: "initial foothold", ImagePath: "attachments/003.png", CaptionAfter: true})
	if !strings.Contains(out, "![](attachments/003.png)\ninitial foothold") {
		t.Fatalf("caption not directly below image:\n%s", out)
	}
}

func TestCodeFenceEscalation(t *testing.T) {
	dir := t.TempDir()
	text := "cat file.md\n```\nraw markdown\n```\n"
	out := writeEntries(t, dir, Entry{Kind: KindCode, CodeText: text})
	if !strings.Contains(out, "````text") {
		t.Fatalf("fence should widen past embedded backticks:\n%s", out)
	}
	// The opening fence must be the widened 4-backtick fence, and the
	// embedded triple-backtick lines must be preserved verbatim inside it.
	for _, line := range strings.Split(out, "\n") {
		if line == "```text" {
			t.Fatalf("narrow 3-backtick fence used despite embedded backticks:\n%s", out)
		}
	}
	if !strings.Contains(out, "cat file.md\n```\nraw markdown\n```") {
		t.Fatalf("embedded content not preserved verbatim:\n%s", out)
	}
}

func TestNoteEntry(t *testing.T) {
	dir := t.TempDir()
	out := writeEntries(t, dir, Entry{Kind: KindNote, NoteText: "Found creds in source."})
	if !strings.Contains(out, "Found creds in source.") {
		t.Fatalf("note text missing:\n%s", out)
	}
	if strings.Contains(out, "**Found") {
		t.Fatalf("note text must not be bolded:\n%s", out)
	}
}

func TestEntriesSeparatedByBlankLine(t *testing.T) {
	dir := t.TempDir()
	out := writeEntries(t, dir,
		Entry{Kind: KindNote, NoteText: "one"},
		Entry{Kind: KindNote, NoteText: "two"},
	)
	if !strings.Contains(out, "one\n\ntwo\n") {
		t.Fatalf("entries not separated by exactly one blank line:\n%q", out)
	}
}

func TestResumeAppends(t *testing.T) {
	dir := t.TempDir()
	writeEntries(t, dir, Entry{Kind: KindNote, NoteText: "first"})
	// Simulate a resumed session: append again, must not rewrite.
	writeEntries(t, dir, Entry{Kind: KindNote, NoteText: "second"})
	data, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	if strings.Count(string(data), "first") != 1 || strings.Count(string(data), "second") != 1 {
		t.Fatalf("append not idempotent:\n%s", data)
	}
}
