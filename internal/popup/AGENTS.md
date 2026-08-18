# AGENTS.md — internal/popup

Owns: the floating window that appears after a screenshot or command
capture (and for raw notes) to collect an optional caption. It is a real
**GUI window** (a zenity GTK form dialog) — there is no TUI anywhere in
this mode. This is the shared tail-end referenced by the Alt+1 screenshot
flow, the Alt+2 code flow, the Alt+3 raw-note flow, and the Alt+4
selection flow. It also owns moving the dialog to a configured screen
position after it spawns (`[popup].position`).

## Design: one zenity dialog per mode, spawned by the daemon

`popup.Capture(mode, sessionDir, file, text string, width, height int,
font, position, theme string, count int)` is the single entry point. It
blocks until the dialog closes: it launches zenity, reads the caption/note
text from its stdout, then appends the finished entry to
`<sessionDir>/blog.md` via `internal/blog`. It must be run in its own
goroutine by the daemon — a slow or ignored dialog must never block the
daemon or the next hotkey press. `count` is how many commands the capture
spans (code mode only): for `count > 1` the window title gains a
multiplication-sign suffix (`snapshell - command ×2`) so a multi-command
Alt+2 capture is visibly different; other modes ignore it, and the
position mover searches by substring so the suffix doesn't break it.

Because the dialog is spawned **inside the daemon process** (a synchronous
`exec.Command`), there is no separate popup subprocess and no temp file:
the captured code text is passed in memory. `zenity` is the only dependency
(`exec.LookPath("zenity")`, with an error that names the missing binary).

**Theme:** a non-empty `theme` is passed to zenity as the `GTK_THEME`
environment variable (`GTK_THEME=Sweet:dark zenity ...`), re-theming the
dialog at spawn time. The value comes from `[themes].name` in the config;
an unknown theme silently falls back to the system default (GTK's own
behavior — no validation here). Which themes exist is `snapshell
list-themes`'s job, not this package's.

## Dialog by mode

Every mode is `zenity --text-info --editable`: a scrollable text area that
fills the window, so the caption/note input is always large enough to see
everything you type. The mode only changes the `--text` label, the title,
and the cancel label. Do NOT use `zenity --forms` with `--add-entry` — a
one-line entry can't grow and zenity leaves the rest of the window as dead
space (verified against zenity 4.1.90 / libadwaita).

- **`image` mode**: `--text` label describing the captured file (relative
  path + pixel dimensions read from the PNG header via Go's `image`
  package — do not render a thumbnail). The screenshot is not previewed; a
  text label ("📷 attachments/003.png — 1920×1040") is sufficient. The
  user types the caption in the text area. Same three-button contract as
  code mode: Save keeps it with a caption, Skip keeps it without, Cancel
  **deletes the screenshot file** from attachments/ and adds nothing.
- **`code` mode**: `--text` label showing a truncated preview of the
  captured command+output (the full text is what lands in blog.md, the
  preview is just context — truncate, don't grow the window to the size of
  a full tmux dump). The user types the caption in the text area. Code
  mode has **three** buttons: `Save` (keep with caption), `Skip` (keep
  without caption, this is zenity's cancel button → exit 1), and
  `Cancel` (an `--extra-button` that discards the capture entirely — the
  only code path that throws a captured command away).
- **`note` mode**: the text area IS the note. zenity 4.x < 4.2 has no
  `--add-multiline-entry`, so `--text-info --editable` is the multiline
  input.
- All dialogs get `--width`/`--height` (px) from `[popup].width`/`height`
  config (0 = let zenity pick); the height sizes the text area. A `--font`
  (Pango font description) from `[popup].font` is passed when non-empty so
  the text the user types is comfortably readable. Plus a title,
  `--ok-label=Save` and `--cancel-label=Skip` (note mode: `Discard`).
- **Window titles** (`dialogTitle`) describe the thing captured, not the
  action, with a plain hyphen after "snapshell": `snapshell - screenshot`,
  `snapshell - command`, `snapshell - note`, `snapshell - selected text`.
  No em dash, no "add ". The position mover searches by this exact title,
  so the strings are the single source of truth for both `--title` and
  `xdotool search --name`. Code mode appends ` ×N` when the capture spans
  more than one command (`count > 1`), the visible count-prefix feedback
  for Alt+2 + digit.

Dynamic label text is escaped for Pango markup (`& < >` → entities) since
zenity parses labels as markup.

## Positioning (`[popup].position`)

zenity has no `--geometry`, so a configured position is applied by moving
the window after it maps: a background goroutine polls
`xdotool search --name <title>` (the title comes from `dialogTitle(mode)`
and must match the `--title` zenity shows) until the dialog appears, then
`xdotool windowmove` places it. `position.go` owns this.

- Accepts a named preset (`center`, `top-left`, `top-center`, `top-right`,
  `center-left`, `center-right`, `bottom-left`, `bottom-center`,
  `bottom-right`) resolved against `xdotool getdisplaygeometry` and the
  dialog's own size, or explicit `X,Y` pixels from the screen's top-left.
- Empty = leave placement to the window manager (no xdotool needed).
- A configured position is **validated before zenity launches**: invalid
  syntax errors, and a missing `xdotool` errors loudly naming the binary
  (repo subprocess rule) — an unmoved window is never a silent failure.
  The move itself is best-effort after that (polls ~5s; if the window
  never maps, it's simply skipped). After the first `windowmove` the
  result is **verified** against `xdotool getwindowgeometry` and re-moved
  if zenity overwrote it while still mapping — one move is not assumed to
  stick.

## Exit-code → result semantics

zenity exits `0` on the Save/OK button and `1` on cancel/Esc (any other
code, e.g. timeout, is treated as cancelled). `popup` returns:

- `Submitted=true` + caption text (possibly empty) on Save,
- `Submitted=false` on cancel/Esc/close,
- `Aborted=true` on the extra "Cancel" button (image and code modes). For
  an image capture, aborting also deletes the screenshot file that was
  already written to attachments/ — a cancelled capture leaves no trace.

**Extra-button gotcha (verified against zenity 4.1.90):** the documented
exit code for an `--extra-button` is `5`, but real zenity 4.1.90 exits
`1` for it and prints the button's *label* to stdout. `resultFromExit`
therefore treats the result as aborted when (exit == 5) **or** (exit != 0
and trimmed stdout == the extra-button label). Do not rely on the exit
code alone. A caption that happens to equal the label is safe: Save exits
0, so it is never misdetected as an abort.

The submit/skip behavior differs by mode:

- **image/code**: the entry is **always** appended — unless code mode's
  Cancel was pressed. Empty submit or cancel just means "no caption
  line". Losing an already-taken screenshot or capture because the user
  dismissed the caption window would be a bad outcome.
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
