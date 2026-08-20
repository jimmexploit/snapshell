# snapshell

**Snapshell documents a HackTheBox pentest session as you work — literally as
you work.** Press a global hotkey and the moment lands in a Markdown file:
a screenshot, the exact command you just ran with its full output, or a
free-form note. When the session is over you have a portable, zippable
`blog.md` with embedded images — no copy-pasting, no after-the-fact
reconstruction, no screenshots dying in a stale clipboard.

It is one static Go binary, it runs in the background as a daemon, and it
only works on **Linux + X11** (MATE and other X11 desktops). No database —
all state is plain files.

---

## Who is this for, and why

- **Pentesters and CTF players.** You spend hours in a terminal probing a
  box. The commands, their output, the screenshots of a web app or a
  service banner — that *is* the write-up. Snapshell makes writing it
  effortless instead of a chore you avoid until the memory fades.
- **People who document by example.** Blog posts, lab write-ups, "how I
  broke in" walkthroughs — capture the evidence live and the prose writes
  itself afterwards.
- **People who hate busywork.** No copying terminal text, no pasting into
  Markdown, no renaming screenshot files, no fighting with PS1 regexes to
  reconstruct a command. The hotkey does it.

Why this design: everything that can fail (missing tool, tmux not running, a
key combo already grabbed by another app) is caught, logged, and surfaced as
a desktop notification — never a silent nothing, and never a crash. You stay
in the flow; the documentation happens in the background.

---

## What you need (exact setup)

Snapshell is built for a specific environment. Anything not listed here is
not supported.

| Layer      | Requirement                                                          |
| ---------- | -------------------------------------------------------------------- |
| OS         | Linux                                                               |
| Display    | **X11 only** (Wayland is not supported — the global hotkeys and the popup positioning depend on X11 APIs) |
| Desktop    | MATE recommended; any X11 desktop/window manager works (the hotkeys are grabbed via XGrabKey, so they work over any window) |
| Shell      | bash or zsh (the command-capture shell hook is a bash/zsh function)  |
| Terminal   | **kitty strongly recommended** — in-terminal screenshot preview, inline previews and the "view blog" render use the kitty graphics protocol. Any terminal works for the core flows (screenshot, notes, selection capture) |
| Multiplexer | tmux recommended — lets Alt+2 capture a command's *full output* (including scrolled output). Everything else works without it |

### Required tools

These are checked by `snapshell setup` and the one-shot script, and each
missing one is reported by name with the apt package that provides it:

| Tool           | Package         | Needed for                                   |
| -------------- | --------------- | -------------------------------------------- |
| flameshot or mate-screenshot | flameshot / mate-utils | Alt+1 screenshots |
| zenity         | zenity          | the caption/note popup dialog                |
| notify-send    | libnotify-bin   | desktop notifications                        |
| xclip          | xclip           | Alt+4 selected-text capture                   |

### Optional tools (each unlocks a bonus feature)

Snapshell degrades gracefully without these — it tells you what you're
missing and which feature it powers, but nothing breaks.

| Tool  | Package     | Unlocks                                                                |
| ----- | ----------- | --------------------------------------------------------------------- |
| tmux  | tmux        | Alt+2 full command + output capture (including scrolled output); without it Alt+2 captures just the command line |
| kitty | kitty       | in-terminal screenshot preview, inline previews, and the blog render  |
| xdotool | xdotool   | positioning the caption popup at a configured `[popup].position`       |
| wmctrl | wmctrl     | reliably auto-closing the image viewer after a peek                    |
| fc-list | fontconfig | the `snapshell list-fonts` command (fontconfig is present on most distros) |

`xdg-open` is the default image viewer when none is configured; it ships with
every desktop environment.

### Build requirements

Go 1.24+ to build from source. There are no other build-time dependencies.

---

## Install

### One-shot script (recommended)

```sh
./scripts/setup.sh                    # installs deps, builds, installs binary
./scripts/setup.sh --skip-deps        # if you already have the dependencies
./scripts/setup.sh --enable-systemd   # also run the daemon under systemd
```

### Manual

```sh
make build          # builds ./bin/snapshell
make install        # installs to ~/go/bin/snapshell
make check          # fmt + vet + tests
make uninstall      # remove the installed binary
```

