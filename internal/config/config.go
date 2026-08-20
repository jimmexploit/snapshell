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
	Image      ImageConfig      `toml:"image"`
	Auto       AutoConfig       `toml:"auto"`
	// Inventory is the legacy name for the image settings table: old config
	// files used [inventory] for these keys. Read for backward compatibility
	// and merged into Image wherever [image] leaves a key unset. Never
	// written out — the default file uses [image].
	Inventory ImageConfig `toml:"inventory"`
}

// AutoConfig configures auto mode: while an inventory session is active,
// every command that exits 0 is queued as a pending code card automatically,
// so the successful commands are waiting in the review TUI without pressing
// Alt+2. Excluded commands (e.g. "ls") are skipped by the auto path but can
// still be captured manually with Alt+2.
type AutoConfig struct {
	// Enabled turns auto capture on. Default false — Alt+2 stays the only
	// way commands land in the queue.
	Enabled bool `toml:"enabled"`
	// Exclude lists commands that the auto path skips even when they exit 0.
	// Each entry matches either the full command line or its first word
	// (e.g. "ls" also skips "ls -la"). Matches are case-sensitive.
	Exclude []string `toml:"exclude"`
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
	// Inventory keys for the review TUI ('snapshell inventory').
	Inventory InventoryKeymapConfig `toml:"inventory"`
}

// InventoryKeymapConfig configures the keys of the review TUI. Each value is
// a comma-separated list of key names exactly as the terminal reports them,
// e.g. "q", "ctrl+c", "up", "k", "enter", "esc", "pgup", "y", "Y". Empty =
// the default for that action. ctrl+c is always the quit/interrupt key while
// typing a caption or note (it cannot be rebound), because text input must
// never swallow a letter that is also bound to an action.
type InventoryKeymapConfig struct {
	Quit     string `toml:"quit"`
	Up       string `toml:"up"`
	Down     string `toml:"down"`
	PageUp   string `toml:"page_up"`
	PageDown string `toml:"page_down"`
	Append   string `toml:"append"`
	Caption  string `toml:"caption"`
	Discard  string `toml:"discard"`
	Note     string `toml:"note"`
	Blog     string `toml:"blog"`
	Open     string `toml:"open"`
	Submit   string `toml:"submit"`
	Cancel   string `toml:"cancel"`
	Confirm  string `toml:"confirm"`
	Decline  string `toml:"decline"`
}

// Default inventory-TUI key bindings. Values are comma-separated key names.
const (
	InventoryQuitDefault     = "q, ctrl+c"
	InventoryUpDefault       = "up, k"
	InventoryDownDefault     = "down, j"
	InventoryPageUpDefault   = "pgup"
	InventoryPageDownDefault = "pgdown"
	InventoryAppendDefault   = "a"
	InventoryCaptionDefault  = "c"
	InventoryDiscardDefault  = "d"
	InventoryNoteDefault     = "n"
	InventoryBlogDefault     = "v"
	InventoryOpenDefault     = "enter"
	InventorySubmitDefault   = "ctrl+s"
	InventoryCancelDefault   = "esc"
	InventoryConfirmDefault  = "y, Y"
	InventoryDeclineDefault  = "n, N"
)

// DefaultImageCloseDelay is how long the review TUI leaves an image open
// in the external viewer before trying to close it, when close_delay_secs is
// unset or non-positive.
const DefaultImageCloseDelay = 5

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

// DefaultBlogImagePadding is the default [inventory].blog_image_padding: the
// gap, in cells, kept between a screenshot and the left/right pane edge when
// [inventory].blog_image_align is "left" or "right". Center alignment ignores
// it (the image is always centered on the pane). An unset key means 2.
const DefaultBlogImagePadding = 2

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

