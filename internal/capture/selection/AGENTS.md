# AGENTS.md — internal/capture/selection

Owns: reading the text currently selected on screen, falling back to the
clipboard. This is the Alt+4 flow's first half (everything up to "we have
the text") — the caption popup / blog-append tail is the same shared
`appendCodeEntry` path used by Alt+2.

## Behavior

`Read() (text string, err error)`:

1. Try the X11 PRIMARY selection first (`xclip -o -selection primary`) —
   that's the "text you just highlighted" semantic.
2. If PRIMARY is empty or unavailable, fall back to the CLIPBOARD
   (`xclip -o -selection clipboard`).
3. If both are empty/unavailable, return the sentinel `ErrEmpty` (a
   `notify-send` informs the user; it is not an error in the daemon
   flow).

An empty selection is detected by xclip exiting non-zero and/or producing
whitespace-only output — real xclip prints `Error: target STRING not
available` to stderr and exits 1 when nothing is selected. Treat any
non-zero exit from a *read* as "empty", not as a failure (there is no
meaningful read-failure mode other than "nothing there").

## Rules

- Check `exec.LookPath("xclip")` first; if missing, return an error that
  names the missing binary ("xclip not found on PATH — required for
  selection/clipboard capture") — never a raw exec error or panic.
- Preserve internal newlines and leading whitespace of the captured text
  — it lands verbatim in a code fence. Only trim the single trailing
  newline (the shell-echo artifact); do not collapse or reflow the text.
- No timeout on the xclip call: the selection is read synchronously at
  hotkey time and the calls complete immediately.

## Tests

The package is unit-tested against an `xclip` shim installed on a fake
`PATH`: primary-wins, primary-empty-then-clipboard, both-empty →
`ErrEmpty`, and missing-xclip error paths.
