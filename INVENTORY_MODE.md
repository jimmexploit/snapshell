# Inventory Mode

This document describes a new session mode: **Inventory Mode**. It is
written as a requirements/behavior spec, not a file-by-file implementation
plan — the existing codebase already has its own structure and
conventions (see `internal/daemon`, `internal/hotkeys`, `internal/popup`,
`internal/capture/*`, `internal/notify`, `internal/blog`,
`internal/config`, and their respective `AGENTS.md` files). Decide how
this fits into that structure yourself: whether it's new files, new
packages, or additions to existing ones (e.g. `internal/hotkeys/
dispatcher.go` already has mode-dispatch-shaped logic — extending it may
make more sense than inventing a parallel dispatcher). Read the existing
code before deciding where things go.

**This is additive.** Normal mode's existing capture flow — hotkeys →
GTK/Qt popup → caption → `blog.md` — must keep working exactly as it does
today. If implementing something below seems to require changing how
`internal/popup`, `internal/capture/screenshot`, `internal/capture/
tmuxcap`, `internal/capture/selection`, or `internal/blog` currently
work internally, stop and treat that as a design smell, not a green light
to refactor them. The only expected touch points in existing code are:
adding a `Mode` concept to session state/IPC, adding a `--mode` flag and
an `inventory` subcommand, and adding new IPC verbs alongside whatever
`start`/`stop`/`status` already do in `internal/daemon`'s `ipc.go` /
`cmd/snapshell`'s `ipc.go`.

---

## What it's for

Normal mode interrupts the user for a caption on every single capture.
Inventory mode is for working through an HTB box without that
interruption: captures happen silently in the background, and the user
reviews and writes everything up in one pass afterward, in a dedicated
terminal UI.

## CLI shape

- `snapshell start <name>` — starts the daemon if it isn't already
  running (no separate `daemon start` step required first), then creates
  or resumes the session in **normal mode** (today's GTK/Qt popup
  behavior, unchanged).
- `snapshell start inventory <name>` — starts the daemon if needed,
  creates or resumes the session in **inventory mode**, and immediately
  opens the review TUI in the foreground of that same terminal. This is
  the *first* look at the TUI, not the only one — see below.
- `snapshell inventory` (no args) — reopens the review TUI for whatever
  session is currently active, as long as it's in inventory mode. This is
  how the user gets back into review after quitting the TUI to go run
  more commands: hotkey captures keep landing in the queue whether or not
  the TUI happens to be open, so this just needs to attach to whatever's
  already there.
