# AGENTS.md — internal/config

Owns: loading `~/.config/snapshell/config.toml`, providing defaults, and
resolving fallbacks for missing external tools. Every other package that
needs a config value should get it through this package's typed struct,
not by reading TOML itself.

## Schema

```toml
[screenshot]
tool = "flameshot"        # or "mate-screenshot"

[popup]
width = 560               # caption window width in px (0 = let zenity pick)
height = 320              # caption/note text area height in px (0 = let zenity pick)
font = "Sans 13"          # Pango font for the text you type ("" = zenity default)
keep_ratio = true         # lock the window to the default 560:320 aspect:
                          #   change ONE of width/height away from its
                          #   default and the other follows to keep the
                          #   ratio; set both and both are honored
position = ""             # where the dialog spawns: a preset ("center",
                          #   "top-left", "top-center", "top-right",
                          #   "center-left", "center-right", "bottom-left",
                          #   "bottom-center", "bottom-right") or explicit
                          #   pixels from the top-left ("120,80"); empty =
                          #   let the WM place it. Requires xdotool.

[capture]
include_output = true     # false = Alt+2 captures only the command line
reload_on_hotkey = false  # true = re-read config before every hotkey capture
count_timeout_ms = 1500   # ms after Alt+2 to wait for a count digit (1-9);
                          # 0 = 1500. Digit sets how many recent commands
                          # to capture at once; none pressed = last one.

[keymaps]
screenshot = "Alt+1"      # global hotkeys; Alt=Mod1, Ctrl=Control,
command    = "Alt+2"      # Super/Win=Mod4, raw Mod1..Mod5 accepted too
note       = "Alt+3"
selection  = "Alt+4"      # capture selected text (clipboard fallback)
reload     = "Alt+5"      # re-read config + re-grab hotkeys, no restart

[keymaps.inventory]        # keys for the review TUI ('snapshell inventory')
quit      = "q, ctrl+c"    # each value is a comma-separated list of key
up        = "up, k"        # names as the terminal reports them ("q",
down      = "down, j"      # "ctrl+c", "up", "enter", "esc", "pgup", "y",
page_up   = "pgup"         # "Y", ...). Empty = default. ctrl+c ALWAYS
page_down = "pgdown"       # quits in every state and cannot be rebound.
append    = "a"            #   append-as-is / caption / discard / note /
caption   = "c"            #   blog-view / open-image in the card list
discard   = "d"            # page_up/page_down scroll the code/blog preview;
note      = "n"            # submit/cancel act in the caption/note editors;
blog      = "v"            # confirm/decline answer the discard prompt
open      = "enter"
submit    = "ctrl+s"
cancel    = "esc"
confirm   = "y, Y"
decline   = "n, N"

[paths]
session_root = "~/.local/share/snapshell"

[themes]
name = ""                 # GTK theme for the popup via GTK_THEME
                          # ("Sweet", "Sweet:dark", ...); empty = system default
root = ""                 # extra dir to scan for installed themes
                          # (list-themes); empty = standard locations only

[blog]
caption_position = "above"  # where the caption of an image/code entry sits
                            # in blog.md: "above" (default) or "below";
                            # note entries have no caption and ignore it

[image]
image_viewer = ""           # binary for peeking at screenshots ("feh", ...)
                            # "" = xdg-open
close_delay_secs = 5        # seconds an opened image stays up before
                            # best-effort close (0 = 5)
image_mode = "kitty"        # "kitty" = render in-terminal (full-screen,
                            # falls back to external), "external" = viewer
image_render = "tab"        # "tab" = in-terminal screenshot opens full-screen
                            # on Enter (default); "inline" = rendered right
                            # in the preview pane for the selected image
                            # card, no Enter needed (Enter still zooms)
image_scale_percent = 60    # in-terminal image size as % of the pane fit.
                            # A *int: UNSET = per-mode default (tab 100,
                            # inline 50) and inline is hard-capped at 65%;
                            # explicit values are clamped to 1..100 and
                            # inline never exceeds 65. External ignores.
blog_image_scale_percent = 100 # how large screenshots embedded in the
                            # "view blog" render are drawn, as % of the pane
                            # fit (100 = full fit when unset; distinct from
                            # image_scale_percent, which governs the image
                            # card previews). Explicit values clamp to 1..100.
blog_image_align = "left"   # horizontal position of each screenshot in the
                            # "view blog" render: "left" (default), "center",
                            # or "right". Ignored by the image card previews.
blog_image_padding = 2      # edge gap in cells kept for left/right-aligned
                            # blog screenshots (so they aren't glued to the
                            # edge). 0 = flush. Ignored for "center" and by
                            # the image card previews.

# NOTE: configs written before the [inventory]→[image] rename used the
# [inventory] table for these keys. Load() merges those legacy values into
# [image] wherever the new table is silent (mergeLegacyImage), so old configs
# keep working untouched. The default file and this schema use [image] only.
```

