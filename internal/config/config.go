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
	"time"

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
	Themes     ThemesConfig     `toml:"themes"`
	Blog       BlogConfig       `toml:"blog"`
	Inventory  InventoryConfig  `toml:"inventory"`
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
	// CountTimeoutMs is how long after Alt+2 the daemon waits for a digit
	// (1-9) that sets how many recent commands to capture at once. 0 or
	// negative = DefaultCommandCountTimeout.
	CountTimeoutMs int `toml:"count_timeout_ms"`
}

// DefaultPopupWidth/Height are the reference popup dimensions. The 560:320
// (1.75:1) ratio is what keep_ratio preserves.
const (
	DefaultPopupWidth  = 560
	DefaultPopupHeight = 320
)

// DefaultCommandCountTimeout is how long Alt+2 waits for a count digit when
// count_timeout_ms is unset or non-positive.
const DefaultCommandCountTimeout = 1500

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

// ThemesConfig configures the GTK theme applied to the popup window.
type ThemesConfig struct {
	// Name is the GTK theme passed to the popup via the GTK_THEME
	// environment variable (e.g. "Sweet" or "Sweet:dark"). Empty = the
	// system's default theme. See `snapshell list-themes` for what's
	// installed.
	Name string `toml:"name"`
	// Root is an extra directory to scan for installed themes, for people
	// who install them outside the standard locations (/usr/share/themes,
	// ~/.themes, ~/.local/share/themes). Empty = standard locations only.
	Root string `toml:"root"`
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

// DefaultInventoryCloseDelay is how long the review TUI leaves an image open
// in the external viewer before trying to close it, when close_delay_secs is
// unset or non-positive.
const DefaultInventoryCloseDelay = 5

// DefaultImageScalePercent is the default [inventory].image_scale_percent
// for tab (full-screen) rendering: 100 = the image is rendered as large as
// the pane allows. Lower values render the image proportionally smaller
// while keeping its aspect ratio. An unset key means "use the per-mode
// default" (100 for tab, 50 for inline).
const DefaultImageScalePercent = 100

// DefaultBlogImageScalePercent is the default
// [inventory].blog_image_scale_percent for the blog render view: 100 = each
// screenshot is rendered as large as the render pane allows. An unset key
// means 100.
const DefaultBlogImageScalePercent = 100

// MaxInlineImageScalePercent is the hard cap on the inline preview image
// size: even an explicit image_scale_percent above this is clamped down, so
// the in-pane preview can never take over more than ~2/3 of the pane.
const MaxInlineImageScalePercent = 65

// BlogConfig configures how blog.md entries are laid out.
type BlogConfig struct {
	// CaptionPosition places the caption of an image/code entry either
	// above ("above", default) or below ("below") the image/code block.
	// Note entries have no caption and ignore it.
	CaptionPosition string `toml:"caption_position"`
}

// InventoryConfig configures the review TUI's screenshot preview
// (inventory mode).
type InventoryConfig struct {
	// ImageViewer is the binary used to open captured screenshots for a
	// quick look. Empty = the system default (xdg-open).
	ImageViewer string `toml:"image_viewer"`
	// CloseDelaySecs is how long an opened image stays on screen before the
	// TUI best-effort closes it (default DefaultInventoryCloseDelay).
	CloseDelaySecs int `toml:"close_delay_secs"`
	// ImageMode selects how Enter on an image card shows the screenshot:
	// "kitty" renders it full-screen inside the terminal (kitty only, falls
	// back to the external viewer otherwise); "external" opens it in
	// ImageViewer. Default "kitty".
	ImageMode string `toml:"image_mode"`
	// ImageScalePercent is how large the in-terminal image is rendered as a
	// percentage of the size that would exactly fit the pane: 100 (default,
	// when unset) = full fit, 50 = half size. A *int so an unset key is
	// distinguishable from an explicit value: the inline preview defaults to
	// 50% when unset and never exceeds MaxInlineImageScalePercent. Ignored in
	// "external" mode.
	ImageScalePercent *int `toml:"image_scale_percent"`
	// ImageRender chooses where the in-terminal screenshot is shown: "tab"
	// (default) opens it full-screen on Enter, "inline" renders it directly
	// in the preview pane for the selected image card (no Enter needed; Enter
	// still opens the full-screen zoom). Ignored in "external" mode.
	ImageRender string `toml:"image_render"`
	// BlogImageScalePercent is how large the screenshots embedded in the
	// "view blog" render are drawn, as a percentage of the size that would
	// exactly fit the render pane: 100 (default, when unset) = full fit, 50
	// = half size. Distinct from ImageScalePercent, which governs the
	// inventory image card previews. A *int so an unset key is
	// distinguishable from an explicit value.
	BlogImageScalePercent *int `toml:"blog_image_scale_percent"`
}

// Default returns the built-in configuration values. SessionRoot is the
// per-user location sessions land in (a leading "~/" is expanded by Load).
func Default() *Config {
	includeOutput := true
	keepRatio := true
	reloadOnHotkey := false
	return &Config{
		Screenshot: ScreenshotConfig{Tool: "flameshot"},
		Capture:    CaptureConfig{IncludeOutput: &includeOutput, ReloadOnHotkey: &reloadOnHotkey, CountTimeoutMs: DefaultCommandCountTimeout},
		Popup:      PopupConfig{Width: DefaultPopupWidth, Height: DefaultPopupHeight, Font: "Sans 13", KeepRatio: &keepRatio},
		Keymaps:    KeymapConfig{Screenshot: "Alt+1", Command: "Alt+2", Note: "Alt+3", Selection: "Alt+4", Reload: "Alt+5"},
		Paths:      PathsConfig{SessionRoot: "~/.local/share/snapshell"},
		Themes:     ThemesConfig{},
		Blog:       BlogConfig{CaptionPosition: "above"},
		Inventory:  InventoryConfig{CloseDelaySecs: DefaultInventoryCloseDelay, ImageMode: "kitty", ImageRender: "tab"},
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

// CountTimeout returns how long Alt+2 waits for a command-count digit after
// the hotkey fires (default 1500ms).
func (c *Config) CountTimeout() time.Duration {
	ms := c.Capture.CountTimeoutMs
	if ms <= 0 {
		ms = DefaultCommandCountTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

// CaptionAfter reports whether image/code captions are placed below the
// block in blog.md instead of above it. Unknown or empty values resolve to
// "above" (the default).
func (c *Config) CaptionAfter() bool {
	return strings.ToLower(strings.TrimSpace(c.Blog.CaptionPosition)) == "below"
}

// CloseDelay returns how long the review TUI leaves an opened image on
// screen before best-effort closing it (default 5s).
func (c *Config) CloseDelay() time.Duration {
	secs := c.Inventory.CloseDelaySecs
	if secs <= 0 {
		secs = DefaultInventoryCloseDelay
	}
	return time.Duration(secs) * time.Second
}

// ImageMode returns how the review TUI shows screenshots on Enter: "kitty"
// renders them full-screen in the terminal (only when running under kitty),
// "external" opens them in the configured image viewer. Unknown or empty
// values resolve to "kitty".
func (c *Config) ImageMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.Inventory.ImageMode))
	if mode != "kitty" && mode != "external" {
		mode = "kitty"
	}
	return mode
}

// ImageRender returns how the review TUI shows in-terminal screenshots:
// "tab" opens them full-screen on Enter (default), "inline" renders them in
// the preview pane for the selected image card. Unknown or empty values
// resolve to "tab".
func (c *Config) ImageRender() string {
	mode := strings.ToLower(strings.TrimSpace(c.Inventory.ImageRender))
	if mode != "tab" && mode != "inline" {
		mode = "tab"
	}
	return mode
}

// ImageScale returns the multiplier for the full-screen (tab) image size,
// derived from [inventory].image_scale_percent: 1.0 renders the image as
// large as the pane allows, 0.5 half that, etc. The aspect ratio is always
// kept. An unset key or a percent outside the sane 1..100 range resolves to
// the default 100 (multiplier 1.0).
func (c *Config) ImageScale() float64 {
	pct := c.Inventory.ImageScalePercent
	if pct == nil {
		return 1
	}
	v := *pct
	if v < 1 || v > 100 {
		v = DefaultImageScalePercent
	}
	return float64(v) / 100
}

// ImageScaleInline returns the multiplier for the inline preview image,
// derived from [inventory].image_scale_percent with inline-specific bounds:
// an unset key defaults to 50% of the pane fit, an explicit value is used
// as-is up to MaxInlineImageScalePercent (65%), beyond which it is clamped.
func (c *Config) ImageScaleInline() float64 {
	pct := c.Inventory.ImageScalePercent
	if pct == nil {
		return 0.5
	}
	v := *pct
	if v < 1 {
		v = 1
	}
	if v > 100 {
		v = 100
	}
	scale := float64(v) / 100
	if scale > float64(MaxInlineImageScalePercent)/100 {
		scale = float64(MaxInlineImageScalePercent) / 100
	}
	return scale
}

// BlogImageScale returns the multiplier for the screenshots in the "view
// blog" render, derived from [inventory].blog_image_scale_percent: 1.0
// renders each image as large as the render pane allows, 0.5 half that, etc.
// The aspect ratio is always kept. An unset key or a percent outside the
// sane 1..100 range resolves to the default 100 (multiplier 1.0).
func (c *Config) BlogImageScale() float64 {
	pct := c.Inventory.BlogImageScalePercent
	if pct == nil {
		return 1
	}
	v := *pct
	if v < 1 || v > 100 {
		v = DefaultBlogImageScalePercent
	}
	return float64(v) / 100
}

// ThemeSearchDirs returns the directories to scan for installed GTK themes:
// the standard system and per-user locations plus the configured
// `[themes].root`. A leading "~/" in root is expanded. Duplicates are
// dropped. Used by `snapshell list-themes`.
func (c *Config) ThemeSearchDirs() []string {
	var dirs []string
	add := func(d string) {
		for _, have := range dirs {
			if have == d {
				return
			}
		}
		dirs = append(dirs, d)
	}
	add("/usr/share/themes")
	add("/usr/local/share/themes")
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".themes"))
		add(filepath.Join(home, ".local", "share", "themes"))
	}
	if strings.TrimSpace(c.Themes.Root) != "" {
		add(expandPath(strings.TrimSpace(c.Themes.Root)))
	}
	return dirs
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
  # Milliseconds after Alt+2 during which pressing a number (1-9) sets how
  # many recent commands to capture at once (Alt+2 then 2 = the last two
  # commands, captured together). 0 = 1500. No number pressed = capture the
  # last command only.
  count_timeout_ms = 1500

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