- Resuming an existing session name (either form) should respect whatever
  mode that session was originally started with — don't let a bare
  `snapshell start <name>` silently downgrade an in-progress inventory
  session back to normal mode, and don't let `snapshell start inventory
  <name>` upgrade an existing normal-mode session either. If the
  requested mode conflicts with the session's existing mode, that's worth
  a clear error rather than silently picking one.
- There is no `--mode` flag — the mode is expressed by whether the literal
  word `inventory` appears as the first argument after `start`, not by a
  flag.
- `snapshell status` should still report the active session's mode.

## Capture behavior in inventory mode

- **Alt+1 (screenshot) and Alt+2 (command capture)**: run through the
  existing capture mechanics (same screenshot tool, same tmux row-range
  logic) but skip the popup entirely — no window, no interruption. The
  result becomes a **pending card** (see data model below) instead of an
  immediate `blog.md` entry. Fire a short notification via the existing
  `internal/notify` package confirming the capture landed and how many
  cards are now pending (e.g. "captured — 4 pending").
- **Alt+3 (raw note) does not participate in inventory mode.** It's a
  no-op beyond a notification telling the user to use the review TUI
  instead. Standalone notes in this mode are written only from inside the
  TUI (see below), never via a hotkey — there's no point queuing a card
  for something that's going to be discarded on write anyway.
- If no session is active, or the active session is in normal mode,
  behavior is unchanged from today.

## Data model

Two kinds of pending card: **image** (path to a captured screenshot,
under the session's existing `attachments/` convention) and **code**
(captured tmux text, same format the normal-mode Alt+2 flow already
produces). No "note" card kind — standalone notes bypass this entirely.

Whatever holds this queue needs to:
- Survive a daemon restart without losing unresolved cards (crash
  recovery matters here the same way it already matters for session/
  attachment-counter state elsewhere in the daemon).
- Be owned by a single writer (the daemon) — the review TUI is a client,
  not a second process touching the same files directly. Use the
  existing IPC mechanism for all mutations; reading `blog.md` itself for
  the read-only render view (below) is fine to do directly since that's
  non-mutating.

## New IPC verbs needed

Follow whatever request/response shape `internal/daemon`'s `ipc.go` /
`cmd/snapshell`'s `ipc.go` already use for `start`/`stop`/`status` — don't
invent a different protocol style for these:

- **list** pending cards (ordered oldest-first)
- **commit** a card — with an optional caption; empty caption means
  append as-is. On success this calls into the existing `internal/blog`
  append logic (same formatting contract normal mode already uses:
  timestamp comment, optional bold caption line, image/code block) and
  removes the card from the queue.
- **discard** a card — deletes the underlying file (image cards) and
  removes it from the queue. **Permanent, no trash/soft-delete.** The
  daemon should require an explicit confirmation flag in the request
  (don't rely on the TUI's own confirm prompt as the only safety check).

## The review TUI (`snapshell inventory`)

A standalone, foreground, terminal UI — distinct from the GTK/Qt popup
used at capture time in normal mode. It's a client of the daemon (talks
over the same IPC channel), reachable two ways: automatically, as the
last step of `snapshell start inventory <name>`, or later via a bare
`snapshell inventory` to reattach after having quit it. Either way it's
the same UI/same code path — `start inventory` shouldn't have a special
first-run variant, it just happens to chain straight into launching it.

Refuse to start (clear error, no blank/broken UI) if invoked as a bare
`snapshell inventory` with no active session, or with an active session
that's in normal mode. (When reached via `start inventory`, these cases
don't apply — the session was just created/resumed in inventory mode as
part of that same command.)

### Layout

Two columns: roughly 2/3 of the screen on the left for a detail view,
1/3 on the right for the card list, plus a footer showing available
keybindings for whatever state the UI is currently in.

- **Right (list)**: one line per pending card — kind icon, short label
  (filename, or first line of captured command), relative timestamp.
  Selected card highlighted. Empty state when there's nothing pending.
- **Left (detail)**: changes depending on what the user's doing —
  keep these as clearly distinct UI states, don't conflate them:
  1. **Card preview** (default): code cards show the captured text
     verbatim, scrollable if long. Image cards show a text label (file
     path + pixel dimensions) — no inline thumbnail, terminals can't do
     that reliably.
  2. **Caption input**: triggered on a selected card, a text field with
     a live rendered preview of what's being typed below it (debounced,
     not re-rendered on every keystroke — re-render after a short pause).
  3. **Standalone note input**: same idea, full-width, not tied to any
     card. On submit this goes straight to `blog.md` via the same
     `internal/blog` append path normal mode uses (as a plain paragraph,
     matching Alt+3's existing normal-mode formatting) — it was never a
     queued card, so there's nothing to remove from a queue.
- **Render view** (toggled full-screen): reads `blog.md` off disk
  directly and renders it read-only, for the user to see what's already
  been committed so far. Not a preview of pending cards.

### Actions

- Navigate the list (up/down).
- **Enter** on an image card: open the file in an external image viewer
  for a few seconds, then best-effort auto-close it (see below). No-op on
  code cards, since the text is already fully visible.
- **Append as-is**: commit the selected card with no caption.
- **Add caption then append**: opens caption input; on submit, commits
  with that caption.
- **Discard**: asks for an inline y/n confirmation first ("this cannot be
  undone") — no separate popup, keep it in the same terminal. Only
  proceeds to the IPC discard call on explicit confirmation.
- **New standalone note**: opens note input, available regardless of
  whether any card is selected or the queue is empty. Cancelling
  (`Esc`) before submitting discards the typed text, nothing is written.
- **Toggle render view.**
- **Quit**: safe at any time — nothing in the TUI holds state that isn't
  already durable on the daemon side or on disk, so there's no
  "unsaved work" to lose by quitting.

### Live updates

Poll the daemon's list verb periodically to pick up new cards captured
while the TUI is open (silent hotkey captures don't push to the TUI,
polling is fine — this doesn't need to be real-time). Pause polling
while the user is mid-caption/mid-note/mid-discard-confirmation so an
incoming update can't yank focus or reflow the list out from under
something they're actively typing.

### External image preview

Pressing Enter on an image card should open it in the system's default
image viewer, with a best-effort auto-close after a short delay (default
~5s). Use the system default (e.g. `xdg-open`) rather than requiring a
specific viewer to be installed — don't add a new binary dependency for
this. Be aware some default viewers hand off to an already-running
background instance instead of spawning a fresh, killable process, which
means the auto-close won't always actually happen — that's an acceptable
limitation, not something to engineer around. If the person wants a
guaranteed close, they can point the configured viewer at something like
`feh`, but that should be an opt-in override, never the default or a
requirement.

## Config

New settings needed, additive to whatever `internal/config` already
defines (don't restructure the existing schema, just add a section):
image viewer binary (default: system default / `xdg-open`), and the
auto-close delay in seconds (default: 5).

## Non-regression checklist

After building this, confirm — by actually running it, not just reading
the diff — that normal mode is completely unaffected: Alt+1/2/3 still
pop the GTK/Qt popup and write captions the same way they do today, for
a session started without `--mode inventory` (or with no flag at all).
