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
- **zenity** — the caption/note popup is a zenity GTK dialog window
- `notify-send` (libnotify) for notifications
- Go 1.24+ to build from source

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

### Interactive setup wizard

On the first `snapshell start <name>` (when no config file exists yet) a
one-time wizard walks you through a few Y/n questions: installing missing
dependencies, adding the shell hook, and creating the default config. Run
it again any time with:

```sh
snapshell setup
```

If the config already exists, `snapshell setup` asks whether you want to
reset it to defaults (the old one is backed up to `config.toml.bak`) or
keep it and be shown where it lives for editing.

> After installing the hook, **start a new shell/tmux pane**. Hooks are
> sourced at shell startup and don't apply retroactively to open shells.

---

## Quick start

```sh
# 1. (first run only) the wizard runs automatically — or run it yourself
snapshell setup

# 2. start a session — this also starts the daemon in the background
snapshell start acme-box

# 3. work. Press the hotkeys while you document:
#    Alt+1  screenshot
#    Alt+2  capture the last command (and its output)
#    Alt+3  raw note
#    (redefine any of these in ~/.config/snapshell/config.toml → [keymaps])

# 4. when done
snapshell stop

# 5. your documentation lives in a portable folder
ls ~/.local/share/snapshell/acme-box/   # blog.md + attachments/
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
- **Captions in a zenity window.** After each capture a zenity dialog
  pops up. Every mode uses a scrollable text area that fills the window,
  so the caption/note box is big enough to see everything you type: a
  caption box for screenshots and commands, a note box for Alt+3. Save
  appends the entry with your caption; Skip (or Esc) appends
  screenshots/commands without one and discards notes. The dialog runs in
  its own capture goroutine, so leaving it open never blocks the next
  hotkey.
- **The blog file.** Each entry is appended to the session's `blog.md`:
  a hidden timestamp comment, an optional bold caption, then the
  screenshot (`![](attachments/NNN.png)`) or a ```console``` code block
  or the note text. Image paths are relative, so the whole session folder
  can be zipped and opened anywhere.

---

## Hotkeys

The hotkeys are grabbed globally (they work in any window) and are defined
in `~/.config/snapshell/config.toml` under `[keymaps]` — the defaults are
shown below. Combos use `Alt` (Mod1), `Ctrl`, `Shift`, or `Super` (Mod4),
plus any key: `Ctrl+F9`, `Super+space`, etc.

| Hotkey   | Action |
| -------- | ------ |
| Alt+1    | Screenshot → `attachments/NNN.png` → caption → image entry |
| Alt+2    | Last tmux command + full output (or last command text) → caption → code entry |
| Alt+3    | Raw note → paragraph entry |

In the caption dialog, an empty submit or **Skip/Esc**:
- image/code: the entry is still appended, just without a caption;
- note: discarded (there is nothing captured yet).

Ignoring or closing the caption dialog never blocks the daemon or the next
hotkey.

---

## Configuration

The first run creates `~/.config/snapshell/config.toml` with everything
documented inline:

```toml
[screenshot]
  tool = "flameshot"        # "flameshot" or "mate-screenshot"

[popup]
  width = 560              # caption window width in pixels (0 = let zenity pick)
  height = 320             # caption/note text area height in pixels (0 = let zenity pick)
  font = "Sans 13"         # font of the text you type (Pango description, "" = zenity default)

[capture]
  include_output = true     # false = Alt+2 captures only the command line, no output

[keymaps]
  screenshot = "Alt+1"     # global hotkeys — modifiers + key
  command    = "Alt+2"     # Alt (Mod1), Ctrl, Shift, Super (Mod4),
  note       = "Alt+3"     # or raw Mod1..Mod5; key = letter/number/F1-F12/...

[paths]
  # where sessions are stored; change to any writable path
  session_root = "~/.local/share/snapshell"
```

Partial files are fine — missing keys get defaults. `~` is expanded in
paths. If the configured screenshot tool is missing on `PATH`, snapshell
falls back through a sensible list and logs which one it used.

---

## Where things live

```
~/.config/snapshell/config.toml       configuration (auto-created)
~/.local/state/snapshell/
    daemon.log                        human-readable daemon log
    daemon.sock, daemon.pid           IPC socket + PID file
    markers/<pane>.last               tmux row markers (shell hook)
    lastcommand                       recorded command text (plain-shell fallback)
~/.local/share/snapshell/<session>/        each session's folder
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
  install. Run `snapshell setup` and open a new shell.
- **Alt+2 captures nothing in tmux.** Run at least one command first (the
  marker for the first command in a fresh pane is an edge case), and make
  sure the hook is active in that pane.
- **Missing external tool.** Every subprocess (flameshot, tmux, zenity,
  notify-send) is checked on `PATH` and fails with a message naming exactly
  what's missing — install it and retry.
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
internal/popup/           zenity caption/note window (no TUI)
internal/blog/            blog.md writer, formatting contract
internal/config/          TOML config schema + defaults
systemd/                  user service unit
scripts/setup.sh          one-shot install
```

No panics in the daemon: every runtime error is caught, logged, and where
it affects your current action surfaced as a `notify-send`.