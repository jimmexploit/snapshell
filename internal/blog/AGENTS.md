# AGENTS.md — internal/blog

Owns: appending correctly-formatted entries to a session's `blog.md`. This
is the single place that touches `blog.md` — no other package should open
that file directly, everything routes through here so the formatting
contract stays in one place.

## File handling

- Path: `<session_root>/<session_name>/blog.md`.
- **Append-only.** Never read-modify-rewrite the whole file. Open with
  `O_APPEND|O_CREATE|O_WRONLY`.
- Create the file with a minimal header if it doesn't exist yet (first
  entry of a new session), e.g.:
  ```markdown
  # <session_name>

  ```
  Do not add a table of contents, auto-numbering, or any other structure
  beyond this — keep it simple, entries just accumulate below.

## Entry format

Every entry is preceded by a hidden timestamp comment (not rendered by
Markdown viewers, present purely for traceability/debugging):
```
<!-- 2026-08-15T14:02:11+02:00 -->
```
Use RFC3339 with the local timezone offset, not UTC-normalized — the user
will be scanning these by eye occasionally, local time is more useful.

### Image entry (from Alt+1)
```
<!-- 2026-08-15T14:02:11+02:00 -->
<caption text, if any>
![](attachments/003.png)
```
If no caption was given, omit the caption line entirely — don't leave a
blank line. Captions are **plain text, not bolded** — the `**...**` wrapper
was dropped as a deliberate formatting choice.

### Code entry (from Alt+2)
````
<!-- 2026-08-15T14:03:40+02:00 -->
<caption text, if any>
```bash
<captured text verbatim>
```
````
Same rule: omit the caption line if empty. The language tag after the
opening fence is **chosen by `DetectLang`** (see below), not hardcoded.

### Note entry (from Alt+3)
```
<!-- 2026-08-15T14:05:02+02:00 -->
<note text as a plain paragraph, no special formatting applied>
```

## Code block language detection

`DetectLang(text string) string` in `lang.go` picks the language tag for
a code fence by inspecting the captured text. It must distinguish a
terminal session (Alt+2 / Alt+4-of-a-shell) from raw source code
(Alt+4-of-a-file). Rules, in order:

1. **Strong shell prompt** anywhere (powerline box chars `┌`/`└`, or a
   line starting with `$ `, `❯ `, `➜ `, `> `, or a zsh `% `) → `bash`.
2. **Shebang** on the first line (`#!/usr/bin/env python3` → `python`,
   `#!/bin/bash` → `bash`, `node` → `javascript`, perl/ruby/go also
   mapped, unknown → the interpreter basename).
3. **Content detection** (only when no prompt is present, so a shell
   session that merely displayed a file still reads as a session):
   Go (`package X` + `func`/`type`/`var`/`const`) → `go`; Python
   (`def`/`class`/`import`/`from` at line start, or `if __name__ ==`) →
   `python`; YAML (`---` or an early `key: value` line without `=`/`{`/`[`)
   → `yaml`; JSON (`{`…`}` or `[`…`]`) → `json`; HTML (`<!DOCTYPE`/`<html`)
   → `html`; TOML (`[section]` + `key = value`) → `toml`.
4. **Root prompt** — a `# ` line → `bash`. Checked *last* on purpose,
   because `# comment` lines in source code look identical; content
   detection runs first so real code wins.
5. Everything else → `text`.

Keep detection pure and unit-testable (no I/O, no side effects).

## Formatting rules

- Always separate entries with exactly one blank line before and after,
  so the raw Markdown source stays readable, not just the rendered
  output.
- Image paths are always relative (`attachments/NNN.png`), never absolute
  — this is what makes a session folder portable/zippable. Verify this by
  constructing the path relative to `blog.md`'s own directory, not by
  string-trimming an absolute path (fragile if the session root ever
  moves).
- Do not escape or otherwise mangle the captured command/output text
  inside the code fence — if the captured text itself happens to contain
  a line of three backticks (rare, but possible if the user cat'd a
  markdown file), detect that and use four backticks for the fence
  instead of breaking the block. Check for this rather than assuming it
  won't happen.
- Caption text goes through as plain text; no need to escape Markdown
  special characters in it (bold/italics if the user typed them are fine
  to render, this is a personal blog not user-generated HTML needing
  sanitization).

## Public function shape (guidance, not gospel — adjust to fit real code)

```go
package blog

type Entry struct {
    Kind       EntryKind // Image | Code | Note
    Caption    string    // may be empty
    ImagePath  string    // relative path, Image entries only
    CodeText   string    // Code entries only
    NoteText   string    // Note entries only
}

func Append(sessionDir string, e Entry) error
```
Keep this the *only* write path into `blog.md` so future changes to the
format (e.g. if caption placement is made configurable later, per the
original spec discussion) only need to change one function.
