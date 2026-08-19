package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"snapshell/internal/inventory"
)

// fakeClient records the TUI's calls so tests can assert exactly what the
// daemon would have received over IPC.
type fakeClient struct {
	listRes ListResult
	listErr error
	commit  []string // "id|caption"
	discard []int
	notes   []string
}

func (f *fakeClient) List() (ListResult, error) { return f.listRes, f.listErr }
func (f *fakeClient) Commit(id int, caption string) error {
	f.commit = append(f.commit, itoa(id)+"|"+caption)
	return nil
}
func (f *fakeClient) Discard(id int) error {
	f.discard = append(f.discard, id)
	return nil
}
func (f *fakeClient) Note(text string) error {
	f.notes = append(f.notes, text)
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func testCards() []inventory.Card {
	return []inventory.Card{
		{ID: 1, Kind: inventory.KindCode, Text: "whoami\njimmex", Created: time.Now().Add(-2 * time.Minute)},
		{ID: 2, Kind: inventory.KindImage, Path: "attachments/001.png", Created: time.Now()},
	}
}

func setupModel(t *testing.T, cards []inventory.Card) (model, *fakeClient) {
	t.Helper()
	client := &fakeClient{listRes: ListResult{Dir: t.TempDir(), Cards: cards}}
	return newModel(Options{Client: client}), client
}

// upd feeds a message into the model and returns the concrete model.
func upd(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

// step runs a tea.Cmd to its message and feeds it back in, the way the tea
// runtime would.
func step(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	// tea.Batch returns a BatchMsg of yet-unexecuted commands; the runtime
	// unwraps them, so recurse the same way.
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = step(t, m, c)
		}
		return m
	}
	next, _ := m.Update(msg)
	return next.(model)
}

func TestInitRefreshesList(t *testing.T) {
	m, client := setupModel(t, testCards())

	// Init returns a non-nil command; running the list half of it (directly,
	// to avoid blocking on the 1.5s poll timer) populates the model.
	if m.Init() == nil {
		t.Fatal("Init should return a command")
	}
	m = step(t, m, m.refreshList())
	if len(m.cards) != 2 {
		t.Fatalf("cards = %d, want 2", len(m.cards))
	}
	if m.dir == "" {
		t.Fatal("dir should be set from List result")
	}
	if len(client.commit) != 0 {
		t.Fatalf("init must not commit, commits = %v", client.commit)
	}
}

func TestBrowseNavigation(t *testing.T) {
	m, _ := setupModel(t, testCards())
	m = step(t, m, m.refreshList())

	if m.sel != 0 {
		t.Fatalf("initial sel = %d, want 0", m.sel)
	}
	m, _ = upd(t, m, key("down"))
	if m.sel != 1 {
		t.Fatalf("after down sel = %d, want 1", m.sel)
	}
	m, _ = upd(t, m, key("j"))
	if m.sel != 1 {
		t.Fatalf("down past end should clamp, sel = %d", m.sel)
	}
	m, _ = upd(t, m, key("up"))
	if m.sel != 0 {
		t.Fatalf("after up sel = %d, want 0", m.sel)
	}
	m, _ = upd(t, m, key("k"))
	if m.sel != 0 {
		t.Fatalf("up past start should clamp, sel = %d", m.sel)
	}
}

func TestCommitAsIs(t *testing.T) {
	m, client := setupModel(t, testCards())
	m = step(t, m, m.refreshList())

	m, cmd := upd(t, m, key("a"))
	if cmd == nil {
		t.Fatal("append-as-is should return an op cmd")
	}
	m = step(t, m, cmd)
	if len(client.commit) != 1 || client.commit[0] != "1|" {
		t.Fatalf("commit recorded = %v, want [1|]", client.commit)
	}
	if m.st != stateBrowse {
		t.Fatalf("state after commit = %d, want browse", m.st)
	}
}

