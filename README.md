# snapshell

snapshell is a background daemon + CLI that documents a HackTheBox (HTB)
pentest session in real time. Press a global hotkey while you work and the
result is appended to a Markdown file: screenshots, the exact terminal
commands you ran with their full output, and free-form notes. When the
session is over you have a portable, zippable `blog.md` with embedded
screenshots — no copy-pasting, no after-the-fact reconstruction.

The whole thing is a single Go binary. It targets **Linux with an X11
display** (MATE and other X11 desktops). It has no database — all state is
plain files.

---

## What you need

- Linux + X11 (Wayland is not supported)
- tmux — required for full command+output capture (see *Plain shell* below)
- a screenshot tool: **flameshot** (default) or **mate-screenshot**
- `notify-send` (libnotify) for notifications
- a terminal emulator for the optional floating caption window
  (alacritty, kitty, mate-terminal, gnome-terminal, xfce4-terminal,
  konsole, xterm, ...)
- Go 1.24+ to build from source

Optional: `xdotool` centers/focuses the floating caption window.

---

## Install

### One-shot setup script (recommended)

```sh
./scripts/setup.sh                # installs distro deps, builds, installs binary + shell hooks
./scripts/setup.sh --skip-deps    # if you already have the dependencies
./scripts/setup.sh --enable-systemd  # also enable the daemon under systemd
```

### Manual

```sh
make build          # builds ./bin/snapshell
make install        # installs to ~/go/bin/snapshell
make check          # fmt + vet + tests
make uninstall      # remove the installed binary
```

The shell hook is installed per shell:

```sh
snapshell shellhook install bash    # appends the hook to ~/.bashrc
snapshell shellhook install zsh     # appends the hook to ~/.zshrc
# or just print it to review first:
snapshell shellhook print bash
```

> After installing the hook, **start a new shell/tmux pane**. Hooks are
> sourced at shell startup and don't apply retroactively to open shells.

---

## Quick start

```sh
# 1. install the shell hook (once, per shell) and open a new shell
snapshell shellhook install bash

# 2. start a session — this also starts the daemon in the background
snapshell start acme-box

# 3. work. Press the hotkeys while you document:
#    Alt+1  screenshot
#    Alt+2  capture the last command (and its output)
#    Alt+3  raw note

# 4. when done
snapshell stop

# 5. your documentation lives in a portable folder
ls ~/snapshell/acme-box/     # blog.md + attachments/
```

Start another session with a new name anytime. Only **one session is
active at a time** — `snapshell start b` while `a` is active fails with a
clear error and leaves `a` untouched. Starting a session name that already
has a folder **resumes** it (appends to the existing `blog.md`, continues
the attachment numbering).

---

## How it works

```
        Alt+1/2/3 (X11 global hotkeys)
                      │
                      ▼
   ┌────────────── daemon (background) ──────────────┐
   │  screenshot ───▶ attachments/NNN.png            │
   │  tmux marker ──▶ command + output (or command   │
   │                   text alone, plain shell)      │
   │  note ─────────▶ raw text                       │
   └──────────────────────────┬──────────────────────┘
                              ▼
              caption prompt ──▶ appended to blog.md
```

- **Daemon + CLI.** The daemon is a long-running background process that
  owns the X11 hotkey grabs and the capture logic. The CLI talks to it
  over a Unix socket (`start`, `stop`, `status`, ...) with a trivial
  newline-delimited JSON protocol. `snapshell start <name>` auto-launches
  the daemon if it isn't running, so in normal use you never touch the
  daemon directly.
- **Global hotkeys.** Alt+1/2/3 work no matter which window has focus. If
  the grab fails (another process stole the key), you get a log line, not
  a crash.
- **Alt+2 capture without guessing.** The shell hook records *tmux pane
  row numbers* at the start and end of every command (including where the
  prompt began, so even two-line powerline prompts are captured in full).
  The daemon then runs `tmux capture-pane` over that exact range — it gets
  the literal prompt + command + output, including output that scrolled
  off-screen. Nothing is matched against your PS1 string.
- **Plain shell (no tmux).** The hook works without tmux too: it records
  the command's *text* instead of row markers, and Alt+2 falls back to
  that. You get the command line (a notification explains that full
  output capture needs tmux).
