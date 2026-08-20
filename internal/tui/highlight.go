// Code-card syntax coloring: the detail preview renders the captured text
// through chroma using the same palette glamour applies to ```bash blocks,
// so what the user sees in the preview matches the blog view (and any other
// markdown viewer) token for token.

package tui

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

// The chroma theme this package registers for the code preview. It is built
// from glamour's own code-block palette (see codePreviewStyle) so the detail
// preview uses exactly the colors the markdown previews produce.
const codePreviewTheme = "snapshell-charm"

var registerCodePreviewTheme = sync.OnceFunc(func() {
	if s := codePreviewStyle(); s != nil {
		styles.Register(s)
	}
})

// codePreviewStyle builds a chroma style from glamour's dark/light code-block
// palette, mirroring how glamour turns its JSON style config into a chroma
// theme (ansi.StylePrimitive fields -> chroma StyleEntry strings).
func codePreviewStyle() *chroma.Style {
	cfg := glamourstyles.DarkStyleConfig
	if !lipgloss.HasDarkBackground() {
		cfg = glamourstyles.LightStyleConfig
	}
	c := cfg.CodeBlock.Chroma
	if c == nil {
		return nil
	}
	return chroma.MustNewStyle(codePreviewTheme, chroma.StyleEntries{
		chroma.Text:                chromaStyleEntry(c.Text),
		chroma.Error:               chromaStyleEntry(c.Error),
		chroma.Comment:             chromaStyleEntry(c.Comment),
		chroma.CommentPreproc:      chromaStyleEntry(c.CommentPreproc),
		chroma.Keyword:             chromaStyleEntry(c.Keyword),
		chroma.KeywordReserved:     chromaStyleEntry(c.KeywordReserved),
		chroma.KeywordNamespace:    chromaStyleEntry(c.KeywordNamespace),
		chroma.KeywordType:         chromaStyleEntry(c.KeywordType),
		chroma.Operator:            chromaStyleEntry(c.Operator),
		chroma.Punctuation:         chromaStyleEntry(c.Punctuation),
		chroma.Name:                chromaStyleEntry(c.Name),
		chroma.NameBuiltin:         chromaStyleEntry(c.NameBuiltin),
		chroma.NameTag:             chromaStyleEntry(c.NameTag),
		chroma.NameAttribute:       chromaStyleEntry(c.NameAttribute),
		chroma.NameClass:           chromaStyleEntry(c.NameClass),
		chroma.NameConstant:        chromaStyleEntry(c.NameConstant),
		chroma.NameDecorator:       chromaStyleEntry(c.NameDecorator),
		chroma.NameException:       chromaStyleEntry(c.NameException),
		chroma.NameFunction:        chromaStyleEntry(c.NameFunction),
		chroma.NameOther:           chromaStyleEntry(c.NameOther),
		chroma.Literal:             chromaStyleEntry(c.Literal),
		chroma.LiteralNumber:       chromaStyleEntry(c.LiteralNumber),
		chroma.LiteralDate:         chromaStyleEntry(c.LiteralDate),
		chroma.LiteralString:       chromaStyleEntry(c.LiteralString),
		chroma.LiteralStringEscape: chromaStyleEntry(c.LiteralStringEscape),
		chroma.GenericDeleted:      chromaStyleEntry(c.GenericDeleted),
		chroma.GenericEmph:         chromaStyleEntry(c.GenericEmph),
		chroma.GenericInserted:     chromaStyleEntry(c.GenericInserted),
		chroma.GenericStrong:       chromaStyleEntry(c.GenericStrong),
		chroma.GenericSubheading:   chromaStyleEntry(c.GenericSubheading),
		chroma.Background:          chromaStyleEntry(c.Background),
	})
}

// chromaStyleEntry mirrors glamour's chromaStyle() conversion of a
// StylePrimitive into a chroma style-entry string ("#color bg:#bg bold").
func chromaStyleEntry(p ansi.StylePrimitive) string {
	var s string
	if p.Color != nil {
		s = *p.Color
	}
	if p.BackgroundColor != nil {
		if s != "" {
			s += " "
		}
		s += "bg:" + *p.BackgroundColor
	}
	if p.Italic != nil && *p.Italic {
		if s != "" {
			s += " "
		}
		s += "italic"
	}
	if p.Bold != nil && *p.Bold {
		if s != "" {
			s += " "
		}
		s += "bold"
	}
	if p.Underline != nil && *p.Underline {
		if s != "" {
			s += " "
		}
		s += "underline"
	}
	return s
}

// highlightCode renders code with syntax coloring using the glamour-matched
// palette, returning plain text when the language is unknown or anything
// fails (never a panic, never mangled input). The output uses the same
// terminal256 formatter glamour does, so it is safe through the ANSI-aware
// viewport and fillPane.
func highlightCode(code, lang string) string {
	registerCodePreviewTheme()
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}
	var buf strings.Builder
	if err := formatters.Get("terminal256").Format(&buf, styles.Get(codePreviewTheme), it); err != nil {
		return code
	}
	return buf.String()
}