// ImageConfig configures everything about screenshots in the review TUI
// (inventory mode): the external viewer, the in-terminal kitty rendering,
// and how screenshots are laid out in the "view blog" render.
type ImageConfig struct {
	// ImageViewer is the binary used to open captured screenshots for a
	// quick look. Empty = the system default (xdg-open).
	ImageViewer string `toml:"image_viewer"`
	// CloseDelaySecs is how long an opened image stays on screen before the
	// TUI best-effort closes it (default DefaultImageCloseDelay).
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
	// BlogImageAlign is where each screenshot in the "view blog" render sits
	// horizontally: "left" (default, flush to the pane edge), "center", or
	// "right". Ignored by image cards in the inventory view.
	BlogImageAlign string `toml:"blog_image_align"`
	// BlogImagePadding is the gap, in cells, between a screenshot and the
	// pane edge when BlogImageAlign is "left" or "right" (so the image isn't
	// glued to the edge). Ignored for "center" (and for the inventory view).
	// A *int so an explicit 0 (flush) is distinguishable from an unset key.
	BlogImagePadding *int `toml:"blog_image_padding"`
}

// defaultInventoryKeymap returns the built-in review-TUI key bindings.
func defaultInventoryKeymap() InventoryKeymapConfig {
	return InventoryKeymapConfig{
		Quit:     InventoryQuitDefault,
		Up:       InventoryUpDefault,
		Down:     InventoryDownDefault,
		PageUp:   InventoryPageUpDefault,
		PageDown: InventoryPageDownDefault,
		Append:   InventoryAppendDefault,
		Caption:  InventoryCaptionDefault,
		Discard:  InventoryDiscardDefault,
		Note:     InventoryNoteDefault,
		Blog:     InventoryBlogDefault,
		Open:     InventoryOpenDefault,
		Submit:   InventorySubmitDefault,
		Cancel:   InventoryCancelDefault,
		Confirm:  InventoryConfirmDefault,
		Decline:  InventoryDeclineDefault,
	}
}

// InventoryKeys returns the review-TUI key bindings with defaults filled in
// for any action left empty (partial configs are fine, like everywhere else).
func (c *Config) InventoryKeys() InventoryKeymapConfig {
	def := defaultInventoryKeymap()
	k := c.Keymaps.Inventory
	if strings.TrimSpace(k.Quit) == "" {
		k.Quit = def.Quit
	}
	if strings.TrimSpace(k.Up) == "" {
		k.Up = def.Up
	}
	if strings.TrimSpace(k.Down) == "" {
		k.Down = def.Down
	}
	if strings.TrimSpace(k.PageUp) == "" {
		k.PageUp = def.PageUp
	}
	if strings.TrimSpace(k.PageDown) == "" {
		k.PageDown = def.PageDown
	}
	if strings.TrimSpace(k.Append) == "" {
		k.Append = def.Append
	}
	if strings.TrimSpace(k.Caption) == "" {
		k.Caption = def.Caption
	}
	if strings.TrimSpace(k.Discard) == "" {
		k.Discard = def.Discard
	}
	if strings.TrimSpace(k.Note) == "" {
		k.Note = def.Note
	}
	if strings.TrimSpace(k.Blog) == "" {
		k.Blog = def.Blog
	}
	if strings.TrimSpace(k.Open) == "" {
		k.Open = def.Open
	}
	if strings.TrimSpace(k.Submit) == "" {
		k.Submit = def.Submit
	}
	if strings.TrimSpace(k.Cancel) == "" {
		k.Cancel = def.Cancel
	}
	if strings.TrimSpace(k.Confirm) == "" {
		k.Confirm = def.Confirm
	}
	if strings.TrimSpace(k.Decline) == "" {
		k.Decline = def.Decline
	}
	return k
}

// SplitKeyList splits a comma-separated key list into trimmed, non-empty
// entries. "q, ctrl+c" -> ["q", "ctrl+c"]; "" -> [].
func SplitKeyList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// DefaultAutoExclude are the commands skipped by auto mode out of the box.
// They are high-frequency or read-only and would drown the queue with
// noise; every one of them can still be captured manually with Alt+2.
var DefaultAutoExclude = []string{"ls", "cd", "clear", "pwd", "exit", "echo"}

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
		Keymaps:    KeymapConfig{Screenshot: "Alt+1", Command: "Alt+2", Note: "Alt+3", Selection: "Alt+4", Reload: "Alt+5", Inventory: defaultInventoryKeymap()},
		Paths:      PathsConfig{SessionRoot: "~/.local/share/snapshell"},
		Themes:     ThemesConfig{},
		Blog:       BlogConfig{CaptionPosition: "above"},
		Image:      ImageConfig{CloseDelaySecs: DefaultImageCloseDelay, ImageMode: "kitty", ImageRender: "tab"},
		Auto:       AutoConfig{Enabled: false, Exclude: append([]string(nil), DefaultAutoExclude...)},
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
	secs := c.Image.CloseDelaySecs
	if secs <= 0 {
		secs = DefaultImageCloseDelay
	}
	return time.Duration(secs) * time.Second
}

