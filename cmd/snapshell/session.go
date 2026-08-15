package main

import (
	"github.com/spf13/cobra"

	"snapshell/internal/daemon"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <name>",
		Short: "Begin (or resume) a session named <name>",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			resp, err := sendRequest(daemon.Request{
				Cmd:  daemon.CmdStart,
				Args: map[string]string{"name": args[0]},
			})
			if err != nil {
				return err
			}
			c.OutOrStdout().Write([]byte(resp.Message + "\n"))
			return nil
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "End the active session",
		RunE: func(c *cobra.Command, args []string) error {
			resp, err := sendRequest(daemon.Request{Cmd: daemon.CmdStop})
			if err != nil {
				return err
			}
			c.OutOrStdout().Write([]byte(resp.Message + "\n"))
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the active session name and item count",
		RunE: func(c *cobra.Command, args []string) error {
			resp, err := sendRequest(daemon.Request{Cmd: daemon.CmdStatus})
			if err != nil {
				return err
			}
			c.OutOrStdout().Write([]byte(resp.Message + "\n"))
			return nil
		},
	}
}