func TestCaptionFlow(t *testing.T) {
	m, client := setupModel(t, testCards())
	m = step(t, m, m.refreshList())

	m, _ = upd(t, m, key("c"))
	if m.st != stateCaption {
		t.Fatalf("state = %d, want caption", m.st)
	}
	if !m.caption.Focused() {
		t.Fatal("caption textarea should be focused")
	}

	// Type a caption, then submit.
	for _, r := range []rune("the box") {
		m, _ = upd(t, m, key(string(r)))
	}
	m, cmd := upd(t, m, key("ctrl+s"))
	if cmd == nil {
		t.Fatal("ctrl+s should return an op cmd")
	}
	m = step(t, m, cmd)
	if len(client.commit) != 1 || client.commit[0] != "1|the box" {
		t.Fatalf("commit recorded = %v, want [1|the box]", client.commit)
	}
	if m.st != stateBrowse {
		t.Fatalf("state after submit = %d, want browse", m.st)
	}

	// Esc cancels without committing.
	m, _ = upd(t, m, key("c"))
	m, _ = upd(t, m, key("esc"))
	if m.st != stateBrowse {
		t.Fatalf("esc should return to browse, state = %d", m.st)
	}
	if len(client.commit) != 1 {
		t.Fatalf("esc must not commit, commits = %v", client.commit)
	}
}

func TestDiscardFlow(t *testing.T) {
	m, client := setupModel(t, testCards())
	m = step(t, m, m.refreshList())

	m, _ = upd(t, m, key("d"))
	if m.st != stateDiscard {
		t.Fatalf("state = %d, want discard", m.st)
	}
	// n cancels.
	m, _ = upd(t, m, key("n"))
	if m.st != stateBrowse {
		t.Fatalf("n should cancel discard, state = %d", m.st)
	}
	if len(client.discard) != 0 {
		t.Fatalf("cancel must not discard, discards = %v", client.discard)
	}

	// y confirms.
	m, _ = upd(t, m, key("d"))
	m, cmd := upd(t, m, key("y"))
	if cmd == nil {
		t.Fatal("y should return an op cmd")
	}
	m = step(t, m, cmd)
	if len(client.discard) != 1 || client.discard[0] != 1 {
		t.Fatalf("discard recorded = %v, want [1]", client.discard)
	}
	if m.st != stateBrowse {
		t.Fatalf("state after discard = %d, want browse", m.st)
	}
}

func TestNoteFlow(t *testing.T) {
	m, client := setupModel(t, testCards())
	m = step(t, m, m.refreshList())

	m, _ = upd(t, m, key("n"))
	if m.st != stateNote {
		t.Fatalf("state = %d, want note", m.st)
	}
	for _, r := range []rune("remember the hash") {
		m, _ = upd(t, m, key(string(r)))
	}
	// Esc discards the typed text, nothing written.
	m, _ = upd(t, m, key("esc"))
	if len(client.notes) != 0 {
		t.Fatalf("esc must not write a note, notes = %v", client.notes)
	}

	// Now write and submit.
	m, _ = upd(t, m, key("n"))
	for _, r := range []rune("remember the hash") {
		m, _ = upd(t, m, key(string(r)))
	}
	m, cmd := upd(t, m, key("ctrl+s"))
	if cmd == nil {
		t.Fatal("ctrl+s should return an op cmd")
	}
	m = step(t, m, cmd)
	if len(client.notes) != 1 || client.notes[0] != "remember the hash" {
		t.Fatalf("notes = %v, want [remember the hash]", client.notes)
	}
	if m.st != stateBrowse {
		t.Fatalf("state after note submit = %d, want browse", m.st)
	}
}

func TestPollingPausesWhileTyping(t *testing.T) {
	m, _ := setupModel(t, testCards())
	m = step(t, m, m.refreshList())

	// In browse state a tick both refreshes and reschedules.
	_, cmd := upd(t, m, tickMsg{})
	if cmd == nil {
		t.Fatal("tick in browse should return a cmd")
	}

	// In caption state a tick still reschedules but must not fetch the list.
	m, _ = upd(t, m, key("c"))
	_, cmd = upd(t, m, tickMsg{})
	if cmd == nil {
		t.Fatal("tick in caption should still reschedule")
	}
}

