package shellhook

import (
	"fmt"
	"os"
	"path/filepath"
)

// BashSnippet returns the lines to add to ~/.bashrc so every completed
// command is ready for capture. Inside tmux it records row markers (full
// output capture); outside tmux it records the command text so Alt+2 can
// still capture the command line.
//
// It registers through bash-preexec's hook arrays when bash-preexec is
// loaded (avoids fighting over the DEBUG trap); otherwise it falls back to
// the classic DEBUG trap + PROMPT_COMMAND pattern, chaining onto any
// existing DEBUG trap instead of clobbering it.
const BashSnippet = `# --- snapshell shell integration ---
# add this near the end of your .bashrc, then start a NEW shell/tmux pane
if command -v snapshell >/dev/null 2>&1; then
  _snapshell_sourcing=1
  if [ -n "$TMUX" ]; then
    # Inside tmux: record row markers for full prompt+output capture, and
    # the command text (at end, once it completed) for the session history.
    # $1 is the exit status passed by _snapshell_precmd_end (captured
    # before anything could clobber it), used for auto mode.
    _snapshell_mark_start() { _SNAPSHELL_TEXT="$1"; snapshell _hook-mark --pane "$TMUX_PANE" --phase start --prev-end "${_SNAPSHELL_PREV_END:-}"; }
    _snapshell_mark_end()   { local _SNAPSHELL_EXIT="${1:-0}"; _SNAPSHELL_PREV_END="$(snapshell _hook-mark --pane "$TMUX_PANE" --phase end)"; snapshell _hook-record --source "$TMUX_PANE" --exit-code "$_SNAPSHELL_EXIT" --text "${_SNAPSHELL_TEXT:-}"; unset _SNAPSHELL_TEXT; }
  else
    # No tmux: plain terminal. Inside kitty, enable kitty's shell
    # integration (prompt marks) so Alt+2 can read the command's output
    # back from the window's scrollback; the window id + listen socket are
    # recorded with the command text for that lookup.
    if [ -n "${KITTY_WINDOW_ID:-}" ] && [ -z "${KITTY_SHELL_INTEGRATION:-}" ] && [ -r /usr/lib/kitty/shell-integration/bash/kitty.bash ]; then
      export KITTY_SHELL_INTEGRATION=enabled
      source /usr/lib/kitty/shell-integration/bash/kitty.bash
    fi
    _snapshell_mark_start() { _SNAPSHELL_TEXT="$1"; _SNAPSHELL_KITTY_WID="${KITTY_WINDOW_ID:-}"; _SNAPSHELL_KITTY_LISTEN="${KITTY_LISTEN_ON:-}"; }
    _snapshell_mark_end()   { local _SNAPSHELL_EXIT="${1:-0}"; snapshell _hook-record --source "$(tty 2>/dev/null)" --kitty-window "${_SNAPSHELL_KITTY_WID:-}" --kitty-listen "${_SNAPSHELL_KITTY_LISTEN:-}" --exit-code "$_SNAPSHELL_EXIT" --text "${_SNAPSHELL_TEXT:-}"; unset _SNAPSHELL_TEXT _SNAPSHELL_KITTY_WID _SNAPSHELL_KITTY_LISTEN; }
  fi

  _snapshell_preexec() {
    # While .bashrc is still being sourced, the DEBUG trap fires on the
    # hook's own setup lines (e.g. 'unset _snapshell_old_debug ...') — those
    # must never be recorded as user commands.
    [ -n "${_snapshell_sourcing:-}" ] && return 0
    # Record the start row once per command line; later DEBUG events inside
    # compound commands must not overwrite it with a later row.
    [ -n "${_SNAPSHELL_STARTED:-}" ] && return 0
    _SNAPSHELL_STARTED=1
    _snapshell_mark_start "$1"
  }

  _snapshell_precmd_end() {
    # Capture the command's exit status before anything else can change it
    # (the guard below would reset it). Under bash-preexec the reliable
    # value is __bp_last_ret_status, which it snapshots before invoking the
    # precmd hooks; standalone, $? still holds the user command's status.
    local _SNAPSHELL_EXIT=$?
    [ -n "${_SNAPSHELL_STARTED:-}" ] || return 0
    [ -z "${__bp_last_ret_status:-}" ] || _SNAPSHELL_EXIT="${__bp_last_ret_status}"
    _snapshell_mark_end "$_SNAPSHELL_EXIT"
    unset _SNAPSHELL_STARTED
  }

  if declare -p __bp_preexec_functions >/dev/null 2>&1; then
    # bash-preexec is loaded: hook into its arrays, don't touch DEBUG.
    __bp_preexec_functions+=("_snapshell_preexec")
    __bp_precmd_functions+=("_snapshell_precmd_end")
  else
    # Standalone: DEBUG trap (chained onto any existing one) + PROMPT_COMMAND.
    _snapshell_debug_chain() {
      [ -n "${_snapshell_sourcing:-}" ] && return 0
      [ -z "$COMP_LINE" ] || return 0
      case "$BASH_COMMAND" in
        ""|:|_snapshell_*|__bp_*|builtin\ *) return 0 ;;
      esac
      # PROMPT_COMMAND from other frameworks (bash-preexec, starship, ...)
      # invokes functions we must not treat as user commands.
      declare -F "$BASH_COMMAND" >/dev/null 2>&1 && return 0
      [ -n "${_SNAPSHELL_STARTED:-}" ] && return 0
      _SNAPSHELL_STARTED=1
      _snapshell_mark_start "$BASH_COMMAND"
    }
    _snapshell_old_debug="$(trap -p DEBUG)"
    case "$_snapshell_old_debug" in
      "trap -- '"*)
        _snapshell_old_cmd="${_snapshell_old_debug#trap -- \'}"
        _snapshell_old_cmd="${_snapshell_old_cmd%\' DEBUG}"
        trap "$_snapshell_old_cmd; _snapshell_debug_chain" DEBUG
        ;;
      *)
        trap "_snapshell_debug_chain" DEBUG
        ;;
    esac
    unset _snapshell_old_debug _snapshell_old_cmd
    if [ -n "$PROMPT_COMMAND" ]; then
      PROMPT_COMMAND="_snapshell_precmd_end; $PROMPT_COMMAND"
    else
      PROMPT_COMMAND="_snapshell_precmd_end"
    fi
  fi
  unset _snapshell_sourcing
fi
`