- **Captions inline.** By default the caption prompt appears **inline in
  your terminal at the next shell prompt** (fzf-style — no new window).
  The daemon stages a pending request; the shell hook spots it and shows
  the form in place. Set `[popup] inline = false` to go back to a floating
  terminal window instead.
- **The blog file.** Each entry is appended to the session's `blog.md`:
  a hidden timestamp comment, an optional bold caption, then the
  screenshot (`![](attachments/NNN.png)`) or a ```console``` code block
  or the note text. Image paths are relative, so the whole session folder
  can be zipped and opened anywhere.

---

## Hotkeys

| Hotkey | Action |
| ------ | ------ |
| Alt+1  | Screenshot → `attachments/NNN.png` → caption → image entry |
| Alt+2  | Last tmux command + full output (or last command text) → caption → code entry |
| Alt+3  | Raw note → paragraph entry |

In the caption form, an empty submit or **Esc**:
- image/code: the entry is still appended, just without a caption;
- note: discarded (there is nothing captured yet).

Ignoring or closing the caption form never blocks the daemon or the next
hotkey.

---

## Configuration

The first run creates `~/.config/snapshell/config.toml` with everything
documented inline:

```toml
[screenshot]
  tool = "flameshot"        # "flameshot" or "mate-screenshot"

[popup]
  terminal = "alacritty"    # floating-window terminal; missing ones fall
                            # back through a built-in list
  width_cells = 100
  height_cells = 30
  inline = true             # caption form at your next shell prompt; false = floating window

[capture]
  include_output = true     # false = Alt+2 captures only the command line, no output

[paths]
  session_root = "~/snapshell"
```

Partial files are fine — missing keys get defaults. `~` is expanded in
paths. If the configured screenshot tool or popup terminal is missing on
`PATH`, snapshell falls back through a sensible list and logs which one it
used.

---

## Where things live

```
~/.config/snapshell/config.toml       configuration (auto-created)
~/.local/state/snapshell/
    daemon.log                        human-readable daemon log
    daemon.sock, daemon.pid           IPC socket + PID file
    markers/<pane>.last               tmux row markers (shell hook)
    pending.json                      staged capture awaiting its inline caption
    lastcommand                       recorded command text (plain-shell fallback)
~/snapshell/<session>/                each session's folder
    blog.md                           the documentation, append-only
    attachments/                      screenshots (NNN.png)
```

---

## systemd (optional)

A user unit is provided so the daemon survives logout/login:

```sh
cp systemd/snapshell.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now snapshell
```

It runs the daemon as your logged-in user (it needs your X11 session to
grab hotkeys). With the unit active you don't need the auto-launch from
`snapshell start`.

---

## Troubleshooting

- **Hotkeys don't fire.** Check `~/.local/state/snapshell/daemon.log` for
  `could not grab` — usually a second snapshell daemon or another app
  owns the key combo. Stop/restart the daemon after it has fully exited.
- **"no command captured yet — check that the snapshell shell hook is
  sourced"**: the shell hook isn't installed, or the shell predates the
  install. Run `snapshell shellhook install <shell>` and open a new shell.
- **Alt+2 captures nothing in tmux.** Run at least one command first (the
  marker for the first command in a fresh pane is an edge case), and make
  sure the hook is active in that pane.
- **Missing external tool.** Every subprocess (flameshot, tmux,
  notify-send, the popup terminal, xdotool) is checked on `PATH` and fails
  with a message naming exactly what's missing — install it and retry.
- **The daemon lingers after `stop`.** It releases X11 hotkey grabs on
  shutdown, which can take a moment; wait ~1s before restarting.

---

## Development

```sh
make check          # gofmt + vet + all tests
make build          # static binary at ./bin/snapshell
```

Layout (each module has its own `AGENTS.md` with details):

```
cmd/snapshell/            CLI + daemon IPC protocol
internal/daemon/          daemon lifecycle, socket server, session state
internal/hotkeys/         X11 global key grabbing
internal/capture/screenshot/  screenshot tool invocation
internal/capture/tmuxcap/     tmux pane capture by row range
internal/shellhook/       bash/zsh hook scripts + marker files
internal/popup/           huh-based caption/note form (inline + window)
internal/blog/            blog.md writer, formatting contract
internal/config/          TOML config schema + defaults
systemd/                  user service unit
scripts/setup.sh          one-shot install
```

No panics in the daemon: every runtime error is caught, logged, and where
it affects your current action surfaced as a `notify-send`.