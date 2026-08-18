package hotkeys

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/keybind"
)

// WaitForDigit grabs the number keys 1-9 (no modifier) for up to window
// and returns the first one pressed, or 0 when none is pressed in time. It
// backs the Alt+2 command-count prefix: pressing a digit right after Alt+2
// captures that many recent commands instead of just the last one.
//
// A short-lived X connection of its own is used, so the main hotkey event
// loop is never blocked while waiting. Closing that connection (which
// happens on every return) releases the grabs and stops the poll loop, so
// the digit keys are swallowed for the shortest possible time. Grabbing a
// single digit that another application owns is best-effort: that digit
// just isn't available as a count, the rest still are.
func WaitForDigit(window time.Duration) (int, error) {
	X, err := xgbutil.NewConn()
	if err != nil {
		return 0, fmt.Errorf("connect to X11 for command count: %w", err)
	}
	defer X.Conn().Close()

	keybind.Initialize(X)
	root := X.RootWin()

	// keycode -> digit it produces. Grabbed with no modifiers: a plain "2"
	// fires the count (and is swallowed for the focused window), while
	// shifted/modified presses (Shift+2 → '@', NumLock+numpad, ...) pass
	// through to whatever the user is actually typing.
	byKeycode := map[xproto.Keycode]int{}
	var failed []string
	for digit := 1; digit <= 9; digit++ {
		mods, keycodes, err := keybind.ParseString(X, strconv.Itoa(digit))
		if err != nil {
			failed = append(failed, strconv.Itoa(digit))
			continue
		}
		for _, kc := range keycodes {
			if err := keybind.GrabChecked(X, root, mods, kc); err != nil {
				failed = append(failed, strconv.Itoa(digit))
				break
			}
			byKeycode[kc] = digit
		}
	}
	if len(byKeycode) == 0 {
		return 0, fmt.Errorf("could not grab command-count digits 1-9: %s", strings.Join(failed, ", "))
	}

	// Own event loop. xevent.Main can't be used here: it blocks in
	// WaitForEvent, so a Quit call is only noticed after an event arrives —
	// the no-digit (timeout) path would deadlock. Polling the raw
	// connection and selecting on `stop` always returns, and the 20ms poll
	// latency is negligible against a ~1.5s wait window.
	got := make(chan int, 1)
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		conn := X.Conn()
		for {
			select {
			case <-stop:
				return
			default:
			}
			ev, err := conn.PollForEvent()
			if err != nil {
				return
			}
			if ev == nil {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if kp, ok := ev.(xproto.KeyPressEvent); ok {
				if digit, ok := byKeycode[kp.Detail]; ok {
					select {
					case got <- digit:
					default:
					}
				}
			}
		}
	}()

	digit := 0
	select {
	case digit = <-got:
	case <-time.After(window):
	}
	close(stop)
	<-stopped
	return digit, nil
}