### Interactive setup wizard

`snapshell setup` walks you through three steps with Y/n prompts:

1. **Dependencies** — checks every required tool on `PATH` (and reports the
   optional ones separately), offers to install the missing apt packages,
2. **Shell hook** — adds the bash/zsh hook that records command boundaries
   for Alt+2,
3. **Config** — creates `~/.config/snapshell/config.toml` with every option
   documented inline, or offers to reset an existing one to defaults (the
   old file is backed up to `config.toml.bak`).

The wizard runs automatically on the first `snapshell start <name>` when no
config exists. After installing the hook, **start a new shell/tmux pane** —
hooks are sourced at shell startup and don't apply retroactively.

---

## Quick start

```sh
# 1. (first run only) the wizard runs automatically — or run it yourself
snapshell setup

# 2. start a session — this also starts the daemon in the background
snapshell start acme-box

# 3. work. Press the hotkeys while you document:
#    Alt+1  screenshot
#    Alt+2  last command + full output   (Alt+2 then 2 = the last two commands)
#    Alt+3  raw note
#    Alt+4  selected text (primary selection, falls back to clipboard)
#    Alt+5  re-read the config + re-grab hotkeys (no restart)
#    (all of these are redefinable in ~/.config/snapshell/config.toml → [keymaps])

# 4. review what you captured and turn it into the write-up
snapshell inventory

# 5. when done
snapshell stop

# 6. your documentation lives in a portable folder
ls ~/.local/share/snapshell/acme-box/   # blog.md + attachments/
```

Only **one session is active at a time** — `snapshell start b` while `a` is
active fails with a clear error and leaves `a` untouched. Starting a session
name that already has a folder **resumes** it (appends to the existing
`blog.md`, continues the attachment numbering).

---

## How it works

```
        Alt+1/2/3/4/5 (X11 global hotkeys, work in any window)
                      │
                      ▼
   ┌────────────── daemon (background) ──────────────┐
   │  Alt+1 screenshot ──▶ attachments/NNN.png        │
   │  Alt+2 tmux/kitty ─▶ command + full output       │
   │  Alt+3 note ───────▶ raw text                    │
   │  Alt+4 selection ──▶ clipboard/primary text      │
   └──────────────────────────┬───────────────────────┘
                              ▼
              caption prompt ──▶ appended to blog.md
```

- **Daemon + CLI.** A long-running background process owns the X11 hotkey
  grabs and all capture logic. The CLI talks to it over a Unix socket
  (`start`, `stop`, `status`, `inventory`, ...) with a trivial
  newline-delimited JSON protocol. `snapshell start <name>` auto-launches
  the daemon if it isn't running, so in normal use you never touch it.
- **Alt+2 captures without guessing.** The shell hook records tmux pane row
  numbers at the start and end of every command (including where the prompt
  began, so even two-line powerline prompts are captured in full). The daemon
  then runs `tmux capture-pane` over that exact range — the literal prompt +
  command + output, including output that scrolled off-screen. Nothing is
  matched against your PS1 string. In a plain kitty window the hook records
  the command text plus the kitty window id, and Alt+2 reads the output back
  via `kitty @ get-text --extent last_cmd_output`. In any other terminal the
  command text alone is captured (with a notification explaining that full
  output needs tmux or kitty).
- **Captions in a zenity window.** After each capture a zenity dialog pops up
  with a scrollable text area. Save appends the entry with your caption;
  Skip (or Esc) appends screenshots/commands without one and discards notes.
  The dialog runs in its own goroutine, so leaving it open never blocks the
  next hotkey.
- **The blog file.** Each entry is appended to the session's `blog.md`: a
  hidden timestamp comment, an optional bold caption, then the screenshot
  (`![](attachments/NNN.png)`), a `console` code block, or the note text.
  Image paths are relative, so the whole session folder can be zipped and
  opened anywhere.

---

## Reviewing what you captured (`snapshell inventory`)

Captures land silently in a pending queue while you work. Run `snapshell
inventory` any time to review them in a full-screen terminal UI:

