package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"snapshell/internal/shellhook"
)

func newShellhookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "shellhook",
		Short: "Manage the bash/zsh shell integration",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "print bash|zsh",
			Short: "Print the shell hook snippet to add to your rc file",
			Args:  cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				snippet, err := snippetFor(args[0])
				if err != nil {
					return err
				}
				fmt.Print(snippet)
				return nil
			},
		},
		&cobra.Command{
			Use:   "install bash|zsh",
			Short: "Append the shell hook snippet to your rc file",
			Args:  cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				return installHook(args[0])
			},
		},
		newMarkCmd(),
	)

	return cmd
}

func newMarkCmd() *cobra.Command {
	var pane string
	var phase string

	cmd := &cobra.Command{
		Use:   "mark",
		Short: "Record a tmux row marker (called by the shell hook)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			// Silent on failure: this runs on every command prompt and
			// must not spam the terminal when tmux isn't available.
			_ = shellhook.Mark(pane, phase)
			return nil
		},
	}
	cmd.Flags().StringVar(&pane, "pane", "", "tmux pane id (TMUX_PANE)")
	cmd.Flags().StringVar(&phase, "phase", "", "marker phase: start or end")
	_ = cmd.MarkFlagRequired("pane")
	_ = cmd.MarkFlagRequired("phase")

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
	if strings.Contains(string(data), "# --- snapshell shell integration ---") {
		return fmt.Errorf("%s already has the snapshell hook — not appending again", rc)
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
