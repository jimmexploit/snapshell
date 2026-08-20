package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"snapshell/internal/daemon"
	"snapshell/internal/shellhook"
)

// newHookMarkCmd is the hidden helper the shell hook snippet calls to
// record a tmux row marker. It is not user-facing — the setup wizard
// installs the snippet that references it.
func newHookMarkCmd() *cobra.Command {
	var pane string
	var phase string
	var prevEnd string

	cmd := &cobra.Command{
		Use:    "_hook-mark",
		Short:  "Record a tmux row marker (called by the shell hook)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			// Silent on failure: this runs on every command prompt and
			// must not spam the terminal when tmux isn't available.
			// The end phase prints the recorded row so the shell hook can
			// stash it and feed it back as the next command's --prev-end.
			row, err := shellhook.Mark(pane, phase, prevEnd)
			if err == nil && phase == "end" && row >= 0 {
				fmt.Printf("%d\n", row)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pane, "pane", "", "tmux pane id (TMUX_PANE)")
	cmd.Flags().StringVar(&phase, "phase", "", "marker phase: start or end")
	cmd.Flags().StringVar(&prevEnd, "prev-end", "", "end row of the previous command (the row the current prompt started on); empty when unknown")
	_ = cmd.MarkFlagRequired("pane")
	_ = cmd.MarkFlagRequired("phase")

	return cmd
}

// newHookRecordCmd is the hidden helper the shell hook snippet calls to
// record the last command's text for the plain-shell Alt+2 fallback and to
// append it to the active session's command history.
func newHookRecordCmd() *cobra.Command {
	var text string
	var source string
	var kittyWindow string
	var kittyListen string
	var exitCode int

	cmd := &cobra.Command{
		Use:    "_hook-record",
		Short:  "Record the last command's text for the plain-shell Alt+2 fallback (called by the shell hook)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			// Silent on failure: this runs on every command and must not
			// spam the terminal.
			_ = shellhook.RecordCommand(source, kittyWindow, kittyListen, text)
			// Auto mode: when the command succeeded and a session is
			// active, whisper the completion to the daemon so it can queue
			// the command as a pending card. Best-effort and silent — the
			// daemon is the decision-maker (auto mode on, inventory
			// session, command not excluded); a missing/unreachable daemon
			// just means nothing gets queued.
			if exitCode == 0 && shellhook.ActiveSessionActive() {
				_, _ = sendRequest(daemon.Request{
					Cmd: daemon.CmdAutoCapture,
					Args: map[string]string{
						"text":         text,
						"exit":         strconv.Itoa(exitCode),
						"source":       source,
						"kitty-window": kittyWindow,
						"kitty-listen": kittyListen,
					},
				})
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "the executed command's text")
	cmd.Flags().StringVar(&source, "source", "", "where the command ran (tmux pane id, or tty device); shown in the session history")
	cmd.Flags().StringVar(&kittyWindow, "kitty-window", "", "the kitty window id (KITTY_WINDOW_ID) the command ran in, when it ran in a plain kitty tab; enables output capture")
	cmd.Flags().StringVar(&kittyListen, "kitty-listen", "", "the kitty listen socket (KITTY_LISTEN_ON) for that window")
	cmd.Flags().IntVar(&exitCode, "exit-code", -1, "the executed command's exit status; -1 when unknown (pre-auto-mode hooks)")

	return cmd
}

func snippetFor(shell string) (string, error) {
	switch shell {
	case "bash":
		return shellhook.BashSnippet, nil
	case "zsh":
		return shellhook.ZshSnippet, nil
	default:
		return "", fmt.Errorf("unknown shell %q (expected bash or zsh)", shell)
	}
}

// hookMarker is the comment line that both the current and any stale
// installed hook blocks start with. Its presence marks a hook block;
// whether it is current is checked by looking for the current helper
// command names inside it.
const hookMarker = "# --- snapshell shell integration ---"

func installHook(shell string) error {
	snippet, err := snippetFor(shell)
	if err != nil {
		return err
	}
	rc, err := shellhook.RcFile(shell)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(rc)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(data)

	// Existing hook block? If its content matches the current snippet,
	// leave it alone. Any other version — the ancient `shellhook mark`
	// calls, or a slightly older `_hook-mark` block missing a later fix —
	// is replaced in place so the user's shell keeps working without them
	// hand-editing their rc file.
	if i := strings.Index(text, hookMarker); i >= 0 {
		end := hookBlockEnd(text, i)
		if end < 0 {
			return fmt.Errorf("%s has a truncated snapshell hook block — delete the lines around %d manually and re-run setup", rc, i+1)
		}
		if text[i:end] == snippet {
			fmt.Printf("%s already has the snapshell hook — leaving it as-is\n", rc)
			return nil
		}
		updated := text[:i] + snippet + strings.TrimLeft(text[end:], "\n")
		if err := os.WriteFile(rc, []byte(updated), 0o600); err != nil {
			return fmt.Errorf("update hook in %s: %v", rc, err)
		}
		fmt.Printf("updated the snapshell hook in %s (older version replaced)\n", rc)
		fmt.Println("start a NEW shell/tmux pane for the hook to take effect")
		return nil
	}

	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	if _, err := fmt.Fprintf(f, "%s%s", prefix, snippet); err != nil {
		return err
	}
	fmt.Printf("appended snapshell hook to %s\n", rc)
	fmt.Println("start a NEW shell/tmux pane for the hook to take effect")
	return nil
}

// hookBlockEnd returns the byte offset just past the line that closes the
// hook block starting at start (the marker line). The snippet's structure
// guarantees the outermost `fi` sits at column 0 (inner closes are
// indented), so the first `fi` line is the block's end. Returns -1 if the
// block is unterminated.
func hookBlockEnd(text string, start int) int {
	idx := start
	for {
		nl := strings.IndexByte(text[idx:], '\n')
		if nl < 0 {
			return -1
		}
		if line := text[idx : idx+nl]; line == "fi" {
			return idx + nl + 1
		}
		idx += nl + 1
	}
}
