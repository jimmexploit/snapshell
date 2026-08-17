// Package config loads ~/.config/snapshell/config.toml and provides typed
// defaults and external-tool resolution. Every other package that needs a
// config value reads it through this package's structs, never by parsing
// TOML itself.
package config

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the full snapshell configuration. Zero-valued keys are filled
// in with defaults by Load.
type Config struct {
	Screenshot ScreenshotConfig `toml:"screenshot"`
	Capture    CaptureConfig    `toml:"capture"`
	Popup      PopupConfig      `toml:"popup"`
	Keymaps    KeymapConfig     `toml:"keymaps"`
	Paths      PathsConfig      `toml:"paths"`
}

// ScreenshotConfig configures the Alt+1 capture tool.
type ScreenshotConfig struct {
	Tool string `toml:"tool"`
}

// CaptureConfig configures the Alt+2 tmux capture scope and per-capture
// config reloading.
type CaptureConfig struct {
	// IncludeOutput captures the command's full output alongside the
	// prompt+command lines; when false only the prompt+command line(s) are
	// kept. A *bool (not bool) so an explicit `include_output = false` in
	// the config file is preserved instead of being indistinguishable from
	// a missing key.
	IncludeOutput *bool `toml:"include_output"`
	// ReloadOnHotkey re-reads the config file before every capture flow
	// (Alt+1/2/3/4) so edits apply without restarting the daemon. Default
	// false — the reload hotkey covers manual reloads.
	ReloadOnHotkey *bool `toml:"reload_on_hotkey"`
}

// DefaultPopupWidth/Height are the reference popup dimensions. The 560:320
// (1.75:1) ratio is what keep_ratio preserves.
const (
	DefaultPopupWidth  = 560
	DefaultPopupHeight = 320
)

// PopupConfig configures the zenity caption/note window.
type PopupConfig struct {
	// Width sizes the caption window in pixels (0 = let zenity pick).
	Width int `toml:"width"`
	// Height sizes the caption/note text area in pixels (0 = let zenity
	// pick) — every popup input is a text area that fills the window.
	Height int `toml:"height"`
	// Font is a Pango font description (e.g. "Sans 13") for the text area
	// the user types into. Empty = zenity's default font.
	Font string `toml:"font"`
	// KeepRatio locks the window to the default 560:320 aspect ratio: if
	// the user changes only one dimension (width OR height) away from the
	// default, the other is recomputed to preserve the ratio. Both set to
	// non-default values are honored as-is. Default true. A *bool so an
	// explicit `keep_ratio = false` survives the fill-in-defaults pass.
	KeepRatio *bool `toml:"keep_ratio"`
	// Position moves the dialog window to a spot on screen after it
	// spawns. A named preset ("center", "top-left", "top-center",
	// "top-right", "center-left", "center-right", "bottom-left",
	// "bottom-center", "bottom-right") or explicit pixels from the
	// top-left corner ("120,80"). Empty = leave placement to the window
	// manager.
	Position string `toml:"position"`
}

// PathsConfig configures where session folders live.
type PathsConfig struct {
	SessionRoot string `toml:"session_root"`
}

// KeymapConfig configures the global hotkeys. Values are user-friendly
// combos like "Alt+1" or "Ctrl+Shift+F5"; Alt = Mod1, Ctrl = Control,
// Super/Win = Mod4 (the raw Mod1..Mod5 names are accepted too).
type KeymapConfig struct {
	Screenshot string `toml:"screenshot"`
	Command    string `toml:"command"`
	Note       string `toml:"note"`
	Selection  string `toml:"selection"`
	// Reload re-reads the config file (and re-grabs hotkeys) without
	// restarting the daemon.
	Reload string `toml:"reload"`
}

// Default returns the built-in configuration values. SessionRoot is the
// per-user location sessions land in (a leading "~/" is expanded by Load).
func Default() *Config {
	includeOutput := true
	keepRatio := true
	reloadOnHotkey := false
	return &Config{
		Screenshot: ScreenshotConfig{Tool: "flameshot"},
		Capture:    CaptureConfig{IncludeOutput: &includeOutput, ReloadOnHotkey: &reloadOnHotkey},
		Popup:      PopupConfig{Width: DefaultPopupWidth, Height: DefaultPopupHeight, Font: "Sans 13", KeepRatio: &keepRatio},
		Keymaps:    KeymapConfig{Screenshot: "Alt+1", Command: "Alt+2", Note: "Alt+3", Selection: "Alt+4", Reload: "Alt+5"},
		Paths:      PathsConfig{SessionRoot: "~/.local/share/snapshell"},
	}
}

