package main

import (
	"github.com/spf13/cobra"

	"snapshell/internal/daemon"
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

// newInternalPopupInlineCmd runs the caption form inline when the shell
// hook picks up a staged capture at a prompt (see internal/daemon pending
// capture). No args: it reads the pending request itself, runs the form,
// then clears the request. A missing/empty pending file is a silent no-op
// — this command runs on every prompt.
func newInternalPopupInlineCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "internal-popup-inline",
		Short:  "Run the staged caption form inline at the current prompt (invoked by the shell hook)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			p, ok, err := daemon.ReadPending()
			if err != nil {
				return err
			}
			if !ok {
				return nil // nothing staged — nothing to show
			}
			defer daemon.ClearPending() // consumed whether submitted or cancelled
			return popup.Run(p.Mode, p.File, p.SessionDir)
		},
	}
}
