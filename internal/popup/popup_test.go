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

func TestCommandArgsByTerminal(t *testing.T) {
	argv := []string{"/home/u/bin/snapshell", "internal-popup",
		"--mode", "code", "--file", "/tmp/a b.txt", "--session-dir", "/sessions/my box"}

	alac := commandArgs("alacritty", argv)
	wantAlac := []string{"-e", "/home/u/bin/snapshell", "internal-popup",
		"--mode", "code", "--file", "/tmp/a b.txt", "--session-dir", "/sessions/my box"}
	if strings.Join(alac, "\x00") != strings.Join(wantAlac, "\x00") {
		t.Fatalf("alacritty args = %q, want %q", alac, wantAlac)
	}

	mt := commandArgs("mate-terminal", argv)
	wantMT := []string{"-e", "'/home/u/bin/snapshell' 'internal-popup' '--mode' 'code' '--file' '/tmp/a b.txt' '--session-dir' '/sessions/my box'"}
	if strings.Join(mt, "\x00") != strings.Join(wantMT, "\x00") {
		t.Fatalf("mate-terminal args = %q, want %q", mt, wantMT)
	}

	gnome := commandArgs("gnome-terminal", argv)
	wantGnome := []string{"--disable-factory", "--", "/home/u/bin/snapshell", "internal-popup",
		"--mode", "code", "--file", "/tmp/a b.txt", "--session-dir", "/sessions/my box"}
	if strings.Join(gnome, "\x00") != strings.Join(wantGnome, "\x00") {
		t.Fatalf("gnome-terminal args = %q, want %q", gnome, wantGnome)
	}

	xfce := commandArgs("xfce4-terminal", argv)
	if xfce[0] != "-x" || xfce[1] != "/home/u/bin/snapshell" {
		t.Fatalf("xfce4-terminal should pass argv directly after -x, got %q", xfce)
	}

	konsole := commandArgs("konsole", argv)
	if konsole[0] != "-e" || konsole[1] != "/home/u/bin/snapshell" {
		t.Fatalf("konsole should pass argv directly after -e, got %q", konsole)
	}
}

func TestClassAndDimensionFlags(t *testing.T) {
	if got := classFlags("mate-terminal"); !strings.Contains(strings.Join(got, " "), "--class=snapshell-popup") {
		t.Fatalf("mate-terminal class flags = %q, want --class=snapshell-popup", got)
	}
	if got := dimensionsFlags("mate-terminal", 100, 30); len(got) != 1 || got[0] != "--geometry=100x30" {
		t.Fatalf("mate-terminal dims = %q, want --geometry=100x30", got)
	}
	if got := dimensionsFlags("konsole", 100, 30); len(got) != 2 || got[1] != "100x30" {
		t.Fatalf("konsole dims = %q, want --geometry 100x30", got)
	}
	if got := dimensionsFlags("gnome-terminal", 80, 24); len(got) != 1 || got[0] != "--geometry=80x24" {
		t.Fatalf("gnome-terminal dims = %q, want --geometry=80x24", got)
	}
	if got := dimensionsFlags("xfce4-terminal", 80, 24); len(got) != 1 || got[0] != "--geometry=80x24" {
		t.Fatalf("xfce4-terminal dims = %q, want --geometry=80x24", got)
	}
	if got := classFlags("konsole"); len(got) == 0 || got[0] != "--separate" {
		t.Fatalf("konsole class flags = %q, want --separate first", got)
	}
}

func TestShellQuoteArgsEscapesSingleQuote(t *testing.T) {
	got := shellQuoteArgs([]string{"a'b", "plain"})
	want := "'a'\\''b' 'plain'"
	if got != want {
		t.Fatalf("shellQuoteArgs = %q, want %q", got, want)
	}
}
