# AGENTS.md — internal/config

Owns: loading `~/.config/snapshell/config.toml`, providing defaults, and
resolving fallbacks for missing external tools. Every other package that
needs a config value should get it through this package's typed struct,
not by reading TOML itself.

## Schema

```toml
[screenshot]
tool = "flameshot"        # or "mate-screenshot"

[popup]
terminal = "alacritty"    # falls back through kitty, xterm if not found
width_cells = 100
height_cells = 30

[paths]
session_root = "~/htblog"
```

Wait — check `session_root`'s default against the rest of the spec: the
project-wide default session root is `~/snapshell`, not `~/htblog` (that
was the tool's old working name before it was renamed). Use
`~/snapshell` as the actual default value here.

## Behavior

- `Load() (*Config, error)`: if `~/.config/snapshell/config.toml` doesn't
  exist, create it with the full default values written out (not just an
  empty file) so the user has something to look at and edit, then return
  the defaults. If it exists but is missing individual keys, fill in
  defaults for just those keys rather than erroring — partial configs are
  fine.
- Expand `~` in `session_root` (and anywhere else a path appears) using
  the real home directory (`os.UserHomeDir()`), don't rely on shell
  expansion since Go isn't invoking a shell to read this value.
- **Tool resolution is this package's job, exposed as a method other
  packages call rather than re-implementing `$PATH` lookups themselves**,
  e.g.:
  ```go
  func (c *Config) ResolveScreenshotTool() (bin string, err error)
  func (c *Config) ResolvePopupTerminal() (bin string, err error)
  ```
  Each checks the configured value against `exec.LookPath`, falls through
  the documented fallback list (screenshot: flameshot → mate-screenshot;
  terminal: configured → alacritty → kitty → xterm), and returns a
  specific error naming every option that was tried and not found if all
  of them fail — so the eventual `notify-send`/log message can be
  concrete ("none of alacritty, kitty, xterm found on PATH") rather than
  vague.

## What NOT to do here

- Don't validate `width_cells`/`height_cells` against actual screen
  resolution or attempt "smart" auto-sizing — take the configured numbers
  as-is and hand them to `internal/popup`'s window-spawning code, which
  passes them straight to the terminal emulator's `-o
  window.dimensions.columns/lines`-style flags (exact flags vary by
  emulator, that's `internal/popup`'s concern, not this package's).
- Don't add config keys beyond what's listed above unless a build step
  elsewhere in this repo's AGENTS.md files explicitly references one that
  doesn't exist yet here — keep schema and usage in sync rather than
  speculatively adding options nothing reads.