[themes]
  # GTK theme applied to the popup window, e.g. "Sweet" or "Sweet:dark"
  # (GTK_THEME env var). Empty = the system's default theme. See
  # 'snapshell list-themes' for what's installed on this machine.
  name = ""
  # Extra directory to scan for installed themes, for themes installed
  # outside the standard locations (/usr/share/themes, ~/.themes,
  # ~/.local/share/themes). Empty = standard locations only.
  root = ""

[inventory]
  # Inventory mode: captures land silently in a pending queue reviewed in
  # 'snapshell inventory'. Image viewer used to peek at a captured
  # screenshot from the review TUI (Enter on an image card). Empty = the
  # system default (xdg-open). Set it to a viewer like "feh" for a
  # guaranteed auto-close.
  image_viewer = ""
  # Seconds an opened image stays up before the TUI best-effort closes it
  # (0 = 5). Auto-close may not fire for default viewers that hand the
  # image to an already-running instance.
  close_delay_secs = 5
  # How Enter on an image card shows the screenshot: "kitty" renders it
  # full-screen inside the terminal (requires running in kitty; falls back
  # to the external viewer otherwise), "external" opens it in image_viewer.
  image_mode = "kitty"
  # Where the in-terminal screenshot is shown: "tab" opens it full-screen on
  # Enter (default), "inline" renders it right in the preview pane for the
  # selected image card — no Enter needed (Enter still zooms full-screen).
  image_render = "tab"
  # How large the in-terminal image is rendered, as a percentage of the size
  # that would exactly fit the pane. Tab mode: 100 = full fit when unset.
  # Inline mode: unset = 50% of the pane fit, and the value never exceeds
  # 65%. The aspect ratio is always preserved. Ignored in "external" mode.
  # image_scale_percent = 60
  # How large the screenshots embedded in the "view blog" render are drawn,
  # as a percentage of the size that would exactly fit the render pane.
  # 100 = full fit when unset (default). Distinct from image_scale_percent,
  # which controls the inventory image card previews.
  # blog_image_scale_percent = 100

