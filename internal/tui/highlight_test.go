package tui

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"

	"snapshell/internal/inventory"
)

// TestCodePreviewStyleMatchesGlamour verifies the chroma style built from
// glamour's config is non-nil and carries a real color for at least one
// token — the preview would otherwise render everything flat.
func TestCodePreviewStyleMatchesGlamour(t *testing.T) {
	style := codePreviewStyle()
	if style == nil {
		t.Fatal("codePreviewStyle() returned nil")
	}
	if style.Get(chroma.Keyword).Colour.IsSet() && style.Get(chroma.Name).Colour.IsSet() &&
		style.Get(chroma.LiteralString).Colour.IsSet() {
		return
	}
	// At least one of the core token classes must be colored.
	if !style.Get(chroma.Keyword).Colour.IsSet() &&
		!style.Get(chroma.Name).Colour.IsSet() &&
		!style.Get(chroma.LiteralString).Colour.IsSet() {
		t.Fatal("codePreviewStyle() produced a flat (uncolored) palette")
	}
}

func TestHighlightCodeEmitsAnsi(t *testing.T) {
	out := highlightCode("for i in 1 2 3; do echo hi; done\n", "bash")
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("highlighted bash should contain ANSI escapes, got:\n%q", out)
	}
	// The source text is preserved verbatim (coloring adds, never removes).
	for _, want := range []string{"for", "echo", "hi", "done"} {
		if !strings.Contains(out, want) {
			t.Fatalf("highlighted output lost %q:\n%q", want, out)
		}
	}
}

func TestHighlightCodeFallbackDegradesGracefully(t *testing.T) {
	// Unknown language resolves to chroma's fallback lexer: no panic, no
	// mangled output, and the source text survives intact. The base Text
	// color may still wrap each line — that is the normal prose rendering.
	out := highlightCode("arbitrary prose line\nanother one\n", "totally-not-a-language")
	for _, want := range []string{"arbitrary prose line", "another one"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fallback output lost %q:\n%q", want, out)
		}
	}
}

func TestTerminal256FormatterAvailable(t *testing.T) {
	// The formatter highlightCode uses must exist; a missing formatter would
	// silently fall back to plain output.
	if formatters.Get("terminal256") == nil {
		t.Fatal("terminal256 formatter not registered")
	}
}

// TestDetailPreviewSyntaxColored drives the model path: a selected code card
// must populate the detail viewport with colored (ANSI) content while image
// cards stay plain.
func TestDetailPreviewSyntaxColored(t *testing.T) {
	cards := []inventory.Card{
		{ID: 1, Kind: inventory.KindCode, Text: "sudo nmap -sV -p 80 10.10.10.1\n80/tcp open http\n"},
		{ID: 2, Kind: inventory.KindImage, Path: "attachments/001.png"},
	}
	m, _ := setupModel(t, cards)
	m = step(t, m, m.refreshList())

	if m.detailContent == "" {
		t.Fatal("code card should populate the detail viewport")
	}
	if !strings.Contains(m.detailContent, "\x1b[") {
		t.Fatalf("code detail preview should be syntax-colored:\n%q", m.detailContent)
	}
	// Selecting the image card clears the colored code content.
	m, _ = upd(t, m, key("down"))
	if m.detailContent != "" {
		t.Fatalf("image card must not keep code detail content, got %q", m.detailContent)
	}
}
