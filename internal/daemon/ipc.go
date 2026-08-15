package daemon

import (
	"encoding/json"
	"fmt"
)

// Request is a single newline-delimited JSON request sent by the CLI over
// the Unix socket. One request per connection; the connection closes after
// the response.
type Request struct {
	Cmd  string            `json:"cmd"`
	Args map[string]string `json:"args,omitempty"`
}

// Response is the daemon's reply. Exactly one of Message/Error is set.
type Response struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Command names understood by the daemon.
const (
	CmdStart      = "start"       // start a session (args: name)
	CmdStop       = "stop"        // stop the active session
	CmdStatus     = "status"      // report pid + active session
	CmdDaemonStop = "daemon_stop" // shut the daemon down
)

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