// KeepRatioOn reports whether the popup aspect-ratio lock is enabled
// (default true).
func (c *Config) KeepRatioOn() bool {
	return c.Popup.KeepRatio != nil && *c.Popup.KeepRatio
}

// OutputIncluded reports whether Alt+2 should capture the command's full
// output (default true).
func (c *Config) OutputIncluded() bool {
	return c.Capture.IncludeOutput != nil && *c.Capture.IncludeOutput
}

// ReloadOnHotkeyOn reports whether the config file is re-read before each
// capture flow (default false).
func (c *Config) ReloadOnHotkeyOn() bool {
	return c.Capture.ReloadOnHotkey != nil && *c.Capture.ReloadOnHotkey
}

// ConfigPath returns the default config file location.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %v", err)
	}
	return filepath.Join(home, ".config", "snapshell", "config.toml"), nil
}

// Load reads the config file, creating it with the full defaults written
// out if it does not exist yet (so the user has something to edit). Missing
// individual keys are filled with defaults rather than erroring — partial
// configs are fine.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads the config from an explicit path. Used by tests; Load is
// the production entry point.
func LoadFrom(path string) (*Config, error) {
	cfg := Default()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeDefault(path); err != nil {
			return nil, err
		}
		expandCfg(cfg)
		return cfg, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat config %s: %v", path, err)
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %v", path, err)
	}

	fillDefaults(cfg)
	normalizeKeepRatio(cfg)
	expandCfg(cfg)
	return cfg, nil
}

// ResetDefault backs up the current config file (if any) to <path>.bak and
// writes a fresh default config in its place. Used by the setup wizard when
// the user asks to reset their configuration.
func ResetDefault() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, path+".bak"); err != nil {
			return fmt.Errorf("backup config to %s.bak: %v", path, err)
		}
	}
	if err := writeDefault(path); err != nil {
		return fmt.Errorf("write default config %s: %v", path, err)
	}
	return nil
}

// expandCfg normalizes path values (leading ~/) in a loaded config.
func expandCfg(c *Config) {
	c.Paths.SessionRoot = expandPath(c.Paths.SessionRoot)
}

// writeDefault creates the config directory and writes a documented default
// file so the user has something to look at and edit. Written as a template
// (not toml.NewEncoder) so each key can carry a comment; values must stay
// in sync with Default().
func writeDefault(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %v", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create config %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(defaultFileText); err != nil {
		return fmt.Errorf("write config %s: %v", path, err)
	}
	return nil
}

// defaultFileText is the commented-on-disk form of Default().
const defaultFileText = `# snapshell configuration
# paths below are relative to your home unless absolute

[screenshot]
  tool = "flameshot"        # "flameshot" or "mate-screenshot"

[popup]
  # Width of the caption window in pixels. 0 = let zenity pick.
  width = 560
  # Height of the caption/note text area in pixels — every popup input is
  # a text area that fills the window, so this sizes the box you type in.
  # 0 = let zenity pick.
  height = 320
  # Font of the text you type (Pango font description). Empty = zenity's
  # default. "Sans 13" is a comfortable step up from the 10pt desktop font.
  font = "Sans 13"
  # Lock the window to the default 560:320 aspect ratio: change ONE of
  # width/height and the other follows to keep the ratio. Set both to
  # non-default values and both are honored. false = size each freely.
  keep_ratio = true
  # Where the popup window appears after spawning. A named preset
  # ("center", "top-left", "top-center", "top-right", "center-left",
  # "center-right", "bottom-left", "bottom-center", "bottom-right") or
  # explicit pixels from the top-left corner ("120,80"). Empty = let the
  # window manager place it. Requires xdotool.
  position = ""

[capture]
  # false = Alt+2 captures only the command line (and its prompt lines),
  # skipping the command's output.
  include_output = true
  # true = re-read this config file before every hotkey capture (Alt+1/2/3/4)
  # so edits apply immediately. false = apply changes via the reload hotkey
  # (or by restarting the daemon).
  reload_on_hotkey = false

[keymaps]
  # Global hotkeys. Format: modifiers separated by "+", then a key.
  # Alt = Mod1, Ctrl = Control, Shift, Super/Win = Mod4; raw Mod1..Mod5
  # names work too. The key is any X11 keysym (a letter, number, F1-F12,
  # Return, space, ...).
  screenshot = "Alt+1"
  command    = "Alt+2"
  note       = "Alt+3"
  selection  = "Alt+4"
  reload     = "Alt+5"   # re-read config + re-grab hotkeys, no restart

[paths]
  # Where session folders (and their blog.md + attachments/) are stored.
  # "~/.local/share/snapshell" is the default (always writable); you can
  # use any path you have write access to (e.g. "~/snapshell").
  session_root = "~/.local/share/snapshell"
`

