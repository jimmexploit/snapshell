package daemon

import (
	"os"
	"testing"
)

// setUpPending redirects the pending file into a temp dir (the daemon
// StateDir is normally under the real home dir).
func setUpPending(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	os.MkdirAll(StateDir(), 0o700)
}

func TestPendingRoundTrip(t *testing.T) {
	setUpPending(t)
	want := PendingCapture{Mode: "code", File: "/tmp/x.txt", SessionDir: "/home/u/box"}
	if err := WritePending(want); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	got, ok, err := ReadPending()
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if !ok {
		t.Fatal("pending should be present")
	}
	if got != want {
		t.Fatalf("pending = %+v, want %+v", got, want)
	}
}

func TestPendingNoneIsNoop(t *testing.T) {
	setUpPending(t)
	if _, ok, err := ReadPending(); err != nil {
		t.Fatalf("ReadPending: %v", err)
	} else if ok {
		t.Fatal("no pending should report ok=false")
	}
	ClearPending() // must not error on a missing file
}

func TestPendingOverwriteAndClear(t *testing.T) {
	setUpPending(t)
	if err := WritePending(PendingCapture{Mode: "image", File: "attachments/001.png", SessionDir: "/s"}); err != nil {
		t.Fatal(err)
	}
	// A newer capture replaces the older one (last one wins).
	if err := WritePending(PendingCapture{Mode: "note", SessionDir: "/s"}); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := ReadPending()
	if !ok || got.Mode != "note" {
		t.Fatalf("newest pending should win, got %+v ok=%v", got, ok)
	}
	ClearPending()
	if _, ok, _ := ReadPending(); ok {
		t.Fatal("pending should be cleared")
	}
}
