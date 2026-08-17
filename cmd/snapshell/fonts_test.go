package main

import (
	"reflect"
	"testing"
)

func TestParseFamiliesDedupesSorts(t *testing.T) {
	out := "Cascadia Code PL\n" +
		"Noto Sans, Noto Sans CJK SC\n" +
		"Cantarell\n" +
		"Noto Sans\n" // duplicate, also appears inside a multi-family line
	got := parseFamilies(out)

	want := []string{
		"Cantarell",
		"Cascadia Code PL",
		"Monospace", // generic, added because no concrete font provides it
		"Noto Sans",
		"Noto Sans CJK SC",
		"Sans",  // generic
		"Serif", // generic
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseFamiliesGenericsDedupeAgainstRealFonts(t *testing.T) {
	// A concrete font named exactly "Sans" (fc-list can report the generic
	// aliases) must not be duplicated by the generic prepend.
	out := "Sans\nSome Other Font\n"
	got := parseFamilies(out)
	want := []string{"Monospace", "Sans", "Serif", "Some Other Font"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseFamiliesEmpty(t *testing.T) {
	got := parseFamilies("")
	want := []string{"Monospace", "Sans", "Serif"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