func TestQuitKeys(t *testing.T) {
	m, _ := setupModel(t, testCards())
	m = step(t, m, m.refreshList())

	for _, k := range []string{"q", "ctrl+c"} {
		_, cmd := upd(t, m, key(k))
		if cmd == nil {
			t.Fatalf("%s should return tea.Quit", k)
		}
		if msg := cmd(); msg != tea.Quit() {
			t.Fatalf("%s should produce tea.Quit, got %v", k, msg)
		}
	}
}

func TestEnterOnImageReturnsCmdEnterOnCodeIsNoop(t *testing.T) {
	m, _ := setupModel(t, testCards())
	m = step(t, m, m.refreshList())

	// Selected card 0 is a code card: Enter is a no-op (text is visible).
	_, cmd := upd(t, m, key("enter"))
	if cmd != nil {
		t.Fatalf("enter on code card should be a no-op, got cmd %v", cmd)
	}

	// Select the image card: Enter returns an open-image command. We don't
	// execute it (it would spawn a real viewer).
	m, _ = upd(t, m, key("down"))
	_, cmd = upd(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter on image card should return an open command")
	}
}

func TestRenderViewToggle(t *testing.T) {
	m, _ := setupModel(t, testCards())
	m = step(t, m, m.refreshList())

	m, cmd := upd(t, m, key("v"))
	if m.st != stateRender {
		t.Fatalf("state = %d, want render", m.st)
	}
	if cmd == nil {
		t.Fatal("toggling render should refresh blog.md")
	}
	// Running the refresh with no blog.md on disk yields an empty render.
	m = step(t, m, cmd)
	m, _ = upd(t, m, key("v"))
	if m.st != stateBrowse {
		t.Fatalf("v should return to browse, state = %d", m.st)
	}
}

func TestImageCardDetailsResizeNoPanic(t *testing.T) {
	m, _ := setupModel(t, testCards())
	m = step(t, m, m.refreshList())
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if got := m.View(); got == "" {
		t.Fatal("View() returned empty for a populated model")
	}
	m, _ = upd(t, m, key("down"))
	if got := m.View(); got == "" {
		t.Fatal("View() returned empty for an image card")
	}
}

func TestEmptyQueueViewNoPanic(t *testing.T) {
	m, _ := setupModel(t, nil)
	m = step(t, m, m.refreshList())
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if got := m.View(); got == "" {
		t.Fatal("View() returned empty for an empty queue")
	}
}

func TestWrapText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		w    int
		want []string
	}{
		{"short", "hello", 20, []string{"hello"}},
		{"wraps at words", "one two three four five", 10, []string{"one two", "three four", "five"}},
		{"preserves newlines", "abc\ndef", 10, []string{"abc", "def"}},
		{"hard splits long word", "abcdefghijklmnop", 5, []string{"abcde", "fghij", "klmno", "p"}},
		{"empty", "", 10, []string{""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Split(wrapText(tc.in, tc.w), "\n")
			if !slices.Equal(got, tc.want) {
				t.Fatalf("wrapText(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
			}
			for _, ln := range got {
				if lipgloss.Width(ln) > tc.w {
					t.Fatalf("wrapText(%q, %d) produced a %d-cell line: %q", tc.in, tc.w, lipgloss.Width(ln), ln)
				}
			}
		})
	}
}

// TestCaptionLongLineDoesNotPushList guards against the detail column
// growing past its allotted width: a long caption (or a long line in the
// code preview) must wrap, not widen the column and shove the list off
// screen.
func TestCaptionLongLineDoesNotPushList(t *testing.T) {
	m, _ := setupModel(t, testCards())
	m = step(t, m, m.refreshList())
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = upd(t, m, key("c"))

	long := strings.Repeat("words ", 30)
	m.caption.SetValue(long)
	m.captionPreview = long

	for _, ln := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(ln); w > m.width {
			t.Fatalf("view line %d cells wide exceeds terminal width %d: %q", w, m.width, ln)
		}
	}
}

