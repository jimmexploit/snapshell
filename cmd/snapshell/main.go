package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "snapshell",
		Short: "Document HTB pentest sessions with hotkey-driven captures",
		Long: "snapshell is a background daemon + CLI that documents an HTB " +
			"pentest session in real time via global hotkeys, auto-generating a " +
			"Markdown blog post per session with embedded screenshots and " +
			"terminal-output code blocks.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// No auto-generated `completion` command — the setup wizard handles
	// shell integration and the CLI stays minimal.
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(
		newDaemonCmd(),
		newStartCmd(),
		newStopCmd(),
		newStatusCmd(),
		newSetupCmd(),
		// Hidden plumbing called by the installed shell hook snippets.
		newHookMarkCmd(),
		newHookRecordCmd(),
	)

	return root
}

// notImplemented is a placeholder body for subcommands that are parsed but
// not yet wired up. It is replaced step by step as the build order progresses.
func notImplemented() error {
	fmt.Fprintln(os.Stderr, "not implemented")
	os.Exit(1)
	return nil
}
