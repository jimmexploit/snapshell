package hotkeys

import (
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
)

func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		in    string
		want  string
		state uint16
	}{
		{"Alt+1", "mod1-1", xproto.ModMask1},
		{"Alt+2", "mod1-2", xproto.ModMask1},
		{"Alt+3", "mod1-3", xproto.ModMask1},
		{"Ctrl+Shift+F5", "control-shift-F5", xproto.ModMaskControl | xproto.ModMaskShift},
		{"Ctrl+Alt+Return", "control-mod1-Return", xproto.ModMaskControl | xproto.ModMask1},
		{"Super+f", "mod4-f", xproto.ModMask4},
		{"Mod4+space", "mod4-space", xproto.ModMask4},
	} {
		got, state, err := Normalize(tc.in)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", tc.in, err)
		}
		if got != tc.want || state != tc.state {
			t.Fatalf("Normalize(%q) = (%q, %#x), want (%q, %#x)", tc.in, got, state, tc.want, tc.state)
		}
	}
}

func TestNormalizeCaseInsensitive(t *testing.T) {
	got, state, err := Normalize("alt+shift+f2")
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got != "mod1-shift-f2" || state != (xproto.ModMask1|xproto.ModMaskShift) {
		t.Fatalf("got (%q, %#x)", got, state)
	}
}

func TestNormalizeErrors(t *testing.T) {
	for _, in := range []string{"", "1", "Alt+", "Hyper+1", "++1"} {
		if _, _, err := Normalize(in); err == nil {
			t.Fatalf("Normalize(%q) should error", in)
		}
	}
}

func TestNormalizeKeysymsPassThrough(t *testing.T) {
	for _, key := range []string{"a", "F12", "Return", "space", "1"} {
		got, _, err := Normalize("Alt+" + key)
		if err != nil {
			t.Fatalf("Normalize(Alt+%s): %v", key, err)
		}
		if !strings.HasSuffix(got, "-"+key) {
			t.Fatalf("Normalize(Alt+%s) = %q, keysym lost", key, got)
		}
	}
}
