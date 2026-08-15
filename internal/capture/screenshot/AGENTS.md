# AGENTS.md — internal/capture/screenshot

Owns: invoking a screenshot tool in region-select mode and confirming the
resulting file landed where expected. This is the Alt+1 flow's first half
(everything up to "file exists on disk") — the popup/caption/blog-append
tail is shared logic living in `internal/popup` and `internal/blog`.

## Tool selection & fallback

- Configured via `[screenshot].tool` in the TOML config
  (`internal/config`); default `"flameshot"`.
- Resolution order at capture time:
  1. If configured tool is on `$PATH`, use it.
  2. If not, and it was the default (`flameshot`), fall back to
     `mate-screenshot -a` (MATE's built-in region-select screenshot tool)
     and log a one-time warning that flameshot wasn't found.
  3. If neither is available, fail the capture with a `notify-send`
     naming both tools and do not create an empty/broken attachment file.
- Exact invocations:
  - flameshot: `flameshot gui --path <session>/attachments/<NNN>.png`
    (flameshot's own GUI provides region selection and its own
    annotation tools before save — that's fine, out of scope to wrap).
  - mate-screenshot fallback: `mate-screenshot -a
    --file=<session>/attachments/<NNN>.png` (confirm actual current
    mate-screenshot flag names against the installed version — flag names
    have changed across MATE releases, verify with `mate-screenshot
    --help` rather than assuming).

## Filename convention

- Zero-padded 3-digit sequential number scoped to the *session's*
  `attachments/` folder, not global: `001.png`, `002.png`, ... Determine
  the next number by counting existing files in `attachments/` at capture
  time (daemon's in-memory counter is the fast path; falling back to a
  directory scan handles the crash-recovery case described in
  `internal/daemon/AGENTS.md`).
- Always `.png` — don't try to detect/preserve other formats.

## Failure handling

- If the user cancels flameshot's region select (e.g. presses Esc), no
  file is written. Detect this (empty/missing output file after the
  process exits) and treat it as "capture cancelled" — no blog.md entry,
  no popup, no error notification (cancelling isn't an error).
- If the process exits non-zero for any other reason, that *is* an error —
  notify and log, don't proceed to open the popup with a missing file.
- Timeout: if the screenshot tool hangs for an unreasonable time (e.g. the
  user alt-tabs away and forgets about it), do not impose an artificial
  timeout — region-select tools are inherently interactive and the user
  may take a while. Just make sure the daemon isn't blocked waiting on it
  (see concurrency note in `internal/daemon/AGENTS.md` — this should
  already be running in its own goroutine).
