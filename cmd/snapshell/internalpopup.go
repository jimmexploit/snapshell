package main

import (
	"github.com/spf13/cobra"

	"snapshell/internal/popup"
)

func newInternalPopupCmd() *cobra.Command {
	var mode string
	var file string
	var sessionDir string

	cmd := &cobra.Command{
		Use:    "internal-popup",
		Short:  "Run the caption popup TUI (invoked by the daemon)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return popup.Run(mode, file, sessionDir)
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "", "popup mode: image, code, or note")
	cmd.Flags().StringVar(&file, "file", "", "captured file path (image: relative attachment; code: temp text file)")
	cmd.Flags().StringVar(&sessionDir, "session-dir", "", "active session folder (blog.md lives here)")
	_ = cmd.MarkFlagRequired("mode")
	_ = cmd.MarkFlagRequired("session-dir")

	return cmd
}
