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

// combos maps the user-facing name of each hotkey to its X modifier/key
// string. Alt is Mod1 on virtually all layouts.
var combos = map[string]string{
	"screenshot": "Mod1-1", // Alt+1
	"code":       "Mod1-2", // Alt+2
	"note":       "Mod1-3", // Alt+3
}

// GrabAll registers the three global hotkeys and returns an unregister
// function that releases them and stops the event loop. If some grabs fail
// (e.g. another application already owns the key), the working ones still
// register and the returned error names the failed keys.
func GrabAll(alt1, alt2, alt3 Handler) (unregister func(), err error) {
	return grabAll(alt1, alt2, alt3, defaultDebounce)
}

const defaultDebounce = 300 * time.Millisecond

func grabAll(alt1, alt2, alt3 Handler, debounce time.Duration) (func(), error) {
	X, err := xgbutil.NewConn()
	if err != nil {
		return nil, fmt.Errorf("connect to X11: %w", err)
	}

	keybind.Initialize(X)
	root := X.RootWin()

	handlers := map[string]Handler{
		"screenshot": alt1,
		"code":       alt2,
		"note":       alt3,
	}

	// keycode -> name of the combo it belongs to. A keysym can map to
	// multiple keycodes (numpad vs top row), so every keycode gets grabbed.
	byKeycode := map[xproto.Keycode]string{}
	var failed []string

	for name, combo := range combos {
		mods, keycodes, err := keybind.ParseString(X, combo)
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
		}
	}

	disp := newDispatcher(debounce, handlers)

	xevent.KeyPressFun(func(X *xgbutil.XUtil, e xevent.KeyPressEvent) {
		name, ok := byKeycode[e.Detail]
		if !ok {
			return
		}
		// Confirm the combo's primary modifier is actually held; the grab
		// already enforces this, this is a defensive double-check.
		if e.State&xproto.ModMask1 == 0 {
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
			mods, keycodes, err := keybind.ParseString(X, combo)
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
