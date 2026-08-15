# AGENTS.md — internal/popup

Owns: the floating window that appears after a screenshot or command
capture (and for raw notes) to collect an optional caption, using
`github.com/charmbracelet/huh`. This is the shared tail-end referenced by
`internal/capture/screenshot`, `internal/capture/tmuxcap`, and the Alt+3
raw-note flow.

## Two halves: spawning the window, and the TUI inside it

Because Alt+1/2/3 are global hotkeys, the popup can't draw into whatever
window currently has focus — it needs its own floating terminal window.
Split this cleanly into two concerns:

1. **Spawning** (done by the daemon, in this package or a small
   `internal/popup/spawn.go`): launch a terminal emulator running
   `snapshell internal-popup --mode <image|code|note> --file <path>`,
   e.g.:
   ```
   alacritty --class snapshell-popup -e snapshell internal-popup --mode image --file attachments/003.png
   ```
   Then use `xdotool search --class snapshell-popup` (poll briefly, the
   window may not exist the instant the process is spawned) followed by
   `xdotool windowmove`/`windowsize` and `windowactivate` (or `wmctrl`
   equivalents) to center it at the configured size
   (`[popup].width_cells`/`height_cells` from config) and give it focus.
   - Terminal emulator is configurable (`[popup].terminal`), default
     `alacritty`. If not found on `$PATH`, fall back through a short list
     (`kitty`, `xterm` as last resort) and log which one was used.
2. **The TUI itself** (`cmd/snapshell`'s `internal-popup` subcommand,
   logic lives here in `internal/popup`): a `huh` form, mode-dependent
   layout (below). On submit, it writes the caption (possibly empty) to
   the session's `blog.md` via `internal/blog`, then exits — the process
   exiting is what closes the floating window, no separate "close window"
   step needed.

## Layout by mode

- **`image` mode**: two-region layout — one side shows a label with the
  captured file's path and, if easily obtainable, its pixel dimensions
  (e.g. via Go's `image` package reading the PNG header) — do not attempt
  to render an actual thumbnail, terminals can't reliably do that; a
  clear text label ("📷 attachments/003.png — 1920×1040") is sufficient.
  The other side is a `huh.NewText()` caption field.
- **`code` mode**: same two-region idea — one side renders the captured
  command+output text verbatim (scrollable via huh/bubbletea's own
  scrolling if it's long, don't truncate silently), the other side is the
  caption field.
- **`note` mode**: no preview region at all — a single full-width
  `huh.NewText()`.
- If `huh`'s layout primitives don't cleanly support a literal
  side-by-side split, stack preview-above/caption-below instead. Priority
  is both being visible without extra navigation, exact orientation is a
  secondary concern.

## Submit / skip behavior

- Empty submit (user hits the form's submit key with no text typed) =
  "skip caption" for image/code mode: the image or code block still gets
  appended to `blog.md`, just with no caption line.
- Empty submit in `note` mode = discard entirely, nothing appended to
  `blog.md`. This is different from image/code mode — make sure the two
  aren't accidentally given identical skip behavior.
- Use huh's own built-in keybindings for submit/cancel rather than
  inventing custom ones — don't fight the library's defaults.
- Esc / window closed without submitting: treat the same as skip (for
  image/code, the capture itself already happened and should still be
  recorded without a caption — losing an already-taken screenshot because
  the user closed the caption box would be a bad outcome). Only `note`
  mode loses content on cancel, since there's nothing captured yet beyond
  the text itself.

## Caption placement in blog.md

Actual Markdown formatting (where exactly the caption line goes relative
to the image/code block, timestamp comment format, etc.) is
`internal/blog`'s responsibility, not this package's — this package's job
ends at "here is the caption string (possibly empty) and here is the
path/text that was captured," handed off as a simple function call into
`internal/blog`.
