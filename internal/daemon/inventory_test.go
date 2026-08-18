package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"snapshell/internal/inventory"
)

// precreateInventorySession writes a session folder on disk exactly as a
// previously-running inventory session would leave it: the mode marker plus
// a pending.json holding the given cards. start then resumes it.
func precreateInventorySession(t *testing.T, sessionRoot, name string, cards []inventory.Card) string {
	t.Helper()
	dir := filepath.Join(sessionRoot, name)
	if err := os.MkdirAll(filepath.Join(dir, "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".snapshell-mode"), []byte("inventory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nextID := 1
	for _, c := range cards {
		if c.ID >= nextID {
			nextID = c.ID + 1
		}
	}
	data, err := json.MarshalIndent(map[string]any{"next_id": nextID, "cards": cards}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pending.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func codeCard(id int, text string) inventory.Card {
	return inventory.Card{ID: id, Kind: inventory.KindCode, Text: text, Created: time.Now()}
}

func TestInventoryStartListCommit(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	dir := precreateInventorySession(t, sessionRoot, "acme", []inventory.Card{codeCard(1, "whoami"), codeCard(2, "nmap -sV")})
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)

	// Bare `start` on an inventory session must be refused — no silent
	// downgrade.
	resp := send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme"}})
	if resp.OK || !strings.Contains(resp.Error, "inventory mode") {
		t.Fatalf("bare start on inventory session should fail, got %+v", resp)
	}

	// `start inventory acme` resumes it.
	resp = send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme", "mode": "inventory"}})
	if !resp.OK {
		t.Fatalf("start inventory should succeed, got %+v", resp)
	}

	// list returns the persisted cards, oldest-first.
	resp = send(t, sockPath, Request{Cmd: CmdList})
	if !resp.OK {
		t.Fatalf("list should succeed, got %+v", resp)
	}
	var list ListData
	if err := json.Unmarshal(resp.Data, &list); err != nil {
		t.Fatalf("unmarshal list data: %v", err)
	}
	if len(list.Cards) != 2 || list.Cards[0].Text != "whoami" || list.Cards[1].Text != "nmap -sV" {
		t.Fatalf("list cards = %+v", list.Cards)
	}

	// Commit the first card with a caption → blog.md entry, card removed.
	resp = send(t, sockPath, Request{Cmd: CmdCommit, Args: map[string]string{"id": "1", "caption": "initial recon"}})
	if !resp.OK {
		t.Fatalf("commit should succeed, got %+v", resp)
	}
	blogData, err := os.ReadFile(filepath.Join(dir, "blog.md"))
	if err != nil {
		t.Fatalf("read blog.md: %v", err)
	}
	if !strings.Contains(string(blogData), "initial recon") || !strings.Contains(string(blogData), "whoami") {
		t.Fatalf("blog.md missing committed card:\n%s", blogData)
	}
	resp = send(t, sockPath, Request{Cmd: CmdList})
	if !resp.OK {
		t.Fatalf("list after commit failed, got %+v", resp)
	}
	json.Unmarshal(resp.Data, &list)
	if len(list.Cards) != 1 || list.Cards[0].Text != "nmap -sV" {
		t.Fatalf("after commit cards = %+v, want only nmap", list.Cards)
	}

	// Status reports inventory mode + pending count.
	resp = send(t, sockPath, Request{Cmd: CmdStatus})
	if !resp.OK || !strings.Contains(resp.Message, "inventory mode") || !strings.Contains(resp.Message, "1 pending") {
		t.Fatalf("status should mention inventory mode + pending, got %+v", resp)
	}
	var st StatusData
	if err := json.Unmarshal(resp.Data, &st); err != nil {
		t.Fatalf("unmarshal status data: %v", err)
	}
	if st.Mode != "inventory" || st.Session != "acme" || st.Pending != 1 || st.Entries != 1 {
		t.Fatalf("status data = %+v", st)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

func TestInventoryDiscardRequiresConfirm(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	precreateInventorySession(t, sessionRoot, "acme", []inventory.Card{codeCard(1, "bad command")})
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)
	send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme", "mode": "inventory"}})

	// Without the confirm flag the daemon refuses outright.
	resp := send(t, sockPath, Request{Cmd: CmdDiscard, Args: map[string]string{"id": "1"}})
	if resp.OK || !strings.Contains(resp.Error, "confirm") {
		t.Fatalf("discard without confirm should fail, got %+v", resp)
	}
	// Card still there.
	resp = send(t, sockPath, Request{Cmd: CmdList})
	var list ListData
	json.Unmarshal(resp.Data, &list)
	if len(list.Cards) != 1 {
		t.Fatalf("card should survive a refused discard, got %+v", list.Cards)
	}

	// With confirm the card goes.
	resp = send(t, sockPath, Request{Cmd: CmdDiscard, Args: map[string]string{"id": "1", "confirm": "true"}})
	if !resp.OK {
		t.Fatalf("discard with confirm should succeed, got %+v", resp)
	}
	resp = send(t, sockPath, Request{Cmd: CmdList})
	json.Unmarshal(resp.Data, &list)
	if len(list.Cards) != 0 {
		t.Fatalf("queue should be empty after discard, got %+v", list.Cards)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

func TestInventoryDiscardDeletesImageFile(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	dir := precreateInventorySession(t, sessionRoot, "acme", []inventory.Card{
		{ID: 1, Kind: inventory.KindImage, Path: "attachments/001.png", Created: time.Now()},
	})
	img := filepath.Join(dir, "attachments", "001.png")
	if err := os.WriteFile(img, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)
	send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme", "mode": "inventory"}})

	resp := send(t, sockPath, Request{Cmd: CmdDiscard, Args: map[string]string{"id": "1", "confirm": "true"}})
	if !resp.OK {
		t.Fatalf("discard should succeed, got %+v", resp)
	}
	if _, err := os.Stat(img); !os.IsNotExist(err) {
		t.Fatalf("discarded screenshot file should be deleted, stat err=%v", err)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

func TestInventoryCommitImageAppendsRelativePath(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	dir := precreateInventorySession(t, sessionRoot, "acme", []inventory.Card{
		{ID: 1, Kind: inventory.KindImage, Path: "attachments/001.png", Created: time.Now()},
	})
	if err := os.WriteFile(filepath.Join(dir, "attachments", "001.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)
	send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme", "mode": "inventory"}})

	resp := send(t, sockPath, Request{Cmd: CmdCommit, Args: map[string]string{"id": "1", "caption": "foothold"}})
	if !resp.OK {
		t.Fatalf("commit image should succeed, got %+v", resp)
	}
	blogData, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	if !strings.Contains(string(blogData), "![](attachments/001.png)") {
		t.Fatalf("blog.md must use the relative image path:\n%s", blogData)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

func TestInventoryNoteAndNoSessionErrors(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)

	// list/commit with no session → clear errors, not a panic.
	for _, cmd := range []string{CmdList, CmdCommit, CmdDiscard, CmdNote} {
		resp := send(t, sockPath, Request{Cmd: cmd, Args: map[string]string{"id": "1", "confirm": "true", "text": "x"}})
		if resp.OK || !strings.Contains(resp.Error, "no active session") {
			t.Fatalf("%s with no session: got %+v, want 'no active session'", cmd, resp)
		}
	}

	// Start a normal session: inventory verbs refuse, note works.
	resp := send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme"}})
	if !resp.OK {
		t.Fatalf("start should succeed, got %+v", resp)
	}
	resp = send(t, sockPath, Request{Cmd: CmdList})
	if resp.OK || !strings.Contains(resp.Error, "not in inventory mode") {
		t.Fatalf("list on normal session: got %+v", resp)
	}
	resp = send(t, sockPath, Request{Cmd: CmdCommit, Args: map[string]string{"id": "1"}})
	if resp.OK || !strings.Contains(resp.Error, "not in inventory mode") {
		t.Fatalf("commit on normal session: got %+v", resp)
	}
	resp = send(t, sockPath, Request{Cmd: CmdNote, Args: map[string]string{"text": "plain note"}})
	if !resp.OK {
		t.Fatalf("note on normal session should succeed, got %+v", resp)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

func TestStartModeConflict(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)

	// Normal session first.
	resp := send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme"}})
	if !resp.OK {
		t.Fatalf("start normal should succeed, got %+v", resp)
	}
	send(t, sockPath, Request{Cmd: CmdStop})

	// Upgrading a normal session to inventory must fail.
	resp = send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme", "mode": "inventory"}})
	if resp.OK || !strings.Contains(resp.Error, "normal mode") {
		t.Fatalf("upgrade should fail, got %+v", resp)
	}
	// And it must not have written an inventory marker.
	if data, err := os.ReadFile(filepath.Join(sessionRoot, "acme", ".snapshell-mode")); err != nil || strings.TrimSpace(string(data)) != "normal" {
		t.Fatalf("mode marker should stay normal after refused upgrade, data=%q err=%v", data, err)
	}

	// A normal resume still works.
	resp = send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme"}})
	if !resp.OK {
		t.Fatalf("normal resume should succeed, got %+v", resp)
	}
	send(t, sockPath, Request{Cmd: CmdStop})

	// A separate session started in inventory: a bare resume must be
	// refused (no silent downgrade back to normal).
	resp = send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "box2", "mode": "inventory"}})
	if !resp.OK {
		t.Fatalf("inventory start should succeed, got %+v", resp)
	}
	send(t, sockPath, Request{Cmd: CmdStop})
	resp = send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "box2"}})
	if resp.OK || !strings.Contains(resp.Error, "inventory mode") {
		t.Fatalf("bare resume of inventory session should fail, got %+v", resp)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}

func TestUnknownModeRejected(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	sessionRoot := t.TempDir()
	done, sockPath := startTestDaemon(t, stateDir, sessionRoot)

	resp := send(t, sockPath, Request{Cmd: CmdStart, Args: map[string]string{"name": "acme", "mode": "bogus"}})
	if resp.OK || !strings.Contains(resp.Error, "unknown session mode") {
		t.Fatalf("bogus mode should be rejected, got %+v", resp)
	}

	send(t, sockPath, Request{Cmd: CmdDaemonStop})
	<-done
}
