package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// makeTheme creates a theme dir under root with the given gtk subdirs
// (empty = a decoration-only theme).
func makeTheme(t *testing.T, root, name string, gtkDirs ...string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, g := range gtkDirs {
		if err := os.MkdirAll(filepath.Join(dir, g), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGtkThemeDir(t *testing.T) {
	root := t.TempDir()
	makeTheme(t, root, "Sweet", "gtk-2.0", "gtk-3.0", "gtk-4.0")
	makeTheme(t, root, "JustDecorations", "metacity-1", "xfwm4")

	if !gtkThemeDir(filepath.Join(root, "Sweet")) {
		t.Fatal("Sweet should be recognized as a GTK theme")
	}
	if gtkThemeDir(filepath.Join(root, "JustDecorations")) {
		t.Fatal("decoration-only theme must not be listed")
	}
	if gtkThemeDir(filepath.Join(root, "DoesNotExist")) {
		t.Fatal("missing dir must not be a theme")
	}
}

func TestCollectThemesScansAllRootsDedupesSorts(t *testing.T) {
	sys := t.TempDir()
	user := t.TempDir()
	extra := t.TempDir()
	makeTheme(t, sys, "Adwaita", "gtk-3.0")
	makeTheme(t, sys, "Sweet", "gtk-3.0", "gtk-4.0")
	makeTheme(t, sys, "NotAGtk", "metacity-1")
	// A user copy shadows the system copy of Adwaita.
	makeTheme(t, user, "Adwaita", "gtk-3.0")
	makeTheme(t, extra, "CustomThing", "gtk-2.0")

	got, err := collectThemes([]string{sys, user, extra})
	if err != nil {
		t.Fatalf("collectThemes: %v", err)
	}
	want := []string{"Adwaita", "CustomThing", "Sweet"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCollectThemesSkipsMissingRoots(t *testing.T) {
	themes, err := collectThemes([]string{filepath.Join(t.TempDir(), "nope"), t.TempDir()})
	if err != nil {
		t.Fatalf("collectThemes: %v", err)
	}
	if len(themes) != 0 {
		t.Fatalf("got %q, want empty", themes)
	}
}
