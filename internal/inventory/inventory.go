// Package inventory owns the pending-card queue behind inventory mode:
// captures that landed silently (no popup) and await review in the
// `snapshell inventory` TUI.
//
// The queue is a single JSON file (<sessionDir>/pending.json) owned by the
// daemon — the review TUI is a client and only ever mutates it over IPC.
// Every mutation is written through atomically (temp file + rename), so
// pending cards survive a daemon crash or restart.
package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"snapshell/internal/blog"
)

// Kind identifies the kind of content a pending card holds.
type Kind string

// The two card kinds. There is deliberately no note kind — standalone notes
// bypass the queue and go straight to blog.md.
const (
	KindImage Kind = "image"
	KindCode  Kind = "code"
)

// Card is one pending capture. Path is relative to the session dir (image
// cards only); Text holds the captured command text (code cards only).
// AbsPath is derived by the daemon when answering the list IPC verb and is
// never persisted — it lets the TUI open the file / read its dimensions
// without resolving session-relative paths itself.
type Card struct {
	ID      int       `json:"id"`
	Kind    Kind      `json:"kind"`
	Path    string    `json:"path,omitempty"`
	Text    string    `json:"text,omitempty"`
	Created time.Time `json:"created"`

	AbsPath string `json:"abspath,omitempty"`
}

// ErrNotFound is returned when a commit/discard targets a card id that is
// no longer in the queue.
var ErrNotFound = errors.New("no pending card with that id")

// Queue is the in-memory pending-card queue for one session, mirrored to
// disk on every mutation. It owns the single-writer guarantee for the queue
// files: the daemon serializes access through it, the TUI never touches the
// file directly.
type Queue struct {
	mu     sync.Mutex
	dir    string
	path   string
	nextID int
	cards  []Card
}

// fileState is the on-disk shape of the queue.
type fileState struct {
	NextID int    `json:"next_id"`
	Cards  []Card `json:"cards"`
}

// Load reads the queue for a session dir from pending.json. A missing file
// is an empty queue, not an error.
func Load(sessionDir string) (*Queue, error) {
	path := filepath.Join(sessionDir, "pending.json")
	q := &Queue{dir: sessionDir, path: path, nextID: 1}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return q, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pending.json: %w", err)
	}

	var st fileState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse pending.json: %w", err)
	}
	q.nextID = st.NextID
	if q.nextID < 1 {
		q.nextID = 1
	}
	q.cards = st.Cards
	return q, nil
}

// AppendImage queues a captured screenshot, addressed by its path relative
// to the session dir (attachments/NNN.png).
func (q *Queue) AppendImage(relPath string) error {
	return q.append(Card{Kind: KindImage, Path: relPath})
}

// AppendCode queues captured command text.
func (q *Queue) AppendCode(text string) error {
	return q.append(Card{Kind: KindCode, Text: text})
}

func (q *Queue) append(c Card) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	c.ID = q.nextID
	c.Created = time.Now()
	q.nextID++
	q.cards = append(q.cards, c)
	return q.saveLocked()
}

// List returns the pending cards oldest-first. The returned slice is a copy;
// callers may not mutate the queue through it.
func (q *Queue) List() []Card {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Card, len(q.cards))
	copy(out, q.cards)
	return out
}

// Len reports how many cards are pending.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.cards)
}

// Commit appends a card to blog.md via the same formatting contract normal
// mode uses (image/code entry, optional caption) and removes it from the
// queue. An empty caption means append as-is. The blog append happens first
// — if it fails the card stays queued so a retry can't double-write.
func (q *Queue) Commit(id int, caption string) error {
	q.mu.Lock()
	i := q.index(id)
	if i < 0 {
		q.mu.Unlock()
		return fmt.Errorf("%w (id=%d)", ErrNotFound, id)
	}
	c := q.cards[i]

	var err error
	switch c.Kind {
	case KindImage:
		err = blog.Append(q.dir, blog.Entry{Kind: blog.KindImage, Caption: caption, ImagePath: c.Path})
	case KindCode:
		err = blog.Append(q.dir, blog.Entry{Kind: blog.KindCode, Caption: caption, CodeText: c.Text})
	default:
		q.mu.Unlock()
		return fmt.Errorf("cannot commit unknown card kind %q", c.Kind)
	}
	if err != nil {
		q.mu.Unlock()
		return fmt.Errorf("append to blog.md: %w", err)
	}

	q.cards = append(q.cards[:i], q.cards[i+1:]...)
	if err := q.saveLocked(); err != nil {
		q.mu.Unlock()
		return err
	}
	q.mu.Unlock()
	return nil
}

// Discard removes a card from the queue permanently — no trash, no
// soft-delete. For image cards the underlying file is deleted too. Callers
// must confirm the idempotent-on-disk effect via an explicit confirmation
// flag in the IPC request; this is the daemon-side enforcement point.
func (q *Queue) Discard(id int) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	i := q.index(id)
	if i < 0 {
		return fmt.Errorf("%w (id=%d)", ErrNotFound, id)
	}
	c := q.cards[i]

	if c.Kind == KindImage {
		if err := os.Remove(filepath.Join(q.dir, c.Path)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete discarded screenshot %s: %w", c.Path, err)
		}
	}

	q.cards = append(q.cards[:i], q.cards[i+1:]...)
	return q.saveLocked()
}

// index returns the position of card id, or -1.
func (q *Queue) index(id int) int {
	for i, c := range q.cards {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// saveLocked writes the queue to disk atomically. q.mu must be held.
func (q *Queue) saveLocked() error {
	st := fileState{NextID: q.nextID, Cards: q.cards}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode queue: %w", err)
	}
	if err := writeFileAtomic(q.path, data); err != nil {
		return fmt.Errorf("persist queue: %w", err)
	}
	return nil
}

// writeFileAtomic writes via a temp file + rename so a crash never leaves a
// half-written queue behind.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
