package blog

import (
	"regexp"
	"strings"
)

// DetectLang inspects captured text and returns the language tag for its
// Markdown code fence. It distinguishes terminal sessions (shell prompts)
// from raw source code, so Alt+2 captures read as shell sessions while
// Alt+4 selections of an actual file get the file's language.
//
// Heuristics, in order:
//
//   - a strong shell prompt line (powerline box chars, `$ `, `❯ `, `➜ `,
//     `> `, or a zsh `% `) anywhere in the text → "bash"
//   - a `#!` shebang on the first line names the interpreter (python,
//     bash/sh, node, perl, ruby, ...)
//   - Go source: a `package X` line plus a `func`/`type`/`var`/`const`
//     declaration → "go"
//   - Python source: lines starting with `def`/`class`/`import`/`from`,
//     or an `if __name__ == "__main__"` guard → "python"
//   - YAML: a `---` document marker or a leading `key: value` line → "yaml"
//   - JSON: text that starts with `{`/`[` and ends with the matching
//     bracket → "json"
//   - HTML: `<!DOCTYPE` or `<html` → "html"
//   - TOML: a `[section]` header plus a `key = value` line → "toml"
//   - a `# ` root-shell prompt (checked last, because `# comment` lines in
//     source code look identical) → "bash"
//   - anything else → "text"
func DetectLang(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return "text"
	}

	lines := strings.Split(t, "\n")

	if hasStrongPrompt(lines) {
		return "bash"
	}
	if shebang := shebangLang(lines[0]); shebang != "" {
		return shebang
	}

	switch {
	case looksLikeGo(t):
		return "go"
	case looksLikePython(t):
		return "python"
	case looksLikeYAML(lines):
		return "yaml"
	case looksLikeJSON(t):
		return "json"
	case looksLikeHTML(t):
		return "html"
	case looksLikeTOML(lines):
		return "toml"
	case hasRootPrompt(lines):
		return "bash"
	}
	return "text"
}

// hasStrongPrompt reports a shell prompt that cannot be confused with a
// comment or other prose line: powerline box characters, `$ `, `❯ `,
// `➜ `, `> `, or a zsh `% ` sigil followed by a command.
func hasStrongPrompt(lines []string) bool {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		// Powerline / multi-line prompts begin with box-drawing characters.
		if strings.HasPrefix(t, "┌") || strings.HasPrefix(t, "└") {
			return true
		}
		t = strings.TrimLeft(t, "─╼")
		for _, sigil := range []string{"$ ", "❯ ", "➜ ", "> ", "% "} {
			if strings.HasPrefix(t, sigil) {
				return true
			}
		}
	}
	return false
}

// hasRootPrompt treats a `# ` line as a root shell prompt. It is checked
// only after source-code detection has already failed, so `# comment`
// lines in real code never trigger it.
func hasRootPrompt(lines []string) bool {
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "# ") {
			return true
		}
	}
	return false
}

// shebangLang maps the first interpreter word of a `#!` line to a language
// tag, or "" when the line isn't a shebang.
func shebangLang(firstLine string) string {
	line := strings.TrimSpace(firstLine)
	if !strings.HasPrefix(line, "#!") {
		return ""
	}
	words := strings.Fields(line[2:])
	if len(words) == 0 {
		return ""
	}
	bin := words[0]
	// `/usr/bin/env python3` → env is the binary, python3 the first arg.
	if strings.Contains(bin, "env") && len(words) > 1 {
		bin = words[1]
	}
	base := bin
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	switch strings.ToLower(base) {
	case "python", "python2", "python3", "pypy", "pypy3":
		return "python"
	case "sh", "bash", "zsh", "dash", "ksh", "fish":
		return "bash"
	case "node", "nodejs", "deno":
		return "javascript"
	case "perl":
		return "perl"
	case "ruby":
		return "ruby"
	case "go", "gofmt":
		return "go"
	}
	return base
}

var (
	goPkgRE    = regexp.MustCompile(`(?m)^\s*package\s+[a-zA-Z0-9_]+`)
	goDeclRE   = regexp.MustCompile(`(?m)^\s*(func|type|var|const)\s+[a-zA-Z0-9_]`)
	pyDefRE    = regexp.MustCompile(`(?m)^\s*(def|class)\s+[a-zA-Z0-9_]+`)
	pyImportRE = regexp.MustCompile(`(?m)^\s*(import|from)\s+[a-zA-Z0-9_.]+`)
	pyMainRE   = regexp.MustCompile(`(?m)if\s+__name__\s*==`)
	yamlKeyRE  = regexp.MustCompile(`(?m)^[a-zA-Z0-9_./-]+:\s`)
	tomlSecRE  = regexp.MustCompile(`(?m)^\[[a-zA-Z0-9_."]+\]\s*$`)
)

func looksLikeGo(t string) bool {
	return goPkgRE.MatchString(t) && goDeclRE.MatchString(t)
}

func looksLikePython(t string) bool {
	return pyDefRE.MatchString(t) || pyImportRE.MatchString(t) || pyMainRE.MatchString(t)
}

func looksLikeYAML(lines []string) bool {
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if s == "" {
			continue
		}
		if s == "---" {
			return true
		}
		// A `key: value` line early on, without `=` (which would suggest
		// TOML/ini) and without `{`/`[` (which would suggest JSON).
		if yamlKeyRE.MatchString(l) && !strings.Contains(l, "=") &&
			!strings.Contains(l, "{") && !strings.Contains(l, "[") {
			return true
		}
		if i > 8 {
			return false
		}
	}
	return false
}

func looksLikeJSON(t string) bool {
	if strings.HasPrefix(t, "{") {
		return strings.HasSuffix(t, "}")
	}
	if strings.HasPrefix(t, "[") {
		return strings.HasSuffix(t, "]")
	}
	return false
}

func looksLikeHTML(t string) bool {
	low := strings.ToLower(t)
	return strings.Contains(low, "<!doctype html") || strings.Contains(low, "<html")
}

func looksLikeTOML(lines []string) bool {
	hasSection := false
	hasAssign := false
	for _, l := range lines {
		s := strings.TrimSpace(l)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if tomlSecRE.MatchString(s) {
			hasSection = true
		}
		if strings.Contains(s, "=") {
			hasAssign = true
		}
	}
	return hasSection && hasAssign
}
