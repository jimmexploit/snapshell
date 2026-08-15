package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"snapshell/internal/daemon"
)

// ANSI color helpers. Colors are only emitted when stdout is a terminal.
const (
	colReset   = "\033[0m"
	colRed     = "\033[31m"
	colGreen   = "\033[32m"
	colYellow  = "\033[33m"
	colCyan    = "\033[36m"
	colMagenta = "\033[35m"
	colDim     = "\033[2m"
)

// followDaemonLog tails the daemon log from the current end and prints each
// new line, colorized by what it describes. It blocks until the daemon log
// goes away (daemon stopped) or the process is interrupted (Ctrl+C — which
// must NOT stop the session, only detach this view).
func followDaemonLog(sessionName string) error {
	if !isTerminal(os.Stdout) {
		fmt.Fprintf(os.Stdout, "documenting session %q — Ctrl+C to detach\n", sessionName)
	} else {
		fmt.Fprintf(os.Stdout, "%sdocumenting session %q%s — %sCtrl+C%s to detach\n",
			colGreen, sessionName, colReset, colDim, colReset)
	}

	f, err := os.Open(daemon.LogPath())
	if err != nil {
		return fmt.Errorf("open daemon log %s: %w", daemon.LogPath(), err)
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek daemon log: %w", err)
	}

	rd := bufio.NewReader(f)
	carry := ""
	for {
		line, err := rd.ReadString('\n')
		if line != "" {
			if strings.HasSuffix(line, "\n") {
				printLine(carry + line)
				carry = ""
			} else {
				carry += line
			}
		}
		if err != nil {
			if err == io.EOF {
				// Log went away (daemon stopped): detach cleanly.
				if _, statErr := os.Stat(daemon.LogPath()); os.IsNotExist(statErr) {
					fmt.Fprintln(os.Stdout, "daemon stopped — detached")
					return nil
				}
				time.Sleep(250 * time.Millisecond)
				continue
			}
			return fmt.Errorf("read daemon log: %w", err)
		}
	}
}

// printLine colorizes one daemon log line by what it describes and prints
// it to stdout.
func printLine(line string) {
	if !isTerminal(os.Stdout) {
		fmt.Fprint(os.Stdout, line)
		return
	}
	fmt.Fprint(os.Stdout, colorize(line))
}

// colorize maps a daemon log line to an ANSI color by content.
func colorize(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "error"), strings.Contains(lower, "failed"),
		strings.Contains(lower, "not found"), strings.Contains(lower, "cannot"),
		strings.Contains(lower, "refusing"):
		return colRed + line + colReset
	case strings.Contains(lower, "hotkey: screenshot"), strings.Contains(lower, "capture screenshot"):
		return colYellow + line + colReset
	case strings.Contains(lower, "hotkey: code"), strings.Contains(lower, "capture tmux"):
		return colCyan + line + colReset
	case strings.Contains(lower, "hotkey: note"), strings.Contains(lower, "capture note"):
		return colMagenta + line + colReset
	case strings.Contains(lower, "session started"), strings.Contains(lower, "session stopped"),
		strings.Contains(lower, "resumed"), strings.Contains(lower, "daemon started"),
		strings.Contains(lower, "daemon stopped"):
		return colGreen + line + colReset
	case strings.Contains(lower, "request:"):
		return colDim + line + colReset
	default:
		return line
	}
}

// isTerminal reports whether f is a character device (i.e. a real
// terminal, not a pipe or file).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
