package hotkeys

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatcherCallsHandler(t *testing.T) {
	var calls atomic.Int32
	d := newDispatcher(time.Hour, map[string]Handler{
		"screenshot": func() { calls.Add(1) },
	})

	if !d.fire("screenshot", time.Now()) {
		t.Fatal("first press should fire")
	}
	waitFor(t, func() bool { return calls.Load() == 1 })
}

func TestDispatcherDebouncesRapidRepress(t *testing.T) {
	var calls atomic.Int32
	d := newDispatcher(300*time.Millisecond, map[string]Handler{
		"screenshot": func() { calls.Add(1) },
	})

	start := time.Now()
	d.fire("screenshot", start)
	d.fire("screenshot", start.Add(50*time.Millisecond))
	d.fire("screenshot", start.Add(150*time.Millisecond))
	waitFor(t, func() bool { return calls.Load() == 1 })

	// After the window passes, a press fires again.
	d.fire("screenshot", start.Add(400*time.Millisecond))
	waitFor(t, func() bool { return calls.Load() == 2 })
}

func TestDispatcherUnknownKey(t *testing.T) {
	var calls atomic.Int32
	d := newDispatcher(300*time.Millisecond, map[string]Handler{
		"screenshot": func() { calls.Add(1) },
	})
	if d.fire("nonexistent", time.Now()) {
		t.Fatal("unknown key should not fire")
	}
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("handler should never run, got %d calls", calls.Load())
	}
}

func TestDispatcherSeparateKeysAreIndependent(t *testing.T) {
	var shot, code atomic.Int32
	d := newDispatcher(300*time.Millisecond, map[string]Handler{
		"screenshot": func() { shot.Add(1) },
		"code":       func() { code.Add(1) },
	})

	now := time.Now()
	d.fire("screenshot", now)
	d.fire("code", now.Add(20*time.Millisecond)) // within window but different key
	waitFor(t, func() bool { return shot.Load() == 1 && code.Load() == 1 })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
