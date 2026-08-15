package shellhook

import (
	"fmt"
	"os"
	"path/filepath"
)

// BashSnippet returns the lines to add to ~/.bashrc so every completed
// command is ready for capture. Inside tmux it records row markers (full
// output capture); outside tmux it records the command text so Alt+2 can
// still capture the command line. The inline caption form runs at every
// prompt either way.
//
// It registers through bash-preexec's hook arrays when bash-preexec is
// loaded (avoids fighting over the DEBUG trap); otherwise it falls back to
// the classic DEBUG trap + PROMPT_COMMAND pattern, chaining onto any
// existing DEBUG trap instead of clobbering it.
const BashSnippet = `# --- snapshell shell integration ---
# add this near the end of your .bashrc, then start a NEW shell/tmux pane
if command -v snapshell >/dev/null 2>&1; then
  _snapshell_inline_popup() {
    # Run the staged caption form inline at this prompt (fzf-style).
    # Suspend marker recording while it runs so the DEBUG trap doesn't
    # treat the form as a user command.
    local prev="${_SNAPSHELL_STARTED:-}"
    _SNAPSHELL_STARTED=1
    snapshell internal-popup-inline
    _SNAPSHELL_STARTED="$prev"
  }

  if [ -n "$TMUX" ]; then
    # Inside tmux: record row markers for full prompt+output capture.
    _snapshell_mark_start() { snapshell shellhook mark --pane "$TMUX_PANE" --phase start --prev-end "${_SNAPSHELL_PREV_END:-}"; }
    _snapshell_mark_end()   { _SNAPSHELL_PREV_END="$(snapshell shellhook mark --pane "$TMUX_PANE" --phase end)"; }
  else
    # No tmux: no row markers — record the command text so Alt+2 can still
    # capture the command line (without output).
    _snapshell_mark_start() { snapshell shellhook record-command --text "$1"; }
    _snapshell_mark_end()   { :; }
  fi

  _snapshell_preexec() {
    # Record the start row once per command line; later DEBUG events inside
    # compound commands must not overwrite it with a later row.
    [ -n "${_SNAPSHELL_STARTED:-}" ] && return 0
    _SNAPSHELL_STARTED=1
    _snapshell_mark_start "$1"
  }

  _snapshell_precmd_end() {
    [ -n "${_SNAPSHELL_STARTED:-}" ] || return 0
    _snapshell_mark_end
    unset _SNAPSHELL_STARTED
    _snapshell_inline_popup
  }

  if declare -p __bp_preexec_functions >/dev/null 2>&1; then
    # bash-preexec is loaded: hook into its arrays, don't touch DEBUG.
    __bp_preexec_functions+=("_snapshell_preexec")
    __bp_precmd_functions+=("_snapshell_precmd_end")
  else
    # Standalone: DEBUG trap (chained onto any existing one) + PROMPT_COMMAND.
    _snapshell_debug_chain() {
      [ -z "$COMP_LINE" ] || return 0
      case "$BASH_COMMAND" in
        ""|_snapshell_*|__bp_*) return 0 ;;
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
fi
`

// ZshSnippet returns the lines to add to ~/.zshrc so every completed
// command is ready for capture. Inside tmux it records row markers; outside
// tmux it records the command text. Works with or without tmux.
const ZshSnippet = `# --- snapshell shell integration ---
# add this near the end of your .zshrc, then start a NEW shell/tmux pane
if (( $+commands[snapshell] )); then
  autoload -Uz add-zsh-hook
  _snapshell_inline_popup() {
    snapshell internal-popup-inline
  }
  if [ -n "$TMUX" ]; then
    _snapshell_mark_start() { snapshell shellhook mark --pane "$TMUX_PANE" --phase start --prev-end "${_SNAPSHELL_PREV_END:-}"; }
    _snapshell_mark_end()   { _SNAPSHELL_PREV_END="$(snapshell shellhook mark --pane "$TMUX_PANE" --phase end)"; }
  else
    _snapshell_mark_start() { snapshell shellhook record-command --text "$1"; }
    _snapshell_mark_end()   { :; }
  fi
  _snapshell_precmd() {
    _snapshell_mark_end
    _snapshell_inline_popup
  }
  add-zsh-hook preexec _snapshell_mark_start
  add-zsh-hook precmd  _snapshell_precmd
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
