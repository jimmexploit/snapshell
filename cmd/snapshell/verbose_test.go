package main

import (
	"strings"
	"testing"
)

func TestColorize(t *testing.T) {
	cases := []struct {
		in, want, notWant string
	}{
		{"2026/08/15 03:00:00 hotkey: screenshot", colYellow, colCyan},
		{"2026/08/15 03:00:00 capture screenshot saved: attachments/001.png", colYellow, colRed},
		{"2026/08/15 03:00:00 hotkey: code", colCyan, colYellow},
		{"2026/08/15 03:00:00 capture tmux: marker for pane %0 is incomplete", colCyan, colRed},
		{"2026/08/15 03:00:00 hotkey: note", colMagenta, colCyan},
		{"2026/08/15 03:00:00 session started: box (dir=/home/u/snapshell/box)", colGreen, colRed},
		{"2026/08/15 03:00:00 capture screenshot: flameshot not found on PATH", colRed, colYellow},
		{"2026/08/15 03:00:00 request: status map[]", colDim, colGreen},
	}
	for _, tc := range cases {
		got := colorize(tc.in)
		if !strings.Contains(got, tc.want) {
			t.Errorf("colorize(%q) missing %q, got %q", tc.in, tc.want, got)
		}
		if tc.notWant != "" && strings.Contains(got, tc.notWant) {
			t.Errorf("colorize(%q) unexpectedly has %q, got %q", tc.in, tc.notWant, got)
		}
	}
}