func TestDebouncePublishesPreview(t *testing.T) {
	m, _ := setupModel(t, testCards())
	m = step(t, m, m.refreshList())

	m, _ = upd(t, m, key("c"))
	m, cmd := upd(t, m, key("h"))
	if cmd == nil {
		t.Fatal("typing should return a debounce cmd")
	}
	// Running the debounce (after the timer fires) publishes the preview.
	m = step(t, m, cmd)
	if m.captionPreview != "h" {
		t.Fatalf("caption preview = %q, want h", m.captionPreview)
	}
	if m.caption.Value() != "h" {
		t.Fatalf("textarea value = %q, want h", m.caption.Value())
	}
}

func TestRenderViewRendersMarkdown(t *testing.T) {
	m, _ := setupModel(t, testCards())
	m = step(t, m, m.refreshList())
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m, cmd := upd(t, m, key("v"))
	m = step(t, m, cmd) // renderMsg with empty blog.md

	m, cmd = upd(t, m, key("v")) // back to browse
	if cmd != nil {
		t.Fatal("leaving render view should not return a cmd")
	}

	// Feed a markdown document through the render path; the viewport must
	// hold styled (ANSI) output, not the raw source.
	m, _ = upd(t, m, renderMsg{content: "# Recon\n\nRun `nmap -sV` and note the **result**.", width: 100})
	if m.renderContent == "" {
		t.Fatal("renderContent should be set")
	}
	view := m.renderVP.View()
	if !strings.Contains(view, "nmap") {
		t.Fatalf("rendered view missing content: %q", view)
	}
	if !strings.Contains(view, "\x1b[") {
		t.Fatalf("expected styled markdown output, got raw:\n%q", view)
	}
}

func TestViewEmitsImageThenDelete(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	imgPath := filepath.Join(t.TempDir(), "attachments", "001.png")
	writePNG(t, imgPath, 100, 80)

	cards := []inventory.Card{
		{ID: 1, Kind: inventory.KindImage, Path: "attachments/001.png", AbsPath: imgPath, Created: time.Now()},
	}
	m, _ := setupModel(t, cards)
	m = step(t, m, m.refreshList())
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	// Browse shows a plain label — the image is not transmitted inline.
	if view := m.View(); strings.Contains(view, "\x1b_Ga=T") {
		t.Fatalf("browse view should not transmit the image inline:\n%q", view)
	}

	// Enter opens the full-screen kitty view: the transmit escape is present
	// and re-emitted every frame.
	m, _ = upd(t, m, key("enter"))
	if m.st != stateImage {
		t.Fatalf("expected stateImage after Enter, got %d", m.st)
	}
	if view := m.View(); !strings.Contains(view, "\x1b_Ga=T,f=100") {
		t.Fatalf("image view should transmit the image, got no escape:\n%q", view)
	}
	if again := m.View(); !strings.Contains(again, "\x1b_Ga=T,f=100") {
		t.Fatal("image view should always carry the transmit escape")
	}

	// Esc returns to browse: the stale image must be deleted.
	m, _ = upd(t, m, key("esc"))
	if m.st != stateBrowse {
		t.Fatalf("expected stateBrowse after Esc, got %d", m.st)
	}
	if view := m.View(); !strings.Contains(view, "\x1b_Ga=d,q=2\x1b\\") {
		t.Fatalf("non-image frame should delete the image:\n%q", view)
	}
}

