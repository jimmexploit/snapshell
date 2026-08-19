package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"snapshell/internal/config"
	"snapshell/internal/shellhook"
)

// dependency is an external binary snapshell relies on, mapped to the apt
// package that provides it. The screenshot entry has two options
// (flameshot / mate-screenshot) of which only one is needed.
type dependency struct {
	bin string
	pkg string
	// optional tools are not installed by the setup wizard (and missing ones
	// are reported as "recommended", not "required"): each powers a
	// non-core feature that degrades gracefully when absent.
	optional bool
}

// Required tools: without these the core flows break and the error messages
// name them anyway, so the wizard offers to install them.
var requiredDeps = []dependency{
	{bin: "flameshot", pkg: "flameshot"},
	{bin: "mate-screenshot", pkg: "mate-utils"},
	{bin: "zenity", pkg: "zenity"},
	{bin: "notify-send", pkg: "libnotify-bin"},
	{bin: "xclip", pkg: "xclip"},
}

// Optional tools: each unlocks a non-core feature and is only reported, so
// the user decides whether to install it.
var optionalDeps = []dependency{
	// tmux: full command+output capture for Alt+2 (everything else works
	// without it; Alt+2 then captures just the command line).
	{bin: "tmux", pkg: "tmux", optional: true},
	// kitty: in-terminal rendering of screenshots (review TUI + "view blog")
	// and full-output capture of commands typed in a plain kitty window.
	{bin: "kitty", pkg: "kitty", optional: true},
	// xdotool: positioning the caption popup at a configured [popup].position.
	{bin: "xdotool", pkg: "xdotool", optional: true},
	// wmctrl: reliably closing the external image viewer after a peek.
	{bin: "wmctrl", pkg: "wmctrl", optional: true},
	// fc-list (fontconfig): listing fonts for [popup].font via list-fonts.
	{bin: "fc-list", pkg: "fontconfig", optional: true},
}

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "First-time setup: dependencies, shell hook, config",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return runSetup(c.InOrStdin())
		},
	}
}

// runSetup walks the user through the one-time setup: dependencies, the
// shell hook, and the config file. Everything is a Y/n question so the
// user stays in control.
func runSetup(r io.Reader) error {
	br := bufio.NewReader(r)

	fmt.Println("snapshell first-time setup")
	fmt.Println("This only needs to happen once. Re-run it any time with `snapshell setup`.")

	// 1. Dependencies.
	missing := missingDeps(exec.LookPath, requiredDeps)
	if len(missing) == 0 {
		fmt.Println("\n[1/3] Dependencies: all required tools are already installed.")
	} else {
		names := make([]string, 0, len(missing))
		pkgs := make([]string, 0, len(missing))
		seen := map[string]bool{}
		for _, d := range missing {
			names = append(names, d.bin)
			if !seen[d.pkg] {
				seen[d.pkg] = true
				pkgs = append(pkgs, d.pkg)
			}
		}
		fmt.Printf("\n[1/3] Dependencies: missing %s (apt packages: %s).\n",
			strings.Join(names, ", "), strings.Join(pkgs, ", "))
		yes, err := ask(br, "Install them with apt (this will use sudo)?", true)
		if err != nil {
			return err
		}
		if yes {
			if err := installDeps(pkgs); err != nil {
				return err
			}
		} else {
			fmt.Println(" Skipping. Install them later and re-run `snapshell setup`.")
		}
	}

	// Optional tools are reported, never installed behind the user's back.
	var optionalMissing []string
	for _, d := range optionalDeps {
		if _, err := exec.LookPath(d.bin); err != nil {
			optionalMissing = append(optionalMissing, fmt.Sprintf("%s (%s)", d.bin, d.pkg))
		}
	}
	if len(optionalMissing) > 0 {
		fmt.Printf("\n[1/3] Optional tools not found (each unlocks a bonus feature, install if you want it): %s.\n",
			strings.Join(optionalMissing, ", "))
	} else {
		fmt.Println("\n[1/3] Optional tools: all present.")
	}

	// 2. Shell hook (needed for Alt+2 command capture).
	shell := currentShell()
	if shell == "" {
		fmt.Println("\n[2/3] Shell hook: couldn't detect a supported shell (bash/zsh) from $SHELL — skipping.")
	} else {
		fmt.Println("\n[2/3] Shell hook: needed for Alt+2 to capture your commands.")
		rc, err := shellhook.RcFile(shell)
		if err != nil {
			return err
		}
		yes, err := ask(br, fmt.Sprintf("Add the snapshell shell hook to %s?", rc), true)
		if err != nil {
			return err
		}
		if yes {
			if err := installHook(shell); err != nil {
				return err
			}
		} else {
			fmt.Println(" Skipping. Re-run `snapshell setup` any time to add it later.")
		}
	}

	// 3. Config file. When one already exists, offer to reset it to
	// defaults (backing the old one up); otherwise point the user at it.
	path, err := config.ConfigPath()
	if err != nil {
		return err
	}
	fmt.Printf("\n[3/3] Config: snapshell settings live in %s.\n", path)
	if configExists() {
		yes, err := ask(br, "Reset it to default settings? (your current config will be backed up)", false)
		if err != nil {
			return err
		}
		if yes {
			if err := config.ResetDefault(); err != nil {
				return err
			}
			fmt.Printf(" Reset to defaults. Backup saved at %s.bak\n", path)
		} else {
			fmt.Printf(" Keeping your current config — edit %s to change tools, sizes, and hotkeys.\n", path)
		}
	} else {
		yes, err := ask(br, "Create the default config file now?", true)
		if err != nil {
			return err
		}
		if yes {
			if _, err := config.Load(); err != nil {
				return err
			}
			fmt.Printf(" Created %s\n", path)
		} else {
			fmt.Printf(" Skipping — snapshell will create it automatically on first use.\n")
		}
	}

	fmt.Println("\nSetup finished. If you added the shell hook, start a NEW shell/tmux pane")
	fmt.Println("for it to take effect, then run `snapshell start <name>`.")
	return nil
}

