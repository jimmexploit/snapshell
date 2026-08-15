# AGENTS.md — internal/popup

Owns: the floating window that appears after a screenshot or command
capture (and for raw notes) to collect an optional caption. It is a real
**GUI window** (a zenity GTK form dialog) — there is no TUI anywhere in
this mode. This is the shared tail-end referenced by the Alt+1 screenshot
flow, the Alt+2 code flow, and the Alt+3 raw-note flow.

## Design: one zenity dialog per mode, spawned by the daemon

`popup.Capture(mode, sessionDir, file, text string, width, height int)` is
the single entry point. It blocks until the dialog closes: it launches
zenity, reads the caption/note text from its stdout, then appends the
finished entry to `<sessionDir>/blog.md` via `internal/blog`. It must be
run in its own goroutine by the daemon — a slow or ignored dialog must
never block the daemon or the next hotkey press.

Because the dialog is spawned **inside the daemon process** (a synchronous
`exec.Command`), there is no separate popup subprocess and no temp file:
the captured code text is passed in memory. `zenity` is the only dependency
(`exec.LookPath("zenity")`, with an error that names the missing binary).

## Dialog by mode

- **`image` mode**: `zenity --forms` with a `--text` label describing the
  captured file (relative path + pixel dimensions read from the PNG header
  via Go's `image` package — do not render a thumbnail) and a single
  `--add-entry="Caption (optional)"`. The screenshot is not previewed; a
  text label ("📷 attachments/003.png — 1920×1040") is sufficient.
- **`code` mode**: `zenity --forms` with a `--text` label showing a
  truncated preview of the captured command+output (the full text is what
  lands in blog.md, the preview is just context — truncate, don't grow the
  window to the size of a full tmux dump) and a single caption entry.
- **`note` mode**: `zenity --text-info --editable` — a scrollable text
  area where the user types the note (zenity 4.x < 4.2 has no
  `--add-multiline-entry`, so `--text-info --editable` is the multiline
  input).
- All dialogs get `--width`/`--height` (px) from `[popup].width`/`height`
  config (0 = let zenity pick), plus a title, `--ok-label=Save` and
  `--cancel-label=Skip` (note mode: `Discard`).

Dynamic label text is escaped for Pango markup (`& < >` → entities) since
zenity parses labels as markup.

## Exit-code → result semantics

zenity exits `0` on the Save/OK button and `1` on cancel/Esc (any other
code, e.g. timeout, is treated as cancelled). `popup` returns:

- `Submitted=true` + caption text (possibly empty) on Save,
- `Submitted=false` on cancel/Esc/close.

The submit/skip behavior differs by mode:

- **image/code**: the entry is **always** appended. Empty submit or
  cancel just means "no caption line". Losing an already-taken screenshot
  or capture because the user dismissed the caption window would be a bad
  outcome.
- **note**: only appended when `Submitted && text != ""`. Cancelled or
  empty note = discarded entirely — nothing was captured yet beyond the
  text itself.

`Capture` returns an error **only** on an infrastructure failure (zenity
missing, spawn failed). In that case nothing is appended and the daemon
decides the fallback (image/code: append without a caption + notify; note:
just notify). A user pressing cancel is not an error.

## Caption placement in blog.md

Actual Markdown formatting (caption line relative to the image/code block,
timestamp comment format, etc.) is `internal/blog`'s responsibility, not
this package's — this package's job ends at "here is the caption string
(possibly empty) and here is the path/text that was captured," handed off
as a simple function call into `internal/blog`.

## What NOT to do here

- No TUI/terminal code paths. Do not reintroduce `huh`, `bubbles`,
  `lipgloss`, or terminal-emulator spawning — the caption window is a
  zenity GUI window, period.
- Don't run zenity in the background expecting it to write blog.md later —
  the daemon spawns it synchronously (in its capture goroutine), reads the
  result, and appends itself. A single `Capture` call owns the whole
  window→append tail end.
- Don't let a slow dialog block the daemon — `Capture` is blocking by
  design, so the daemon must call it from a per-capture goroutine (see
  `internal/daemon/AGENTS.md`), never from the hotkey event loop.
