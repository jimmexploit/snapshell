package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"snapshell/internal/config"
)

// gtkThemeDir reports whether dir contains a GTK theme, i.e. at least one
// gtk-* subdirectory (gtk-2.0, gtk-3.0, gtk-3.20, gtk-4.0, ...). Theme roots
// also hold window-decoration-only themes (metacity-1, xfwm4, ...) which
// GTK_THEME cannot apply to a GTK dialog, so those are excluded.
func gtkThemeDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "gtk-") {
			return true
		}
	}
	return false
}

// collectThemes scans every root directory and returns the GTK theme names
// found, deduplicated (a theme in the user's ~/.themes shadows the
// system copy) and sorted. Missing root directories are skipped.
func collectThemes(roots []string) ([]string, error) {
	seen := map[string]bool{}
	var themes []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("scan themes in %s: %v", root, err)
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || seen[name] || !gtkThemeDir(filepath.Join(root, name)) {
				continue
			}
			seen[name] = true
			themes = append(themes, name)
		}
	}
	sort.Strings(themes)
	return themes, nil
}

// listThemes returns every GTK theme installed on the system: the standard
// locations plus any custom root configured in [themes].root.
func listThemes() ([]string, error) {
	cfg, err := config.Load()
	if err != nil {
		// A broken/missing config must not kill the listing — fall back to
		// defaults (no custom root).
		cfg = config.Default()
	}
	return collectThemes(cfg.ThemeSearchDirs())
}

// newListThemesCmd prints every GTK theme installed on the system so the
// user can pick a value for [themes].name.
func newListThemesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-themes",
		Short: "List every GTK theme installed on the system (for themes.name)",
		Long: "List the GTK theme names you can put in [themes].name — " +
			"the popup window is spawned with that theme via GTK_THEME. " +
			"Scans /usr/share/themes, /usr/local/share/themes, ~/.themes, " +
			"~/.local/share/themes and any custom [themes].root. Only real " +
			"GTK themes (those with a gtk-* subdirectory) are listed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			themes, err := listThemes()
			if err != nil {
				return err
			}
			for _, t := range themes {
				fmt.Println(t)
			}
			return nil
		},
	}
}
