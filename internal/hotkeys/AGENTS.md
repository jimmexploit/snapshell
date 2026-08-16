# AGENTS.md — internal/hotkeys

Owns: grabbing Alt+1, Alt+2, Alt+3 as **global** X11 hotkeys, i.e. they
fire regardless of which window currently has input focus.

## Approach

Use real X11 key grabbing against the root window
(`github.com/jezek/xgb` + `github.com/jezek/xgbutil` with its `keybind`
and `xevent` sub-packages, or an equivalent well-maintained Go X11
binding — pick one and use it consistently, don't mix libraries).

Do **not**:
- Rely on the user manually configuring MATE's own keyboard shortcuts
  panel to run a script. The daemon must grab the keys itself at the X11
  level on startup.
- Use a polling/global-input-hook hack (e.g. reading `/dev/input` events
  directly) — that requires root/uinput permissions and is unnecessary on
  X11 when proper key grabbing is available.

## Behavior

- Expose a simple interface to the rest of the daemon:
  ```go
  type Handler func()
  func GrabAll(combos map[string]string, handlers map[string]Handler) (unregister func(), err error)
  ```
  `combos` maps a user-facing name (`"screenshot"`, `"code"`, `"note"`) to
  a friendly combo string like `"Alt+1"`; `handlers` maps the same names
  to callbacks. The daemon builds both from the config's `[keymaps]`
  section and keeps the returned `unregister` func to call on shutdown.
- Combo parsing is pure and unit-testable: `Normalize(combo)` converts a
  friendly string (`"Alt+Shift+F5"`) into the xgbutil format
  (`"Mod1-shift-F5"`) plus the required xproto modifier bits, mapping
  Alt/Meta→Mod1, Ctrl/Control, Shift, Super/Win→Mod4, and raw Mod1..Mod5.
  The keysym is passed through untouched.
- If grabbing any of the three keys fails (e.g. already grabbed by
  another application/WM), do not silently continue as if it worked —
  return an error naming which key failed, and have the daemon log it
  and send a `notify-send` warning at startup ("Alt+2 could not be
  grabbed, it may be in use by MATE — check Keyboard Shortcuts settings")
  rather than crashing the whole daemon over one key.
- Debounce: ignore a repeat firing of the same key within ~300ms (guards
  against X11 key-repeat if the user holds the combo down) — a single
  physical press should produce exactly one capture, not several.
- The X event loop this requires (`xevent.Main` or equivalent) is
  blocking — run it in its own goroutine, and make sure it can be
  cleanly stopped when `unregister` is called (don't leak the goroutine
  on daemon shutdown).

## Testing note

There's no clean unit-testing story for real X11 grabs. Structure the
code so the *dispatch logic* (which callback fires for which key,
debounce timing) is testable in isolation from the actual `xgbutil` calls
— e.g. a thin adapter layer — so at least that part has real tests, even
though the X11 integration itself will only be verified by manual smoke
test (press the keys, confirm the daemon log shows the right event).
