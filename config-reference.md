# config-reference.md — snapshot of the config values in effect BEFORE the
# popup size/ratio/position work (commit ad93150). Kept as the reference
# "current percentages" the user likes, so nothing gets lost when new keys
# are added.

## Current effective values (~/.config/snapshell/config.toml)

[screenshot]
  tool = "flameshot"

[popup]
  width = 560          # caption window width, px (0 = zenity picks)
  height = 320         # caption/note text area height, px (0 = zenity picks)
  font = "Sans 13"     # Pango font for typed text ("" = zenity default)

[capture]
  include_output = true

[keymaps]
  screenshot = "Alt+1"
  command    = "Alt+2"
  note       = "Alt+3"
  selection  = "Alt+4"    # added in commit 4d5d7db, default Alt+4

[paths]
  session_root = "~/blogs"

## Reference ratios (the "current percentages")

- Popup outer-box ratio: 560:320 = 1.75:1 (width:height).
  - height / width = 320/560 = 0.5714 = 57.14%
  - width / height = 560/320 = 1.75 = 175%
- zenity dialog: the caption text area (--text-info --editable) FILLS the
  window, so inner (caption box) == outer (window) == width × height.