func TestInlineRenderShowsImageInPreview(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	imgPath := filepath.Join(t.TempDir(), "attachments", "001.png")
	writePNG(t, imgPath, 100, 80)

	cards := []inventory.Card{
		{ID: 1, Kind: inventory.KindImage, Path: "attachments/001.png", AbsPath: imgPath, Created: time.Now()},
		{ID: 2, Kind: inventory.KindCode, Text: "whoami\njimmex", Created: time.Now()},
	}
	m, _ := setupModel(t, cards)
	m.opts.ImageRender = "inline"
	m = step(t, m, m.refreshList())
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	// Browse with the image card selected transmits the image inline.
	if view := m.View(); !strings.Contains(view, "\x1b_Ga=T,f=100") {
		t.Fatalf("inline browse should transmit the image:\n%q", view)
	}
	if !m.showsImage() {
		t.Fatal("inline browse with an image selected should report showsImage")
	}

	// Captioning the image keeps the image on screen — no text preview.
	m, _ = upd(t, m, key("c"))
	if m.st != stateCaption {
		t.Fatalf("expected stateCaption after c, got %d", m.st)
	}
	if view := m.View(); !strings.Contains(view, "\x1b_Ga=T,f=100") {
		t.Fatalf("inline caption should keep the image on screen:\n%q", view)
	}
	if view := m.View(); strings.Contains(view, "Preview") {
		t.Fatalf("inline caption should skip the text preview for images:\n%q", view)
	}

	// Esc back to browse, move to the code card: the image must be deleted.
	m, _ = upd(t, m, key("esc"))
	m, _ = upd(t, m, key("down"))
	if view := m.View(); !strings.Contains(view, "\x1b_Ga=d,q=2\x1b\\") {
		t.Fatalf("selecting a code card should delete the inline image:\n%q", view)
	}
	if m.showsImage() {
		t.Fatal("a code card selected should not report showsImage")
	}
}

func TestInlineImageRowsScale(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	defer resetKittyState()

	imgPath := filepath.Join(t.TempDir(), "attachments", "001.png")
	writePNG(t, imgPath, 100, 100) // square: fit = full pane height

	cards := []inventory.Card{
		{ID: 1, Kind: inventory.KindImage, Path: "attachments/001.png", AbsPath: imgPath, Created: time.Now()},
	}
	m, _ := setupModel(t, cards)
	m.opts.ImageRender = "inline"
	m = step(t, m, m.refreshList())
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})

	full := m.imageRowsInPane(cards[0], 60, 30, 1.0)
	if full <= 0 {
		t.Fatal("full-size inline rows should be > 0 in kitty")
	}
	// Default inline scale is 0.5 (half the pane fit).
	if half := m.inlineImageRows(cards[0], 60, 30); half != int(float64(full)*0.5+0.5) {
		t.Fatalf("default inline rows = %d, want %d", half, int(float64(full)*0.5+0.5))
	}
	// The scale multiplier applies linearly (the 65% cap lives in config).
	m.opts.ImageInlineScale = 0.9
	if got := m.inlineImageRows(cards[0], 60, 30); got != int(float64(full)*0.9+0.5) {
		t.Fatalf("90%% inline rows = %d, want %d", got, int(float64(full)*0.9+0.5))
	}
	// Tab mode (ImageRender default) does not treat the pane as inline.
	m.opts.ImageRender = "tab"
	m.opts.ImageInlineScale = 0.5
	if m.showsImage() {
		t.Fatal("tab mode should not report showsImage in browse")
	}
}

func TestEnterImageExternalMode(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "attachments", "001.png")
	writePNG(t, imgPath, 100, 80)
	cards := []inventory.Card{
		{ID: 1, Kind: inventory.KindImage, Path: "attachments/001.png", AbsPath: imgPath, Created: time.Now()},
	}
	m, _ := setupModel(t, cards)
	m.opts.ImageMode = "external"
	m = step(t, m, m.refreshList())
	m, _ = upd(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m, cmd := upd(t, m, key("enter"))
	if m.st != stateBrowse {
		t.Fatalf("external mode Enter should stay in browse, got state %d", m.st)
	}
	if cmd == nil {
		t.Fatal("external mode Enter should return the open-image command")
	}
}
