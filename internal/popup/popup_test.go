package popup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendNote(t *testing.T) {
	dir := t.TempDir()
	if err := appendResult(ModeNote, "found port 445 open", "", dir); err != nil {
		t.Fatalf("appendResult: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	if !strings.Contains(string(data), "found port 445 open") {
		t.Fatalf("blog.md = %q, want the note text", data)
	}
	if strings.Contains(string(data), "**") {
		t.Fatalf("note entry must not have a caption line, got %q", data)
	}
}

func TestAppendEmptyNoteDiscards(t *testing.T) {
	dir := t.TempDir()
	if err := appendResult(ModeNote, "   ", "", dir); err != nil {
		t.Fatalf("appendResult: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "blog.md")); !os.IsNotExist(err) {
		t.Fatal("empty note must not create blog.md")
	}
}

func TestAppendImageWithCaption(t *testing.T) {
	dir := t.TempDir()
	if err := appendResult(ModeImage, "rooted it", "attachments/001.png", dir); err != nil {
		t.Fatalf("appendResult: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	if !strings.Contains(string(data), "**rooted it**") {
		t.Fatalf("want caption line, got %q", data)
	}
	if !strings.Contains(string(data), "![](attachments/001.png)") {
		t.Fatalf("want relative image path, got %q", data)
	}
}

func TestAppendImageNoCaption(t *testing.T) {
	dir := t.TempDir()
	if err := appendResult(ModeImage, "", "attachments/001.png", dir); err != nil {
		t.Fatalf("appendResult: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	if strings.Contains(string(data), "**") {
		t.Fatalf("empty caption must be omitted, got %q", data)
	}
	if !strings.Contains(string(data), "![](attachments/001.png)") {
		t.Fatalf("image must still be appended, got %q", data)
	}
}

func TestAppendCode(t *testing.T) {
	dir := t.TempDir()
	tmp, err := TempCodeFile("┌─[root@box]# nmap -sV 10.10.11.5\n22/tcp open  ssh\n")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp)

	if err := appendResult(ModeCode, "", tmp, dir); err != nil {
		t.Fatalf("appendResult: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	for _, want := range []string{"```console", "nmap -sV 10.10.11.5", "```"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("code entry missing %q:\n%s", want, data)
		}
	}
}

func TestSpawnFallsBackThroughTerminals(t *testing.T) {
	binDir := t.TempDir()
	// Only kitty exists on the isolated PATH → resolveTerminal must pick it
	// over the missing configured alacritty.
	for _, name := range []string{"kitty", "xterm"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)

	term, err := resolveTerminal("alacritty")
	if err != nil {
		t.Fatalf("resolveTerminal: %v", err)
	}
	if term != "kitty" {
		t.Fatalf("want kitty fallback, got %q", term)
	}
}

func TestSpawnNoTerminal(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty
	if _, err := resolveTerminal(""); err == nil {
		t.Fatal("no terminal should error")
	}
}
