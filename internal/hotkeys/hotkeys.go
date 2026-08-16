package hotkeys

import (
	"fmt"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
	"github.com/jezek/xgbutil/xevent"
)

// Handler is invoked when a hotkey fires. It is called from the X event
// loop goroutine; implementations that do real work should spawn their own
// goroutines and return quickly.
type Handler func()

// GrabAll registers one global X11 hotkey per entry in combos (keyed by a
// user-facing name, valued by a combo string like "Alt+1") and returns an
// unregister function that releases them and stops the event loop. If some
// grabs fail (e.g. another application already owns the key), the working
// ones still register and the returned error names the failed keys.
func GrabAll(combos map[string]string, handlers map[string]Handler) (unregister func(), err error) {
	return grabAll(combos, handlers, defaultDebounce)
}

const defaultDebounce = 300 * time.Millisecond

func grabAll(combos map[string]string, handlers map[string]Handler, debounce time.Duration) (func(), error) {
	X, err := xgbutil.NewConn()
	if err != nil {
		return nil, fmt.Errorf("connect to X11: %w", err)
	}

	keybind.Initialize(X)
	root := X.RootWin()

	// keycode -> name of the combo it belongs to. A keysym can map to
	// multiple keycodes (numpad vs top row), so every keycode gets grabbed.
	// The state bits are the combo's required modifiers, verified on each
	// press (defensive double-check on top of the grab itself).
	byKeycode := map[xproto.Keycode]string{}
	byKeycodeState := map[xproto.Keycode]uint16{}
	var failed []string

	for name, combo := range combos {
		norm, state, err := Normalize(combo)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s (%s: %v)", name, combo, err))
			continue
		}
		mods, keycodes, err := keybind.ParseString(X, norm)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s (%s: %v)", name, combo, err))
			continue
		}
		for _, kc := range keycodes {
			if err := keybind.GrabChecked(X, root, mods, kc); err != nil {
				failed = append(failed, fmt.Sprintf("%s (%s)", name, combo))
				break
			}
			byKeycode[kc] = name
			byKeycodeState[kc] = state
		}
	}

	disp := newDispatcher(debounce, handlers)

	xevent.KeyPressFun(func(X *xgbutil.XUtil, e xevent.KeyPressEvent) {
		name, ok := byKeycode[e.Detail]
		if !ok {
			return
		}
		// Confirm the combo's modifiers are actually held; the grab already
		// enforces this, this is a defensive double-check.
		if state := byKeycodeState[e.Detail]; e.State&state != state {
			return
		}
		disp.fire(name, time.Now())
	}).Connect(X, root)

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		xevent.Main(X)
	}()

	unregister := func() {
		xevent.Quit(X)
		<-stopped
		for _, combo := range combos {
			norm, _, err := Normalize(combo)
			if err != nil {
				continue
			}
			mods, keycodes, err := keybind.ParseString(X, norm)
			if err != nil {
				continue
			}
			for _, kc := range keycodes {
				keybind.Ungrab(X, root, mods, kc)
			}
		}
		X.Conn().Close()
	}

	if len(failed) > 0 {
		return unregister, fmt.Errorf("could not grab: %s", strings.Join(failed, "; "))
	}
	return unregister, nil
}

// modInfo maps a friendly modifier name to the xgbutil ParseString token
// and its xproto modifier bit. Alt is Mod1 on virtually all layouts, Super
// (the "Win" key) is Mod4 on most.
func modInfo(name string) (token string, bit uint16, ok bool) {
	switch strings.ToLower(name) {
	case "alt", "meta":
		return "mod1", xproto.ModMask1, true
	case "ctrl", "control":
		return "control", xproto.ModMaskControl, true
	case "shift":
		return "shift", xproto.ModMaskShift, true
	case "super", "win", "mod4":
		return "mod4", xproto.ModMask4, true
	case "mod1":
		return "mod1", xproto.ModMask1, true
	case "mod2":
		return "mod2", xproto.ModMask2, true
	case "mod3":
		return "mod3", xproto.ModMask3, true
	case "mod5":
		return "mod5", xproto.ModMask5, true
	case "lock":
		return "lock", xproto.ModMaskLock, true
	default:
		return "", 0, false
	}
}

// Normalize converts a user-friendly combo string ("Alt+Shift+1") into the
// format keybind.ParseString expects ("Mod1-Shift-1") plus the xproto
// modifier bits that must be held when the key fires. It is pure (no X11
// connection needed) so the combo parsing is unit-testable.
func Normalize(combo string) (normalized string, state uint16, err error) {
	parts := strings.Split(strings.TrimSpace(combo), "+")
	if len(parts) < 2 {
		return "", 0, fmt.Errorf("bad combo %q: expected MODIFIER+...KEY", combo)
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	if key == "" {
		return "", 0, fmt.Errorf("bad combo %q: missing key", combo)
	}

	var tokens []string
	for _, m := range parts[:len(parts)-1] {
		token, bit, ok := modInfo(strings.TrimSpace(m))
		if !ok {
			return "", 0, fmt.Errorf("unknown modifier %q", m)
		}
		tokens = append(tokens, token)
		state |= bit
	}

	if len(tokens) == 0 {
		return "", 0, fmt.Errorf("bad combo %q: at least one modifier required", combo)
	}
	normalized = strings.Join(tokens, "-") + "-" + key
	return normalized, state, nil
}
