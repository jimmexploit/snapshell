# changes.md — snapshell improvement backlog

Ordered by: quick wins first, architectural work last. Each item is worked
independently; a checkmark means done and verified.

- [x] **1. Popup thin border** — the floating popup currently blends into
      whatever is behind it. Give the huh form a thin rounded border (and a
      subtle frame/title) so the popup reads as a distinct window. Pure
      TUI styling, no new dependencies.

- [x] **2. `start --verbose` live documentation** — a verbose mode that
      stays attached and documents the session in real time with colored
      lines per capture (screenshot / code / note + filename), instead of
      the current silent return. Ctrl+C detaches without stopping the
      session.

- [x] **3. Auto-launch the daemon from `start <name>`** — drop the
      two-step "daemon start, then start box". `snapshell start` should
      detect a dead daemon and spawn it in the background itself, then
      proceed. (Also apply to `status`/`stop` for convenience.)

- [x] **4. Two-line PS1 + capture-scope config** — tmux command capture
      now includes the full (possibly multi-line) prompt: the marker file
      holds a third row (`prev_end` = where the current prompt started),
      threaded through the shell hook's `--prev-end`/captured-output
      dance. A `[capture]` config section lets the user choose scope:
      - `include_output = true` (default): prompt + command + full output.
      - `include_output = false`: only the prompt + command line(s), no
        output noise.

- [x] **5. Wider popup terminal support** — SUPERSEDED by item 8. The
      terminal-emulator popup (alacritty/kitty/mate-terminal/... fallback
      with per-emulator window/exec flags) existed while the caption form
      ran in a spawned floating terminal. The zenity window form replaced
      the spawned terminal entirely, so the emulator fallback list and the
      `[popup].terminal`/`width_cells`/`height_cells` config keys were
      removed.

- [x] **6. fzf-like inline popup (no new window / no taskbar entry)** —
      REVERTED. The inline caption form (pending-capture file +
      `internal-popup-inline` run by the shell hook at each prompt) proved
      too fragile for this phase and is gone. The caption prompt is a real
      zenity window again (see item 8).

- [x] **7. Plain-shell (no tmux) support** — the shell hook now works with
      or without tmux. Outside tmux it records the last command's text
      (`snapshell shellhook record-command`) instead of row markers, and
      Alt+2 falls back to that text when tmux capture isn't available. A
      notification explains that full output capture needs tmux instead of
      staying silent.

- [x] **8. zenity window form popup (TUI removed)** — the caption/note
      prompt is a real GTK window via `zenity`: `--forms` for image/code
      (caption entry; image mode shows the file's path + pixel dimensions,
      code mode shows a truncated preview of the captured text) and
      `--text-info --editable` for notes. Save appends with the caption,
      Skip/cancel appends image/code without one and discards notes. The
      dialog is spawned by the daemon inside its per-capture goroutine, so
      a slow/ignored window never blocks the next hotkey. No TUI anywhere:
      `huh`/`bubbles`/`lipgloss` deps, the spawned terminal, the pending
      file, and `internal-popup`/`internal-popup-inline` are all deleted.
      `[popup]` config is now `width`/`height` (px).