- browse the queue (code preview on the left, image preview on the right),
- caption a capture before committing it, or append it as-is,
- discard what you don't want (with a confirmation prompt),
- write a standalone note straight into `blog.md`,
- press the blog key (default `v`) to view the live-rendered `blog.md`,
  screenshots included — in kitty they render inline via the kitty graphics
  protocol, with configurable size, alignment, and edge padding.

Captured code blocks are syntax-colored — in the queue preview, in the
live blog view, and in `blog.md` itself (the code fence carries the detected
language, so any Markdown viewer colors it too).

### Auto mode (`[auto]`)

Turn on `[auto].enabled` and every command that exits 0 is queued as a
pending code card automatically while you work — no Alt+2 needed. The
successful commands are waiting in the review TUI when you sit down to
write up the session.

- Command output is captured the same way Alt+2 would: from the tmux pane
  (full output, including scrolled content), or from the kitty scrollback
  when you're not in tmux. A failed output capture degrades to a
  command-only card rather than losing the command.
- `[auto].exclude` lists commands the auto path skips even when they exit 0
  (defaults: `ls`, `cd`, `clear`, `pwd`, `exit`, `echo`). Each entry matches
  the full command line or its first word — `ls` also skips `ls -la`.
  Excluded commands can still be captured manually with Alt+2.
- Auto mode only queues commands while the **active** session is in
  inventory mode; nothing is queued in normal-mode sessions, and ignored
  commands (excluded, failed, auto off) never fire a notification.

Every key in the TUI is redefinable under `[keymaps.inventory]` in the
config file.

---

## Hotkeys

Global hotkeys are defined in `~/.config/snapshell/config.toml` under
`[keymaps]` — the defaults are below. Combos use `Alt` (Mod1), `Ctrl`,
`Shift`, or `Super` (Mod4) plus any key: `Ctrl+F9`, `Super+space`, etc.
They are grabbed globally, so they fire no matter which window has focus.

| Hotkey   | Action                                                              |
| -------- | ------------------------------------------------------------------- |
| Alt+1    | Screenshot → `attachments/NNN.png` → caption → image entry          |
| Alt+2    | Last tmux command + full output → caption → code entry. Press a digit (1-9) within 1.5 s to capture that many recent commands at once |
| Alt+3    | Raw note → paragraph entry                                          |
| Alt+4    | Selected text (X11 primary selection, falling back to the clipboard) → caption → code entry |
| Alt+5    | Re-read the config + re-grab hotkeys — no restart                   |

In the caption dialog, Skip/Esc: image/code entries are still appended
(without a caption); notes are discarded (there is nothing captured yet).
Ignoring or closing the dialog never blocks the daemon or the next hotkey.

---

## Configuration

The first run creates `~/.config/snapshell/config.toml` with every option
documented inline. Sections group related settings together:

```toml
[screenshot]   how screenshots are taken (Alt+1)
[popup]        the caption/note dialog window
[capture]      how commands are captured (Alt+2)
[keymaps]      all hotkeys — global + review-TUI keys
[keymaps.inventory]  keys of the review TUI
[image]        how screenshots look in the review TUI and blog render
[auto]         auto-capture successful commands while in inventory mode
[blog]         how entries are written to blog.md
[paths]        where sessions are stored
[themes]       GTK theme for the popup
```

Highlights (see the file itself for the full annotated list):

- `[image]` holds everything image-related in one place: the external viewer
  (`image_viewer`), the in-terminal mode (`image_mode` = kitty/external), the
  preview style (`image_render` = tab/inline), and the blog-render layout
  (`blog_image_scale_percent`, `blog_image_align`, `blog_image_padding`).
- `[auto]` turns on auto mode: every command that exits 0 queues itself as a
  pending code card while an inventory session is active, with an
  `exclude` list for commands you don't want auto-captured.
- `[keymaps]` holds every hotkey in one place — the global Alt+1..5 combos
  and the review-TUI keys, each a comma-separated list of key names.
- Partial files are fine — missing keys get defaults. `~` is expanded in
  paths. If the configured screenshot tool is missing, snapshell falls back
  through a sensible list and logs which one it used.

> A config written for an older snapshell that used the `[inventory]` table
> for image settings keeps working: those keys are merged into `[image]`
> automatically, and the `[image]` table wins where both are set.

---

## Where things live

