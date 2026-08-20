package daemon

import (
	"encoding/json"
	"fmt"

	"snapshell/internal/inventory"
)

// Request is a single newline-delimited JSON request sent by the CLI over
// the Unix socket. One request per connection; the connection closes after
// the response.
type Request struct {
	Cmd  string            `json:"cmd"`
	Args map[string]string `json:"args,omitempty"`
}

// Response is the daemon's reply. Exactly one of Message/Error is set.
// Data carries structured payloads (card lists, status details) for the
// verbs that return more than a human-readable message; it is empty for
// plain ok/fail replies.
type Response struct {
	OK      bool            `json:"ok"`
	Message string          `json:"message,omitempty"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Command names understood by the daemon.
const (
	CmdStart      = "start"       // start a session (args: name, mode)
	CmdStop       = "stop"        // stop the active session
	CmdStatus     = "status"      // report pid + active session
	CmdDaemonStop = "daemon_stop" // shut the daemon down

	// Inventory-mode verbs (args: see each handler).
	CmdList    = "list"    // list pending cards (no args)
	CmdCommit  = "commit"  // commit a card (args: id, caption)
	CmdDiscard = "discard" // permanently discard a card (args: id, confirm)
	CmdNote    = "note"    // append a standalone note (args: text)

	// CmdAutoCapture queues a successful command as a pending card in an
	// inventory session when [auto].enabled is set and the command is not
	// excluded. Sent by the shell hook (_hook-record) after every command
	// that exited 0 while a session is active; the daemon decides whether
	// auto mode applies. Args: text, exit, source, kitty-window,
	// kitty-listen.
	CmdAutoCapture = "autocapture"
)

// StatusData is the structured payload behind the status verb.
type StatusData struct {
	Session string `json:"session,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Entries int    `json:"entries,omitempty"`
	Pending int    `json:"pending,omitempty"`
	Dir     string `json:"dir,omitempty"`
}

// ListData is the structured payload behind the list verb. Dir is the
// absolute session folder, so the review TUI can read blog.md directly for
// its render view without resolving session-relative paths itself.
type ListData struct {
	Dir   string           `json:"dir"`
	Cards []inventory.Card `json:"cards"`
}

func ok(msg string) Response   { return Response{OK: true, Message: msg} }
func fail(msg string) Response { return Response{OK: false, Error: msg} }

func encodeResponse(resp Response) ([]byte, error) {
	b, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	return append(b, '\n'), nil
}

func decodeRequest(data []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return Request{}, fmt.Errorf("malformed request: %w", err)
	}
	if req.Cmd == "" {
		return Request{}, fmt.Errorf("missing cmd field")
	}
	return req, nil
}
