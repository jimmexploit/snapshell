# snapshell — commit reference points

## VERY STABLE / RELIABLE (the absolute beast — come back here if something goes wrong)

Commit: 3db764e3f2bb7eae64cb231ce85065e75c0b436a
Date:   2026-08-18T02:11:22Z

The commit right before the "Alt+2 command count" feature. Extensively
tested and verified across many sessions. If anything in the tree now
misbehaves and needs a trustworthy fallback, this is it.

## CURRENT / BASIC TESTING ONLY (not yet proven stable)

Commit: b632fc7 (Alt+2 command count (digit prefix); track inventory-mode spec)
Date:   2026-08-17

Adds Alt+2 + digit (1-9) multi-command capture, tmuxcap.CaptureN,
hotkeys.WaitForDigit, the [capture].count_timeout_ms key, and the popup
"command ×N" title. Basic live testing survived (both the digit path and
the plain no-digit path), but it is NOT known to be very stable yet —
treat it as a candidate, not a trusted fallback.