// ZshSnippet returns the lines to add to ~/.zshrc so every completed
// command is ready for capture. Inside tmux it records row markers; outside
// tmux it records the command text. Works with or without tmux.
const ZshSnippet = `# --- snapshell shell integration ---
# add this near the end of your .zshrc, then start a NEW shell/tmux pane
if (( $+commands[snapshell] )); then
  autoload -Uz add-zsh-hook
  if [ -n "$TMUX" ]; then
    # $? is captured first thing in _snapshell_mark_end (zsh preserves the
    # last command's exit status into the precmd hook, and add-zsh-hook
    # runs this function first in the chain) — it drives auto mode.
    _snapshell_mark_start() { _SNAPSHELL_TEXT="$1"; snapshell _hook-mark --pane "$TMUX_PANE" --phase start --prev-end "${_SNAPSHELL_PREV_END:-}"; }
    _snapshell_mark_end()   { local _SNAPSHELL_EXIT=$?; _SNAPSHELL_PREV_END="$(snapshell _hook-mark --pane "$TMUX_PANE" --phase end)"; snapshell _hook-record --source "$TMUX_PANE" --exit-code "$_SNAPSHELL_EXIT" --text "${_SNAPSHELL_TEXT:-}"; unset _SNAPSHELL_TEXT; }
  else
    if [ -n "$KITTY_WINDOW_ID" ] && [ -z "$KITTY_SHELL_INTEGRATION" ] && [ -r /usr/lib/kitty/shell-integration/zsh/kitty.zsh ]; then
      export KITTY_SHELL_INTEGRATION=enabled
      source /usr/lib/kitty/shell-integration/zsh/kitty.zsh
    fi
    _snapshell_mark_start() { _SNAPSHELL_TEXT="$1"; _SNAPSHELL_KITTY_WID="${KITTY_WINDOW_ID:-}"; _SNAPSHELL_KITTY_LISTEN="${KITTY_LISTEN_ON:-}"; }
    _snapshell_mark_end()   { local _SNAPSHELL_EXIT=$?; snapshell _hook-record --source "$(tty 2>/dev/null)" --kitty-window "${_SNAPSHELL_KITTY_WID:-}" --kitty-listen "${_SNAPSHELL_KITTY_LISTEN:-}" --exit-code "$_SNAPSHELL_EXIT" --text "${_SNAPSHELL_TEXT:-}"; unset _SNAPSHELL_TEXT _SNAPSHELL_KITTY_WID _SNAPSHELL_KITTY_LISTEN; }
  fi
  add-zsh-hook preexec _snapshell_mark_start
  add-zsh-hook precmd  _snapshell_mark_end
fi
`

// RcFile returns the rc file path for the given shell ("bash" or "zsh").
func RcFile(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch shell {
	case "bash":
		return filepath.Join(home, ".bashrc"), nil
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	default:
		return "", fmt.Errorf("unknown shell %q (expected bash or zsh)", shell)
	}
}
