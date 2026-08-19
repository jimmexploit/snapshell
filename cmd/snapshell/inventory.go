package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"snapshell/internal/config"
	"snapshell/internal/daemon"
	"snapshell/internal/tui"
)

func newInventoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inventory",
		Short: "Open the review TUI for the active inventory-mode session",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return runInventory()
		},
	}
}

// runInventory reattaches the review TUI to the currently active session.
// It refuses to start when there's no active session or the session is in
// normal mode — a blank/broken UI is worse than a clear error.
func runInventory() error {
	resp, err := sendRequest(daemon.Request{Cmd: daemon.CmdStatus})
	if err != nil {
		return err
	}
	var st daemon.StatusData
	if err := json.Unmarshal(resp.Data, &st); err != nil {
		return fmt.Errorf("malformed status from daemon: %v", err)
	}
	if st.Session == "" {
		return fmt.Errorf("no active session — start one with 'snapshell start inventory <name>'")
	}
	if st.Mode != "inventory" {
		return fmt.Errorf("active session %q is in normal mode — the review TUI only works for inventory sessions ('snapshell start inventory <name>')", st.Session)
	}
	return runTUI()
}

// runTUI launches the foreground review TUI. It is reached both from
// `snapshell start inventory <name>` (right after the session starts) and
// from a bare `snapshell inventory` (after having quit the TUI once).
func runTUI() error {
	if !isTerminal(os.Stdout) {
		return fmt.Errorf("the review TUI needs a terminal — run it in a terminal window")
	}
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Default()
	}
	return tui.Run(tui.Options{
		Client:           ipcClient{},
		ImageViewer:      cfg.Inventory.ImageViewer,
		CloseDelay:       cfg.CloseDelay(),
		ImageMode:        cfg.ImageMode(),
		ImageScale:       cfg.ImageScale(),
		ImageRender:      cfg.ImageRender(),
		ImageInlineScale: cfg.ImageScaleInline(),
		BlogImageScale:   cfg.BlogImageScale(),
	})
}

// ipcClient is the TUI's view of the daemon: every mutation goes over the
// Unix socket, keeping the daemon the single writer of queue and blog.md.
type ipcClient struct{}

func (ipcClient) List() (tui.ListResult, error) {
	resp, err := sendRequest(daemon.Request{Cmd: daemon.CmdList})
	if err != nil {
		return tui.ListResult{}, err
	}
	var data daemon.ListData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return tui.ListResult{}, fmt.Errorf("malformed card list from daemon: %v", err)
	}
	return tui.ListResult{Dir: data.Dir, Cards: data.Cards}, nil
}

func (ipcClient) Commit(id int, caption string) error {
	_, err := sendRequest(daemon.Request{Cmd: daemon.CmdCommit, Args: map[string]string{
		"id":      strconv.Itoa(id),
		"caption": caption,
	}})
	return err
}

func (ipcClient) Discard(id int) error {
	// The daemon requires the confirm flag regardless of what the TUI's own
	// y/n prompt already asked for.
	_, err := sendRequest(daemon.Request{Cmd: daemon.CmdDiscard, Args: map[string]string{
		"id":      strconv.Itoa(id),
		"confirm": "true",
	}})
	return err
}

func (ipcClient) Note(text string) error {
	_, err := sendRequest(daemon.Request{Cmd: daemon.CmdNote, Args: map[string]string{
		"text": text,
	}})
	return err
}