// ImageMode returns how the review TUI shows screenshots on Enter: "kitty"
// renders them full-screen in the terminal (only when running under kitty),
// "external" opens them in the configured image viewer. Unknown or empty
// values resolve to "kitty".
func (c *Config) ImageMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.Image.ImageMode))
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
	mode := strings.ToLower(strings.TrimSpace(c.Image.ImageRender))
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
	pct := c.Image.ImageScalePercent
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
	pct := c.Image.ImageScalePercent
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
	pct := c.Image.BlogImageScalePercent
	if pct == nil {
		return 1
	}
	v := *pct
	if v < 1 || v > 100 {
		v = DefaultBlogImageScalePercent
	}
	return float64(v) / 100
}

// BlogImageAlign returns how the screenshots in the "view blog" render are
// positioned horizontally: "left" (default), "center", or "right". Any other
// value is treated as "left".
func (c *Config) BlogImageAlign() string {
	if s := strings.TrimSpace(c.Image.BlogImageAlign); s != "" {
		return s
	}
	return "left"
}

// BlogImagePadding returns the edge gap in cells kept for left/right-aligned
// blog screenshots: 2 (default) when unset, an explicit value clamped to
// 0..100, and 2 again for a nonsensical (negative) value.
func (c *Config) BlogImagePadding() int {
	p := c.Image.BlogImagePadding
	if p == nil {
		return DefaultBlogImagePadding
	}
	v := *p
	if v < 0 {
		return DefaultBlogImagePadding
	}
	if v > 100 {
		return 100
	}
	return v
}

// AutoCaptureEnabled reports whether auto mode is on (default false).
func (c *Config) AutoCaptureEnabled() bool {
	return c.Auto.Enabled
}

// AutoCaptureExcluded reports whether a recorded command line should be
// skipped by the auto path. A command is excluded when it matches an
// [auto].exclude entry either by its full trimmed text or by its first
// whitespace-separated word ("ls -la" matches an "ls" entry). Matching is
// case-sensitive.
func (c *Config) AutoCaptureExcluded(command string) bool {
	full := strings.TrimSpace(command)
	if full == "" {
		return true
	}
	word := full
	if i := strings.IndexAny(word, " \t"); i >= 0 {
		word = word[:i]
	}
	for _, e := range c.Auto.Exclude {
		ex := strings.TrimSpace(e)
		if ex == "" {
			continue
		}
		if full == ex || word == ex {
			return true
		}
	}
	return false
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

	meta, err := toml.DecodeFile(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("parse config %s: %v", path, err)
	}

	mergeLegacyImage(cfg, meta)
	fillDefaults(cfg)
	normalizeKeepRatio(cfg)
	expandCfg(cfg)
	return cfg, nil
}

