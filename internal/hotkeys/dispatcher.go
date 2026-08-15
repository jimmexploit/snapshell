package hotkeys

import (
	"sync"
	"time"
)

// dispatcher owns the debounce logic: it decides whether a given key press
// should actually invoke its handler, ignoring repeat firings within the
// debounce window (guards against X11 key-repeat when the combo is held).
// It is deliberately independent of xgbutil so it can be unit tested.
type dispatcher struct {
	mu       sync.Mutex
	handlers map[string]Handler
	lastFire map[string]time.Time
	debounce time.Duration
}

func newDispatcher(debounce time.Duration, handlers map[string]Handler) *dispatcher {
	return &dispatcher{
		handlers: handlers,
		lastFire: make(map[string]time.Time),
		debounce: debounce,
	}
}

// fire invokes the handler for name if one is registered and the previous
// firing for the same name is older than the debounce window. Returns true
// if the handler ran.
func (d *dispatcher) fire(name string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.handlers[name]; !ok {
		return false
	}
	if prev, ok := d.lastFire[name]; ok && now.Sub(prev) < d.debounce {
		return false
	}
	d.lastFire[name] = now

	// Run in a goroutine so a slow handler never blocks the event loop or
	// subsequent hotkey dispatches.
	go d.handlers[name]()
	return true
}
