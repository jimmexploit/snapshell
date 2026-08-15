package shellhook

import (
	"fmt"
	"os"
	"path/filepath"
)

// BashSnippet returns the lines to add to ~/.bashrc so every completed
// command records a tmux row marker (only while inside tmux).
//
// It registers through bash-preexec's hook arrays when bash-preexec is
// loaded (avoids fighting over the DEBUG trap); otherwise it falls back to
// the classic DEBUG trap + PROMPT_COMMAND pattern, chaining onto any
// existing DEBUG trap instead of clobbering it.
const BashSnippet = `# --- snapshell shell integration ---
# add this near the end of your .bashrc, then start a NEW shell/tmux pane
if [ -n "$TMUX" ] && command -v snapshell >/dev/null 2>&1; then
  _snapshell_mark_start() { snapshell shellhook mark --pane "$TMUX_PANE" --phase start --prev-end "${_SNAPSHELL_PREV_END:-}"; }
  _snapshell_mark_end()   { _SNAPSHELL_PREV_END="$(snapshell shellhook mark --pane "$TMUX_PANE" --phase end)"; }

  _snapshell_preexec() {
    # Record the start row only once per command line; later DEBUG events
    # inside compound commands must not overwrite it with a later row.
    [ -n "${_SNAPSHELL_STARTED:-}" ] && return 0
    _SNAPSHELL_STARTED=1
    _snapshell_mark_start
  }

  _snapshell_precmd_end() {
    [ -n "${_SNAPSHELL_STARTED:-}" ] || return 0
    _snapshell_mark_end
    unset _SNAPSHELL_STARTED
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
      _snapshell_mark_start
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
// command records a tmux row marker (only while inside tmux).
const ZshSnippet = `# --- snapshell shell integration ---
# add this near the end of your .zshrc, then start a NEW shell/tmux pane
if [ -n "$TMUX" ] && (( $+commands[snapshell] )); then
  autoload -Uz add-zsh-hook
  _snapshell_mark_start() { snapshell shellhook mark --pane "$TMUX_PANE" --phase start --prev-end "${_SNAPSHELL_PREV_END:-}"; }
  _snapshell_mark_end()   { _SNAPSHELL_PREV_END="$(snapshell shellhook mark --pane "$TMUX_PANE" --phase end)"; }
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
