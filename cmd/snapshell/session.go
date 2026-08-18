package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"snapshell/internal/daemon"
)

func newStartCmd() *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:   "start [inventory] <name>",
		Short: "Begin (or resume) a session named <name>",
		Args: func(c *cobra.Command, args []string) error {
			switch len(args) {
			case 1:
				return nil
			case 2:
				if args[0] != "inventory" {
					return fmt.Errorf("unknown mode %q — use 'snapshell start <name>' or 'snapshell start inventory <name>'", args[0])
				}
				return nil
			default:
				return fmt.Errorf("accepts 1 or 2 arg(s), received %d", len(args))
			}
		},
		RunE: func(c *cobra.Command, args []string) error {
			// First run: walk the user through a one-time setup before the
			// session begins. Only when stdin is a real terminal — never in
			// scripts/pipes, where the interactive wizard would hang.
			if !configExists() && isTTY(os.Stdin) {
				fmt.Println("First run — snapshell needs a one-time setup before we begin.")
				if err := runSetup(os.Stdin); err != nil {
					return err
				}
			}

			name := args[0]
			mode := ""
			if len(args) == 2 {
				mode = "inventory"
				name = args[1]
			}

			// No need for the user to `daemon start` first — bring it up in
			// the background if it isn't running.
			if err := ensureDaemonStarted(); err != nil {
				return err
			}
			req := daemon.Request{Cmd: daemon.CmdStart, Args: map[string]string{"name": name}}
			if mode != "" {
				req.Args["mode"] = mode
			}
			resp, err := sendRequest(req)
			if err != nil {
				return err
			}
			c.OutOrStdout().Write([]byte(resp.Message + "\n"))

			if mode == "inventory" {
				// Opening the session ends in the review TUI, foreground in
				// this terminal. Quitting it returns here without stopping
				// the session.
				return runTUI()
			}
			if verbose {
				// Stay attached and document captures live. Ctrl+C detaches
				// the view without stopping the session.
				return followDaemonLog(name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "stay attached and document captures in real time (Ctrl+C detaches)")
	return cmd
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
