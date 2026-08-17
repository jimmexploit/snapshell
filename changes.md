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
      prompt is a real GTK window via `zenity`: `--text-info --editable`
      for every mode, so the caption/note input is a scrollable text area
      that fills the window and you can always see everything you type
      (image mode's label shows the file's path + pixel dimensions, code
      mode's label shows a truncated preview of the captured text). Save
      appends with the caption, Skip/cancel appends image/code without one
      and discards notes. The dialog is spawned by the daemon inside its
      per-capture goroutine, so a slow/ignored window never blocks the next
      hotkey. No TUI anywhere: `huh`/`bubbles`/`lipgloss` deps, the spawned
      terminal, the pending file, and
      `internal-popup`/`internal-popup-inline` are all deleted. `[popup]`
      config is now `width`/`height` (px; height sizes the text area) plus
      `font` (a Pango font description, default `"Sans 13"`, that bumps
      the typed text above the 10pt desktop font via zenity's `--font`).
      (`--forms` + one-line `--add-entry` was tried for image/code first —
      a single-line entry can't grow and zenity 4.1.90 leaves the rest of
      the window as dead space, so all modes share the text area.)

- [x] **9. First-run setup wizard** — `snapshell setup` walks through
      three Y/n questions: (1) installing missing dependencies via apt
      (sudo), reporting flameshot/mate-screenshot as one screenshot
      choice; (2) adding the shell hook to the user's rc file; (3) the
      config file at `~/.config/snapshell/config.toml`. The wizard also
      runs automatically on the first `snapshell start <name>` when the
      config file doesn't exist AND stdin is a real terminal — never in
      scripts/pipes. Every question has a Y/n default and re-prompts on
      garbage input.

- [x] **10. Slimmer CLI + configurable hotkeys** — the `shellhook` and
      `completion` commands are gone (`root.CompletionOptions.
      DisableDefaultCmd = true`); the setup wizard now owns shell-hook
      installation. The two helpers the hook snippets call are hidden
      plumbing (`snapshell _hook-mark`, `snapshell _hook-record`), and
      re-running setup with the hook already installed no longer errors.
      `snapshell setup` on an existing config now asks whether to reset
      it to defaults — backing the old file up to `config.toml.bak` — or
      keep it and be pointed at its location. New `[keymaps]` config
      section lets the user rebind the three global hotkeys with friendly
      combos ("Alt+1", "Ctrl+F9", "Super+space"): the hotkeys package
      gained a pure, unit-tested `Normalize` that maps Alt/Meta→Mod1,
      Ctrl, Shift, Super/Win→Mod4 (raw Mod1..Mod5 also accepted) and
      passes the keysym through; the daemon builds its grab map from
      `[keymaps]` (verified end-to-end: Ctrl+F9 fired, Alt+1 stopped).

- [x] **11. Session storage path + hook self-upgrade** — the `[paths]`
      `session_root` setting now defaults to `~/.local/share/snapshell`
      (changeable to any writable path). `snapshell setup` now *replaces*
      a stale shell-hook block in the user's rc file instead of skipping
      it: when the installed block still calls the removed
      `snapshell shellhook mark`/`record-command` helpers (which broke
      every shell command — e.g. `make` — with "unknown command
      shellhook"), it rewrites the block in place to the current
      `_hook-mark`/`_hook-record` version, preserving the rest of the rc
      file (unit-tested; the affected `~/.bashrc` was upgraded via the
      wizard).

- [x] **12. Per-user session root + wizard step feedback** — the default
      `session_root` was changed again to `~/.local/share/snapshell`: the
      earlier `/usr/share/snapshell` default broke `snapshell start` for
      non-root users with `mkdir /usr/share/snapshell: permission denied`
      (the XDG per-user data dir is always writable). The setup wizard now
      prints a `[2/3] Shell hook:` header before its question (it
      previously jumped straight from `[1/3]` to `[3/3]`, looking like the
      step was missing) and confirms when the hook is skipped.

- [x] **13. Global command recording + per-session history** — every
      completed command now lands in the active session's command history
      at `<session_root>/logs/<name>/commands.history`, one line per
      command:
      ```
      2026-08-16 11:16:57  %1        echo first_command
      2026-08-16 11:17:15  /dev/pts/2  whoami
      ```
      The shell hook records from EVERY shell, not just tmux: tmux panes
      pass `--source "$TMUX_PANE"` (plus their existing row markers for
      Alt+2), and plain terminals (a new kitty tab/window, any tty) pass
      `--source "$(tty)"` via `_hook-record`, which also keeps the
      `lastcommand` plain-shell fallback working. Newlines in a command are
      collapsed to spaces so each record is exactly one line. No session
      active → only `lastcommand` is written, no history file is created.
      Also fixed: the bash DEBUG trap no longer records the hook's own
      `.bashrc` sourcing lines (`unset _snapshell_old_debug ...`) as fake
      commands — a `_snapshell_sourcing` flag makes the trap a no-op while
      the rc file is being sourced.

- [x] **14. Unified command log — Alt+2 captures the last command
      anywhere** — the earlier split meant Alt+2 in a plain kitty tab
      still read the *tmux* row log and captured the last tmux command
      instead of the command just typed in the terminal. Now the active
      session's `commands.log` is unified: tmux records stay row-based
      (`%N <prev> <start> <end>`, captured via tmux) and plain-terminal
      records are appended as text (`tty <source> <command text...>`).
      `tmuxcap` dispatches on the last line's first field — `%` → tmux
      capture, `tty` → return the command text verbatim (no tmux needed) —
      so Alt+2 always captures the most recently completed command no
      matter which shell it was typed in. Command text recording moved to
      the end phase (after the command completes), matching the tmux row
      semantics.
- [x] **15. Alt+2 output for plain kitty terminals** — previously a command
      typed in a kitty tab (no tmux) was captured as text only, with no
      output. Now the shell hook enables kitty's shell integration (OSC 133
      prompt/command marks) in plain kitty shells, records the command with
      its kitty window id + listen socket, and the record type becomes
      `ktty <source> <kittywid> <listen> <command text...>`. Alt+2 reads
      the output back from that window with `kitty @ --to <listen> get-text
      --match id:<kittywid> --extent last_cmd_output` and appends command +
      output. Degrades cleanly: missing `kitty`, a dead socket/window, or a
      window whose shell predates the hook all fall back to the command text
      (with an actionable error when kitty itself is the problem).
- [x] **16. Plain captions + language-tagged code blocks** — blog.md
      captions are no longer bolded by default; they render as a plain
      paragraph line directly above the image/code block. Code blocks get
      a language tag chosen from the captured text by
      `internal/blog/lang.go`: shell sessions (a prompt line) → ```bash,
      source code by content/shebang → ```go / ```python / ```yaml /
      ```json / ```html / ```toml / ```javascript, anything else →
      ```text. A plain `# ` line is treated as a root prompt only after
      content detection fails, so `# comment` lines in selected code don't
      misclassify.
- [x] **17. Alt+4 selection/clipboard capture** — a fourth global hotkey
      (configurable under `[keymaps].selection`, default Alt+4) captures
      the currently selected text via `xclip -o -selection primary`,
      falling back to the clipboard when nothing is selected. Both empty →
      a notification, no entry. The captured text goes through the same
      caption popup + language-tagged blog.md append as Alt+2.