[blog]
  # Where the caption of an image/code entry sits in blog.md relative to
  # the image/code block: "above" (default) or "below". Note entries have
  # no caption and ignore this.
  caption_position = "above"
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
	if c.Capture.CountTimeoutMs <= 0 {
		c.Capture.CountTimeoutMs = def.Capture.CountTimeoutMs
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
	if c.Inventory.CloseDelaySecs <= 0 {
		c.Inventory.CloseDelaySecs = def.Inventory.CloseDelaySecs
	}
	if strings.TrimSpace(c.Inventory.ImageMode) == "" {
		c.Inventory.ImageMode = def.Inventory.ImageMode
	}
	if strings.TrimSpace(c.Inventory.ImageRender) == "" {
		c.Inventory.ImageRender = def.Inventory.ImageRender
	}
	// A non-positive explicit image_scale_percent is treated as unset so the
	// per-mode defaults (tab 100, inline 50) apply.
	if c.Inventory.ImageScalePercent != nil && *c.Inventory.ImageScalePercent <= 0 {
		c.Inventory.ImageScalePercent = nil
	}
	// Same for blog_image_scale_percent: a non-positive explicit value means
	// "use the default" (100).
	if c.Inventory.BlogImageScalePercent != nil && *c.Inventory.BlogImageScalePercent <= 0 {
		c.Inventory.BlogImageScalePercent = nil
	}
	if strings.TrimSpace(c.Blog.CaptionPosition) == "" {
		c.Blog.CaptionPosition = def.Blog.CaptionPosition
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
