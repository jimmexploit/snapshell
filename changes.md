# changes.md — snapshell improvement backlog

Ordered by: quick wins first, architectural work last. Each item is worked
independently; a checkmark means done and verified.

- [x] **1. Popup thin border** — the floating popup currently blends into
      whatever is behind it. Give the huh form a thin rounded border (and a
      subtle frame/title) so the popup reads as a distinct window. Pure
      TUI styling, no new dependencies.

- [ ] **2. `start --verbose` live documentation** — a verbose mode that
      stays attached and documents the session in real time with colored
      lines per capture (screenshot / code / note + filename), instead of
      the current silent return. Ctrl+C detaches without stopping the
      session.

- [ ] **3. Auto-launch the daemon from `start <name>`** — drop the
      two-step "daemon start, then start box". `snapshell start` should
      detect a dead daemon and spawn it in the background itself, then
      proceed. (Also apply to `status`/`stop` for convenience.)

- [ ] **4. Two-line PS1 + capture-scope config** — tmux command capture
      currently assumes a single-line prompt; a two-line PS1 (e.g.
      powerline) can cut the command line. Fix the start-row calculation to
      include the full prompt/command. Add a `[capture]` config section the
      user can choose from:
      - `include_output = true` (default): current behavior — command line
        + full output.
      - `include_output = false`: capture only the command line, skip
        output (some users want just the command, no noise).

- [ ] **5. Wider popup terminal support** — the popup terminal resolution
      only knows alacritty/kitty/xterm. The user may run mate-terminal,
      gnome-terminal, xfce4-terminal, konsole, etc. Extend the fallback
      list and the per-emulator size flags; document how to set
      `[popup].terminal`.

- [ ] **6. fzf-like inline popup (no new window / no taskbar entry)** — the
      current popup spawns a brand-new terminal window (heavyweight,
      appears in the running-apps bar). Make the caption prompt run inline
      in the user's existing terminal, like fzf: the shell hook detects a
      pending capture request at each prompt and runs the form in-place.
      Keep the spawned-window path as a fallback when there's no shell
      hook / terminal context to inject into.

- [ ] **7. Plain-shell (no tmux) support** — Alt+2 currently no-ops
      without tmux. For users who don't use tmux: fall back to capturing
      the last command's text via shell history (the shell hook already
      knows the command). Full scrolled-output capture still requires
      tmux; a notification explains the difference instead of a dead
      silence.