// mergeLegacyImage migrates the old [inventory] image table onto [image].
// Configs written before the rename put the image settings under [inventory];
// the [image] table wins wherever it explicitly sets a value, and any
// leftover legacy values fill in what [image] left unset — so an old config
// keeps working byte-for-byte without the user touching it. meta tells us
// which [image] keys were actually present in the file (as opposed to still
// at their Default() value after decode).
func mergeLegacyImage(c *Config, meta toml.MetaData) {
	legacy := c.Inventory
	img := &c.Image
	if !meta.IsDefined("image", "image_viewer") {
		img.ImageViewer = legacy.ImageViewer
	}
	if !meta.IsDefined("image", "close_delay_secs") {
		img.CloseDelaySecs = legacy.CloseDelaySecs
	}
	if !meta.IsDefined("image", "image_mode") {
		img.ImageMode = legacy.ImageMode
	}
	if !meta.IsDefined("image", "image_render") {
		img.ImageRender = legacy.ImageRender
	}
	if !meta.IsDefined("image", "image_scale_percent") {
		img.ImageScalePercent = legacy.ImageScalePercent
	}
	if !meta.IsDefined("image", "blog_image_scale_percent") {
		img.BlogImageScalePercent = legacy.BlogImageScalePercent
	}
	if !meta.IsDefined("image", "blog_image_align") {
		img.BlogImageAlign = legacy.BlogImageAlign
	}
	if !meta.IsDefined("image", "blog_image_padding") {
		img.BlogImagePadding = legacy.BlogImagePadding
	}
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
#
# This file is created automatically on first use. Every key below is
# optional — delete a line and its default kicks in. Paths are relative to
# your home unless absolute.
#
# Sections:
#   [screenshot]        how screenshots are taken (Alt+1)
#   [popup]             the caption/note dialog window
#   [capture]           how commands are captured (Alt+2)
#   [keymaps]           all hotkeys — global + review-TUI keys
#   [image]             how screenshots look in the review TUI and blog render
#   [auto]              auto-capture successful commands while in inventory mode
#   [blog]              how entries are written to blog.md
#   [paths]             where sessions are stored
#   [themes]            GTK theme for the popup

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

  # Keys for the review TUI ('snapshell inventory'). Each is a comma-separated
  # list of key names as the terminal reports them. Empty = default. ctrl+c
  # always quits, even while typing, and cannot be rebound.
  [keymaps.inventory]
  quit       = "q, ctrl+c"  # leave the review TUI
  up         = "up, k"      # previous card
  down       = "down, j"    # next card
  page_up    = "pgup"       # scroll the code/blog preview up
  page_down  = "pgdown"     # scroll the code/blog preview down
  append     = "a"          # commit the selected card with no caption
  caption    = "c"          # caption the selected card
  discard    = "d"          # discard the selected card
  note       = "n"          # write a standalone note
  blog       = "v"          # toggle the blog.md render view
  open       = "enter"      # view the selected screenshot
  submit     = "ctrl+s"     # save the caption/note being typed
  cancel     = "esc"        # back out of the current view
  confirm    = "y, Y"       # yes to the discard prompt
  decline    = "n, N"       # no to the discard prompt

[image]
  # Everything about how screenshots are shown in the review TUI and in the
  # "view blog" render.
  #
  # External viewer for peeking at a screenshot (Enter on an image card).
  # Empty = the system default (xdg-open). Set it to a viewer like "feh"
  # for a guaranteed auto-close.
  image_viewer = ""
  # Seconds an opened image stays up before the TUI best-effort closes it
  # (0 = 5). Auto-close may not fire for default viewers that hand the
  # image to an already-running instance.
  close_delay_secs = 5
  # How Enter on an image card shows the screenshot: "kitty" renders it
  # full-screen inside the terminal (requires running the TUI in kitty;
  # falls back to the external viewer otherwise), "external" opens it in
  # image_viewer.
  image_mode = "kitty"
  # Where the in-terminal screenshot is shown: "tab" opens it full-screen on
  # Enter (default), "inline" renders it right in the preview pane for the
  # selected image card — no Enter needed (Enter still zooms full-screen).
  image_render = "tab"
  # In-terminal image size, as a percentage of the size that would exactly
  # fit the pane. Tab mode: 100 = full fit when unset. Inline mode: unset =
  # 50% of the pane fit, and the value never exceeds 65%. The aspect ratio
  # is always preserved. Ignored in "external" mode.
  # image_scale_percent = 60
  # The "view blog" render (press the blog key inside the review TUI):
  #   - how large each screenshot is drawn, as % of the pane fit
  #     (100 = full fit when unset);
  #   - where it sits horizontally: "left" (default), "center", "right";
  #   - how many cells of space to keep from the pane edge when aligned
  #     "left" or "right" (so it isn't glued to the edge; 0 = flush).
  # blog_image_scale_percent = 100
  # blog_image_align = "left"
  # blog_image_padding = 2

[blog]
  # Where the caption of an image/code entry sits in blog.md relative to
  # the image/code block: "above" (default) or "below". Note entries have
  # no caption and ignore this.
  caption_position = "above"

[auto]
  # Auto mode: while an inventory session is active, every command that
  # exits 0 is queued as a pending code card automatically — no Alt+2
  # needed. The successful commands are waiting for you in the review TUI
  # ('snapshell inventory'), captions optional, when you're ready to write
  # them up. Command output is captured the same way Alt+2 would (from the
  # tmux pane, or the kitty scrollback when you're not in tmux), so long/
  # scrolled output comes along too.
  enabled = false
  # Commands that the auto path skips even when they exit 0. Each entry
  # matches either the full command line or its first word ("ls" also skips
  # "ls -la"). Excluded commands can still be captured manually with Alt+2.
  exclude = ["ls", "cd", "clear", "pwd", "exit", "echo"]

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
	if strings.TrimSpace(c.Keymaps.Inventory.Quit) == "" {
		c.Keymaps.Inventory.Quit = def.Keymaps.Inventory.Quit
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Up) == "" {
		c.Keymaps.Inventory.Up = def.Keymaps.Inventory.Up
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Down) == "" {
		c.Keymaps.Inventory.Down = def.Keymaps.Inventory.Down
	}
	if strings.TrimSpace(c.Keymaps.Inventory.PageUp) == "" {
		c.Keymaps.Inventory.PageUp = def.Keymaps.Inventory.PageUp
	}
	if strings.TrimSpace(c.Keymaps.Inventory.PageDown) == "" {
		c.Keymaps.Inventory.PageDown = def.Keymaps.Inventory.PageDown
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Append) == "" {
		c.Keymaps.Inventory.Append = def.Keymaps.Inventory.Append
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Caption) == "" {
		c.Keymaps.Inventory.Caption = def.Keymaps.Inventory.Caption
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Discard) == "" {
		c.Keymaps.Inventory.Discard = def.Keymaps.Inventory.Discard
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Note) == "" {
		c.Keymaps.Inventory.Note = def.Keymaps.Inventory.Note
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Blog) == "" {
		c.Keymaps.Inventory.Blog = def.Keymaps.Inventory.Blog
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Open) == "" {
		c.Keymaps.Inventory.Open = def.Keymaps.Inventory.Open
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Submit) == "" {
		c.Keymaps.Inventory.Submit = def.Keymaps.Inventory.Submit
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Cancel) == "" {
		c.Keymaps.Inventory.Cancel = def.Keymaps.Inventory.Cancel
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Confirm) == "" {
		c.Keymaps.Inventory.Confirm = def.Keymaps.Inventory.Confirm
	}
	if strings.TrimSpace(c.Keymaps.Inventory.Decline) == "" {
		c.Keymaps.Inventory.Decline = def.Keymaps.Inventory.Decline
	}
	if strings.TrimSpace(c.Paths.SessionRoot) == "" {
		c.Paths.SessionRoot = def.Paths.SessionRoot
	}
	if c.Image.CloseDelaySecs <= 0 {
		c.Image.CloseDelaySecs = def.Image.CloseDelaySecs
	}
	if strings.TrimSpace(c.Image.ImageMode) == "" {
		c.Image.ImageMode = def.Image.ImageMode
	}
	if strings.TrimSpace(c.Image.ImageRender) == "" {
		c.Image.ImageRender = def.Image.ImageRender
	}
	// A non-positive explicit image_scale_percent is treated as unset so the
	// per-mode defaults (tab 100, inline 50) apply.
	if c.Image.ImageScalePercent != nil && *c.Image.ImageScalePercent <= 0 {
		c.Image.ImageScalePercent = nil
	}
	// Same for blog_image_scale_percent: a non-positive explicit value means
	// "use the default" (100).
	if c.Image.BlogImageScalePercent != nil && *c.Image.BlogImageScalePercent <= 0 {
		c.Image.BlogImageScalePercent = nil
	}
	if strings.TrimSpace(c.Image.BlogImageAlign) == "" {
		c.Image.BlogImageAlign = def.Image.BlogImageAlign
	}
	// A negative explicit blog_image_padding is treated as unset (default 2).
	if c.Image.BlogImagePadding != nil && *c.Image.BlogImagePadding < 0 {
		c.Image.BlogImagePadding = nil
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
