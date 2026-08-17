package popup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePosition(t *testing.T) {
	for _, preset := range presets {
		if _, err := parsePosition(preset); err != nil {
			t.Errorf("parsePosition(%q): %v", preset, err)
		}
	}
	pos, err := parsePosition("120,80")
	if err != nil {
		t.Fatalf("parsePosition(120,80): %v", err)
	}
	if pos.preset != "" || pos.x != 120 || pos.y != 80 {
		t.Fatalf("pos = %+v, want pixels 120,80", pos)
	}
	for _, bad := range []string{"", "   ", "middle", "120", "120,-5", "abc,80", "1,2,3"} {
		if _, err := parsePosition(bad); err == nil {
			t.Errorf("parsePosition(%q) should error", bad)
		}
	}
}

func TestPositionResolvePresets(t *testing.T) {
	// Screen 1920x1080, dialog 560x320.
	sw, sh, ww, wh := 1920, 1080, 560, 320
	cases := map[string][2]int{
		"top-left":      {0, 0},
		"top-center":    {(sw - ww) / 2, 0},
		"top-right":     {sw - ww, 0},
		"center-left":   {0, (sh - wh) / 2},
		"center":        {(sw - ww) / 2, (sh - wh) / 2},
		"center-right":  {sw - ww, (sh - wh) / 2},
		"bottom-left":   {0, sh - wh},
		"bottom-center": {(sw - ww) / 2, sh - wh},
		"bottom-right":  {sw - ww, sh - wh},
	}
	for preset, want := range cases {
		pos, err := parsePosition(preset)
		if err != nil {
			t.Fatal(err)
		}
		x, y := pos.resolve(sw, sh, ww, wh)
		if x != want[0] || y != want[1] {
			t.Errorf("%s → (%d,%d), want (%d,%d)", preset, x, y, want[0], want[1])
		}
	}
}

func TestPositionResolvePixelsPassthrough(t *testing.T) {
	pos, _ := parsePosition("100,50")
	x, y := pos.resolve(1920, 1080, 560, 320)
	if x != 100 || y != 50 {
		t.Fatalf("pixels must pass through, got (%d,%d)", x, y)
	}
}

func TestPositionResolveClampsNegative(t *testing.T) {
	// A dialog bigger than the screen must not land off-screen.
	pos, _ := parsePosition("bottom-right")
	x, y := pos.resolve(400, 300, 560, 320)
	if x < 0 || y < 0 {
		t.Fatalf("clamped position must be non-negative, got (%d,%d)", x, y)
	}
}

// fakeXdotool installs an xdotool shim that answers getdisplaygeometry,
// search and getwindowgeometry, and records every windowmove invocation to
// recordPath. After a windowmove the shim reports the moved position on
// subsequent getwindowgeometry calls — matching real xdotool — so the
// settle loop converges after one move. Setting overwrite=true makes the
// first move appear to be undone by zenity (reported back at 0,0), forcing
// the settle loop to move a second time.
func fakeXdotool(t *testing.T, binDir, recordPath string, overwrite bool) {
	t.Helper()
	state := filepath.Join(binDir, "geom.state")
	mark := filepath.Join(binDir, "overwrite.mark")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = getdisplaygeometry ]; then echo '1920 1080'; exit 0; fi\n" +
		"if [ \"$1\" = search ]; then echo '50001'; exit 0; fi\n" +
		"if [ \"$1\" = windowmove ]; then\n" +
		"  echo \"$@\" >> " + recordPath + "\n" +
		"  if [ \"" + boolStr(overwrite) + "\" = 1 ] && [ ! -f " + mark + " ]; then\n" +
		"    touch " + mark + "\n" +
		"    echo '0 0' > " + state + "\n" +
		"  else\n" +
		"    echo \"$3 $4\" > " + state + "\n" +
		"  fi\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = getwindowgeometry ]; then\n" +
		"  pos=$(cat " + state + " 2>/dev/null || echo '0 0')\n" +
		"  echo \"Position: $(echo $pos | tr ' ' ',') (screen: 0)\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "xdotool"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func readMoves(t *testing.T, recordPath string) []string {
	t.Helper()
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var moves []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			moves = append(moves, line)
		}
	}
	return moves
}

func TestMoveDialogCentersWindow(t *testing.T) {
	record := filepath.Join(t.TempDir(), "moves")
	fakeXdotool(t, t.TempDir(), record, false)
	if err := moveDialog("snapshell - command", "center", 560, 320); err != nil {
		t.Fatalf("moveDialog: %v", err)
	}
	// 1920x1080 screen, 560x320 dialog → center (680, 380).
	moves := readMoves(t, record)
	if len(moves) != 1 || moves[0] != "windowmove 50001 680 380" {
		t.Fatalf("moves = %q, want one move to center", moves)
	}
}

func TestMoveDialogPixels(t *testing.T) {
	record := filepath.Join(t.TempDir(), "moves")
	fakeXdotool(t, t.TempDir(), record, false)
	if err := moveDialog("snapshell - note", "120,80", 560, 320); err != nil {
		t.Fatalf("moveDialog: %v", err)
	}
	moves := readMoves(t, record)
	if len(moves) != 1 || moves[0] != "windowmove 50001 120 80" {
		t.Fatalf("moves = %q, want one move to (120,80)", moves)
	}
}

func TestMoveDialogRetriesWhenZenityReplaces(t *testing.T) {
	record := filepath.Join(t.TempDir(), "moves")
	fakeXdotool(t, t.TempDir(), record, true)
	if err := moveDialog("snapshell - screenshot", "center", 560, 320); err != nil {
		t.Fatalf("moveDialog: %v", err)
	}
	moves := readMoves(t, record)
	if len(moves) != 2 || moves[1] != "windowmove 50001 680 380" {
		t.Fatalf("moves = %q, want a second move that sticks at center", moves)
	}
}

func TestAskDialogPositionWithoutXdotoolErrors(t *testing.T) {
	// zenity present, xdotool absent → the configured position must fail
	// loudly before the dialog opens, naming the missing binary.
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "zenity"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	_, err := askDialog(ModeNote, "", "", "", 560, 320, "", "center", "")
	if err == nil || !strings.Contains(err.Error(), "xdotool not found on PATH") {
		t.Fatalf("err = %v, want xdotool-not-found error", err)
	}
}

func TestAskDialogInvalidPositionErrors(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "zenity"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// xdotool present (shim), position syntactically invalid → error names
	// the valid options.
	xdotoolShim := filepath.Join(binDir, "xdotool")
	if err := os.WriteFile(xdotoolShim, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	_, err := askDialog(ModeNote, "", "", "", 560, 320, "", "not-a-place", "")
	if err == nil || !strings.Contains(err.Error(), "invalid popup position") {
		t.Fatalf("err = %v, want invalid-position error", err)
	}
}
