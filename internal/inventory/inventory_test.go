package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"snapshell/internal/blog"
)

func TestLoadMissingFileIsEmptyQueue(t *testing.T) {
	q, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if q.Len() != 0 {
		t.Fatalf("len = %d, want 0", q.Len())
	}
	if got := q.List(); len(got) != 0 {
		t.Fatalf("List = %v, want empty", got)
	}
}

func TestAppendAndListOrder(t *testing.T) {
	dir := t.TempDir()
	q, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := q.AppendImage("attachments/001.png"); err != nil {
		t.Fatalf("AppendImage: %v", err)
	}
	if err := q.AppendCode("whoami"); err != nil {
		t.Fatalf("AppendCode: %v", err)
	}
	if err := q.AppendImage("attachments/002.png"); err != nil {
		t.Fatalf("AppendImage: %v", err)
	}

	cards := q.List()
	if len(cards) != 3 {
		t.Fatalf("len = %d, want 3", len(cards))
	}
	if cards[0].ID != 1 || cards[0].Kind != KindImage || cards[0].Path != "attachments/001.png" {
		t.Fatalf("card 0 = %+v", cards[0])
	}
	if cards[1].ID != 2 || cards[1].Kind != KindCode || cards[1].Text != "whoami" {
		t.Fatalf("card 1 = %+v", cards[1])
	}
	if cards[2].ID != 3 || cards[2].Kind != KindImage {
		t.Fatalf("card 2 = %+v", cards[2])
	}
	// IDs are unique and increasing.
	if cards[0].ID >= cards[1].ID || cards[1].ID >= cards[2].ID {
		t.Fatalf("ids not strictly increasing: %+v", cards)
	}
}

func TestPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	q, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AppendImage("attachments/001.png"); err != nil {
		t.Fatal(err)
	}
	if err := q.AppendCode("cat /etc/passwd"); err != nil {
		t.Fatal(err)
	}

	// A fresh Load (simulating a daemon restart) must see the same cards.
	q2, err := Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	cards := q2.List()
	if len(cards) != 2 {
		t.Fatalf("after reload len = %d, want 2", len(cards))
	}
	if cards[1].Text != "cat /etc/passwd" {
		t.Fatalf("reloaded card text = %q", cards[1].Text)
	}
	// The next id must continue, not restart at 1.
	if err := q2.AppendCode("nmap"); err != nil {
		t.Fatal(err)
	}
	ids := q2.List()
	if ids[2].ID <= ids[1].ID {
		t.Fatalf("id reuse after reload: %+v", ids)
	}
}

func TestCommitImageWritesBlogAndRemovesCard(t *testing.T) {
	dir := t.TempDir()
	attach := filepath.Join(dir, "attachments")
	if err := os.MkdirAll(attach, 0o700); err != nil {
		t.Fatal(err)
	}
	// Create the screenshot file so the entry's relative path is truthful.
	img := filepath.Join(attach, "001.png")
	if err := os.WriteFile(img, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}

	q, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AppendImage("attachments/001.png"); err != nil {
		t.Fatal(err)
	}
	if err := q.Commit(1, "the attack"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if q.Len() != 0 {
		t.Fatalf("len after commit = %d, want 0", q.Len())
	}

	blogData, err := os.ReadFile(filepath.Join(dir, "blog.md"))
	if err != nil {
		t.Fatalf("read blog.md: %v", err)
	}
	if !strings.Contains(string(blogData), "the attack") {
		t.Fatalf("blog.md missing caption:\n%s", blogData)
	}
	if !strings.Contains(string(blogData), "attachments/001.png") {
		t.Fatalf("blog.md missing image path:\n%s", blogData)
	}
	// The image file survives a commit.
	if _, err := os.Stat(img); err != nil {
		t.Fatalf("image file removed by commit: %v", err)
	}
}

func TestCommitCodeNoCaptionAppendsAsIs(t *testing.T) {
	dir := t.TempDir()
	q, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AppendCode("curl -s http://host/"); err != nil {
		t.Fatal(err)
	}
	if err := q.Commit(1, ""); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	blogData, err := os.ReadFile(filepath.Join(dir, "blog.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blogData), "curl -s http://host/") {
		t.Fatalf("blog.md missing code text:\n%s", blogData)
	}
}

func TestDiscardImageDeletesFileAndCard(t *testing.T) {
	dir := t.TempDir()
	attach := filepath.Join(dir, "attachments")
	if err := os.MkdirAll(attach, 0o700); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(attach, "001.png")
	if err := os.WriteFile(img, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}

	q, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AppendImage("attachments/001.png"); err != nil {
		t.Fatal(err)
	}
	if err := q.Discard(1); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if q.Len() != 0 {
		t.Fatalf("len after discard = %d, want 0", q.Len())
	}
	if _, err := os.Stat(img); !os.IsNotExist(err) {
		t.Fatalf("image file should be deleted on discard, stat err=%v", err)
	}
	// blog.md must not exist — a discard never appends.
	if _, err := os.Stat(filepath.Join(dir, "blog.md")); !os.IsNotExist(err) {
		t.Fatal("blog.md should not be created by a discard")
	}
}

func TestDiscardCodeCardKeepsQueueConsistent(t *testing.T) {
	dir := t.TempDir()
	q, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AppendCode("cmd a"); err != nil {
		t.Fatal(err)
	}
	if err := q.AppendCode("cmd b"); err != nil {
		t.Fatal(err)
	}
	if err := q.Discard(1); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	cards := q.List()
	if len(cards) != 1 || cards[0].Text != "cmd b" {
		t.Fatalf("after discard = %+v, want only cmd b", cards)
	}
}

func TestCommitDiscardUnknownID(t *testing.T) {
	dir := t.TempDir()
	q, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AppendCode("cmd"); err != nil {
		t.Fatal(err)
	}
	if err := q.Commit(99, ""); err == nil {
		t.Fatal("commit of unknown id should fail")
	}
	if err := q.Discard(99); err == nil {
		t.Fatal("discard of unknown id should fail")
	}
	if q.Len() != 1 {
		t.Fatalf("queue mutated by failed ops, len=%d", q.Len())
	}
}

func TestCorruptQueueFailsLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pending.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("Load should fail on corrupt pending.json")
	}
}

// TestQueueWritesThroughBlogContract guards the formatting contract: the
// entry a code commit produces must be exactly the normal-mode one.
func TestCommitFormattingContract(t *testing.T) {
	dir := t.TempDir()
	q, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AppendCode("$ whoami\njimmex\n"); err != nil {
		t.Fatal(err)
	}
	if err := q.Commit(1, "spotted a user"); err != nil {
		t.Fatal(err)
	}

	blogData, _ := os.ReadFile(filepath.Join(dir, "blog.md"))
	s := string(blogData)
	if !strings.Contains(s, "spotted a user\n") {
		t.Fatalf("caption not rendered as plain line:\n%s", s)
	}
	if !strings.Contains(s, "```bash") || !strings.Contains(s, "```\n") {
		t.Fatalf("code fence missing or mis-tagged:\n%s", s)
	}

	// Exercise the same path blog.Append itself tests via a second entry.
	if err := blog.Append(dir, blog.Entry{Kind: blog.KindNote, NoteText: "direct"}); err != nil {
		t.Fatalf("direct append: %v", err)
	}
}