```
~/.config/snapshell/config.toml       configuration (auto-created)
~/.local/state/snapshell/
    daemon.log                        human-readable daemon log
    daemon.sock, daemon.pid           IPC socket + PID file
    markers/<pane>.last               tmux row markers (shell hook)
    logs/<session>/markers.logs       per-session command log
~/.local/share/snapshell/<session>/   each session's folder
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

It runs the daemon as your logged-in user (it needs your X11 session to grab
hotkeys). With the unit active you don't need the auto-launch from
`snapshell start`.

---

## Troubleshooting

- **Hotkeys don't fire.** Check `~/.local/state/snapshell/daemon.log` for
  `could not grab` — usually a second snapshell daemon or another app owns
  the key combo. Stop/restart the daemon after it has fully exited.
- **"no command captured yet — check that the snapshell shell hook is
  sourced"**: the shell hook isn't installed, or the shell predates the
  install. Run `snapshell setup` and open a new shell.
- **Alt+2 captures nothing in tmux.** Run at least one command first (the
  marker for the first command in a fresh pane is an edge case), and make
  sure the hook is active in that pane.
- **Screenshots render as blank boxes in the review TUI.** The in-terminal
  image rendering needs kitty — run `snapshell inventory` inside kitty, or
  set `[image].image_mode = "external"` to use an external viewer instead.
- **Missing external tool.** Every subprocess (flameshot, tmux, zenity,
  notify-send, xclip, kitty, xdotool, wmctrl) is checked on `PATH` and fails
  with a message naming exactly what's missing — install it and retry.
- **The daemon lingers after `stop`.** It releases X11 hotkey grabs on
  shutdown, which can take a moment; wait ~1s before restarting.

---

## Roadmap

Snapshell is actively developed. These are the directions being explored —
none of them exist yet, and none of them will change how the core flows work.

- **Remote daemons (capture from anywhere).** Multiple capture endpoints on
  devices on the same LAN, each running a lightweight snapshell daemon, all
  streaming screenshots and clipboard content to your main daemon over the
  socket protocol. Screenshot a router's config page on your phone's browser
  on a shared host, or capture from a second laptop, and the image lands in
  the active session's `blog.md` — captioned from anywhere, edited nowhere.
- **tmux-native inventory rendering.** The in-terminal screenshot preview
  and the blog render currently use the kitty graphics protocol, so the
  review TUI shows images only inside kitty. Making the same previews work
  inside tmux (via kitty's tmux passthrough, or by delegating to an external
  viewer pane) would lift the kitty requirement for inventory mode.
- **User plugins in Go.** A plugin interface for hooking into the capture
  flows, so you can add your own post-processing without forking the project:
  for example, an analyzer that scans every captured command and its output
  for secrets — passwords, API keys, hashes — and flags or strips them before
  they land in `blog.md`. Plugins are just Go, compiled against a small
  public API, so anything you can write in Go can become a plugin.
- **Recommended future features.**
  - a secrets/credential scrubber built in as the reference plugin (gitleaks-style
    scanning of captured output),
  - session metadata at start (target, box, user) written into the blog's
    front-matter,
  - a daily session digest / summary generator,
  - exporting a session to HTML/PDF for sharing,
  - optional GPG encryption of a session folder for sensitive engagements.

---

## Development

```sh
make check          # gofmt + vet + all tests
make build          # static binary at ./bin/snapshell
```

Layout (each module has its own `AGENTS.md` with details):

```
cmd/snapshell/            CLI + daemon IPC protocol + setup wizard
internal/daemon/          daemon lifecycle, socket server, session state
internal/hotkeys/         X11 global key grabbing
internal/capture/screenshot/  screenshot tool invocation
internal/capture/tmuxcap/     tmux/kitty command + output capture
internal/shellhook/       bash/zsh hook scripts + marker records
internal/popup/           zenity caption/note window
internal/tui/             the review TUI (inventory mode + blog render)
internal/blog/            blog.md writer, formatting contract
internal/config/          TOML config schema + defaults
systemd/                  user service unit
scripts/setup.sh          one-shot install
```

No panics in the daemon: every runtime error is caught, logged, and where it
affects your current action surfaced as a `notify-send`.

---

## License

[MIT](LICENSE)