## Behavior

- `Load() (*Config, error)`: if `~/.config/snapshell/config.toml` doesn't
  exist, create it with the full default values written out (not just an
  empty file) so the user has something to look at and edit, then return
  the defaults. If it exists but is missing individual keys, fill in
  defaults for just those keys rather than erroring — partial configs are
  fine.
- `ResetDefault() error`: move the current config to `config.toml.bak`
  (if present) and write a fresh default file. Used by the setup wizard's
  "reset to defaults" question.
- Expand `~` in `session_root` (and anywhere else a path appears) using
  the real home directory (`os.UserHomeDir()`), don't rely on shell
  expansion since Go isn't invoking a shell to read this value.
- **Tool resolution is this package's job, exposed as a method other
  packages call rather than re-implementing `$PATH` lookups themselves**,
  e.g.:
  ```go
  func (c *Config) ResolveScreenshotTool() (bin string, err error)
  ```
  It checks the configured value against `exec.LookPath`, falls through
  the documented fallback list (screenshot: flameshot → mate-screenshot),
  and returns a specific error naming every option that was tried and not
  found if all of them fail — so the eventual `notify-send`/log message
  can be concrete ("none of flameshot, mate-screenshot found on PATH")
  rather than vague. (zenity is not resolved here — `internal/popup` does
  its own `exec.LookPath` and names the missing binary itself.)
- **Inventory-TUI keymaps live here**: `InventoryKeys()` fills `[keymaps.inventory]`
  with defaults for any action left empty (partial configs are fine, like
  everywhere else), and `SplitKeyList()` splits a comma-separated binding
  list into trimmed, non-empty key names. `cmd/snapshell` maps the raw
  strings onto `tui.Keys`; the TUI package owns the key-handling and its own
  `DefaultKeys()` fallback. `ctrl+c` always quits the TUI in every state and
  is deliberately not part of any binding list.
- **The image settings live under `[image]`** (formerly `[inventory]`).
  `mergeLegacyImage` runs during load and folds any legacy `[inventory]`
  values into `[image]` wherever the new table did not explicitly set them
  (checked via the TOML metadata, since `Default()` pre-fills `Image` before
  the file is decoded). The default file writes `[image]` only.
- **`keep_ratio` is applied at load time** (`normalizeKeepRatio`): when on
  (default), exactly one dimension changed away from the default
  (`DefaultPopupWidth` 560 / `DefaultPopupHeight` 320) makes the other
  follow so the 560:320 ratio holds; both non-default values are honored
  as-is. `KeepRatioOn()` exposes the effective value. Popup position is
  passed through to `internal/popup` verbatim — parsing/validation lives
  there, not here.
- **Config reloading is a daemon concern, but this package is the reload
  source**: the daemon calls `Load()` again to get a fresh `*Config` and
  swaps it atomically (see `internal/daemon/AGENTS.md`). `ReloadOnHotkeyOn()`
  and `OutputIncluded()` are the *bool accessors used to drive that
  decision.
- **Theme search dirs live here** (`ThemeSearchDirs() []string`): the
  standard GTK theme locations (/usr/share/themes, /usr/local/share/themes,
  ~/.themes, ~/.local/share/themes) plus `[themes].root` (a single extra
  root, `~`-expanded), deduplicated. `snapshell list-themes` consumes this;
  the popup itself only receives the theme *name* and sets GTK_THEME. The
  actual directory scan (which entries are GTK themes) is `cmd/snapshell`
  territory, not this package's.

## What NOT to do here

- Don't validate `width`/`height` against actual screen resolution or
  attempt "smart" auto-sizing — take the configured numbers as-is and hand
  them to `internal/popup`, which passes them to zenity's `--width`/
  `--height`.
- Don't add config keys beyond what's listed above unless a build step
  elsewhere in this repo's AGENTS.md files explicitly references one that
  doesn't exist yet here — keep schema and usage in sync rather than
  speculatively adding options nothing reads.