// fillDefaults replaces empty/zero values with the built-in defaults.
func fillDefaults(c *Config) {
	def := Default()
	if strings.TrimSpace(c.Screenshot.Tool) == "" {
		c.Screenshot.Tool = def.Screenshot.Tool
	}
	if c.Capture.IncludeOutput == nil {
		c.Capture.IncludeOutput = def.Capture.IncludeOutput
	}
	if c.Capture.ReloadOnHotkey == nil {
		c.Capture.ReloadOnHotkey = def.Capture.ReloadOnHotkey
	}
	if c.Popup.Width <= 0 {
		c.Popup.Width = def.Popup.Width
	}
	if c.Popup.Height <= 0 {
		c.Popup.Height = def.Popup.Height
	}
	if c.Popup.KeepRatio == nil {
		c.Popup.KeepRatio = def.Popup.KeepRatio
	}
	if c.Popup.Position == "" {
		c.Popup.Position = def.Popup.Position
	}
	if strings.TrimSpace(c.Keymaps.Screenshot) == "" {
		c.Keymaps.Screenshot = def.Keymaps.Screenshot
	}
	if strings.TrimSpace(c.Keymaps.Command) == "" {
		c.Keymaps.Command = def.Keymaps.Command
	}
	if strings.TrimSpace(c.Keymaps.Note) == "" {
		c.Keymaps.Note = def.Keymaps.Note
	}
	if strings.TrimSpace(c.Keymaps.Selection) == "" {
		c.Keymaps.Selection = def.Keymaps.Selection
	}
	if strings.TrimSpace(c.Keymaps.Reload) == "" {
		c.Keymaps.Reload = def.Keymaps.Reload
	}
	if strings.TrimSpace(c.Paths.SessionRoot) == "" {
		c.Paths.SessionRoot = def.Paths.SessionRoot
	}
}

// normalizeKeepRatio applies the popup aspect-ratio lock. When keep_ratio
// is on, changing exactly one of width/height away from its default makes
// the other follow so the 560:320 ratio always holds. Both dimensions
// explicitly set to non-default values are honored as-is (the user
// outranks the ratio), and neither being changed leaves them untouched.
func normalizeKeepRatio(c *Config) {
	if !c.KeepRatioOn() {
		return
	}
	p := &c.Popup
	switch {
	case p.Width != DefaultPopupWidth && p.Height == DefaultPopupHeight:
		p.Height = int(math.Round(float64(p.Width) * DefaultPopupHeight / DefaultPopupWidth))
	case p.Height != DefaultPopupHeight && p.Width == DefaultPopupWidth:
		p.Width = int(math.Round(float64(p.Height) * DefaultPopupWidth / DefaultPopupHeight))
	}
}

// expandPath expands a leading ~/ to the real home directory. Go never
// runs the value through a shell, so this expansion is this package's job.
func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}

// ResolveScreenshotTool finds a usable screenshot binary. Order: configured
// tool, then (when the configured tool is the default flameshot) the
// mate-screenshot fallback. Returns a specific error naming every option
// tried when none is available.
func (c *Config) ResolveScreenshotTool() (string, error) {
	candidates := []string{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		for _, have := range candidates {
			if have == s {
				return
			}
		}
		if s != "" {
			candidates = append(candidates, s)
		}
	}
	add(c.Screenshot.Tool)
	if strings.TrimSpace(c.Screenshot.Tool) == "" || c.Screenshot.Tool == "flameshot" {
		add("mate-screenshot")
	}
	for _, name := range candidates {
		if _, err := exec.LookPath(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("cannot capture screenshot: none of %s found on PATH — install one", strings.Join(candidates, ", "))
}
