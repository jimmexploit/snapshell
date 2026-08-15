package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"snapshell/internal/daemon"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the snapshell background daemon",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start the snapshell background daemon (foreground)",
			RunE: func(c *cobra.Command, args []string) error {
				return runDaemonStart()
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Tell a running daemon to shut down cleanly",
			RunE: func(c *cobra.Command, args []string) error {
				return runDaemonStop()
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show whether the daemon is running and its PID",
			RunE: func(c *cobra.Command, args []string) error {
				return runDaemonStatus()
			},
		},
	)

	return cmd
}

func runDaemonStart() error {
	fmt.Println("starting snapshell daemon...")
	if err := daemon.Run(daemon.Options{}); err != nil {
		return fmt.Errorf("daemon: %w", err)
	}
	return nil
}

func runDaemonStop() error {
	resp, err := sendRequest(daemon.Request{Cmd: daemon.CmdDaemonStop})
	if err != nil {
		return err
	}
	fmt.Println(resp.Message)
	return nil
}

func runDaemonStatus() error {
	resp, err := sendRequest(daemon.Request{Cmd: daemon.CmdStatus})
	if err != nil {
		return err
	}
	fmt.Println(resp.Message)
	return nil
}