// ask poses a Y/n question and returns the parsed answer. An empty answer
// (just Enter) takes the default. Invalid input re-prompts. A single
// bufio.Reader is shared across questions so re-prompting never loses
// buffered input.
func ask(br *bufio.Reader, prompt string, def bool) (bool, error) {
	mark := "[y/N]"
	if def {
		mark = "[Y/n]"
	}
	fmt.Printf("%s %s ", prompt, mark)

	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	if err == io.EOF && line == "" {
		return def, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	case "":
		return def, nil
	default:
		fmt.Println("  Please answer y or n.")
		return ask(br, prompt, def)
	}
}

// missingDeps returns the dependencies not found on PATH. Only one
// screenshot tool is required, so a missing fallback is not reported when
// the other is installed.
func missingDeps(lookup func(string) (string, error), deps []dependency) []dependency {
	haveScreenshot := false
	for _, d := range deps {
		if isScreenshotDep(d) {
			if _, err := lookup(d.bin); err == nil {
				haveScreenshot = true
			}
		}
	}
	var missing []dependency
	for _, d := range deps {
		if _, err := lookup(d.bin); err != nil {
			if isScreenshotDep(d) && haveScreenshot {
				continue
			}
			missing = append(missing, d)
		}
	}
	return missing
}

func isScreenshotDep(d dependency) bool {
	return d.bin == "flameshot" || d.bin == "mate-screenshot"
}

// installDeps runs sudo apt-get install for the given packages, streaming
// through to the user's terminal (sudo may prompt for a password).
func installDeps(pkgs []string) error {
	if len(pkgs) == 0 {
		return nil
	}
	for _, bin := range []string{"sudo", "apt-get"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s not found on PATH — install manually: apt-get install -y %s", bin, strings.Join(pkgs, " "))
		}
	}
	cmd := exec.Command("sudo", append([]string{"apt-get", "install", "-y"}, pkgs...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dependency install failed: %v", err)
	}
	fmt.Println("  Dependencies installed.")
	return nil
}

// currentShell detects the user's login shell from $SHELL ("bash" or
// "zsh"); returns "" for anything else.
func currentShell() string {
	switch {
	case strings.HasSuffix(os.Getenv("SHELL"), "bash"):
		return "bash"
	case strings.HasSuffix(os.Getenv("SHELL"), "zsh"):
		return "zsh"
	default:
		return ""
	}
}

// configExists reports whether the config file already exists on disk.
func configExists() bool {
	p, err := config.ConfigPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// isTTY reports whether f is a terminal (used to avoid running the
// interactive first-run wizard when stdin is a pipe/script).
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
