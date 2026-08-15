#!/usr/bin/env bash
#
# snapshell setup — one-shot installer.
#
# Installs the runtime dependencies for snapshell, builds the binary,
# installs it to ~/go/bin, adds the shell hook to your rc file, and
# (optionally) registers the systemd user unit.
#
# Usage:
#   ./scripts/setup.sh                 full install
#   ./scripts/setup.sh --skip-deps     skip the system-package install
#   ./scripts/setup.sh --enable-systemd  also enable the daemon under systemd
#
# Re-running is safe: package installs are idempotent, the binary is
# overwritten, and the shell hook refuses to double-append.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PREFIX="${HOME}/go/bin"
BIN="${PREFIX}/snapshell"

usage() {
  sed -n '2,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit 0
}

SKIP_DEPS=0
ENABLE_SYSTEMD=0
for arg in "$@"; do
  case "$arg" in
    --skip-deps) SKIP_DEPS=1 ;;
    --enable-systemd) ENABLE_SYSTEMD=1 ;;
    --help|-h) usage ;;
    *) echo "unknown option: $arg"; usage ;;
  esac
done

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

warn() { printf '\033[33m[setup] %s\033[0m\n' "$*" >&2; }
info() { printf '\033[36m[setup] %s\033[0m\n' "$*"; }
die()  { printf '\033[31m[setup] %s\033[0m\n' "$*" >&2; exit 1; }

# sudo wrapper: returns the command prefix needed to run as root, or the
# empty string when already root or when non-interactive sudo isn't
# available (password prompt would hang a setup script).
maybe_sudo() {
  if [ "$(id -u)" -eq 0 ]; then
    echo ""
  elif sudo -n true 2>/dev/null; then
    echo "sudo"
  fi
}

detect_pm() {
  local id like=""
  if [ -r /etc/os-release ]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    id="${ID:-}"; like="${ID_LIKE:-}"
  fi
  case "${like} ${id}" in
    *debian*)  echo apt ;;
    *fedora*|*rhel*|*centos*) echo dnf ;;
    *arch*)    echo pacman ;;
    *suse*|*opensuse*) echo zypper ;;
    *)         echo unknown ;;
  esac
}

# ---------------------------------------------------------------------------
# 1. Go toolchain
# ---------------------------------------------------------------------------

info "checking Go toolchain..."
if ! command -v go >/dev/null 2>&1; then
  die "go is not installed. Install Go >= 1.24 (https://go.dev/dl/) then re-run this script."
fi
GO_VERSION="$(go version | sed -n 's/.*go\([0-9.]*\).*/\1/p')"
info "found go ${GO_VERSION}"

# ---------------------------------------------------------------------------
# 2. Runtime dependencies
# ---------------------------------------------------------------------------

# Install the runtime deps for the detected distro. Tolerated failure: the
# build below still runs, and the script lists what's missing — snapshell
# degrades gracefully without any given tool, so a missing package must
# not abort setup.
install_deps() {
  local PM SUDO
  PM="$(detect_pm)"
  SUDO="$(maybe_sudo)"
  info "package manager: ${PM}"

  case "$PM" in
    apt)
      local PKGS="flameshot libnotify-bin xdotool tmux xclip kitty xterm mate-utils"
      $SUDO apt-get update || return 1
      $SUDO apt-get install -y $PKGS || return 1
      ;;
    dnf)
      local PKGS="flameshot libnotify xdotool tmux xclip kitty xterm mate-utils"
      $SUDO dnf install -y $PKGS || return 1
      ;;
    pacman)
      local PKGS="flameshot libnotify xdotool tmux xclip kitty xterm mate-utils"
      $SUDO pacman -S --needed --noconfirm $PKGS || return 1
      ;;
    zypper)
      local PKGS="flameshot libnotify xdotool tmux xclip kitty xterm mate-utils"
      $SUDO zypper --non-interactive install $PKGS || return 1
      ;;
    *)
      warn "unsupported distro — install the dependencies manually:"
      warn "flameshot (or mate-screenshot), notify-send, xdotool, tmux, xclip, kitty/xterm"
      return 0
      ;;
  esac
  return 0
}

if [ "$SKIP_DEPS" -eq 1 ]; then
  info "skipping system dependency install (--skip-deps)"
elif [ "$(maybe_sudo)" = "" ] && [ "$(id -u)" -ne 0 ]; then
  warn "no root/passwordless sudo available — skipping system dependency install."
  warn "install these yourself: flameshot/mate-screenshot, libnotify-bin (notify-send),"
  warn "xdotool, tmux, xclip, and a terminal (kitty/xterm)."
elif ! install_deps; then
  warn "system dependency install failed — install the missing tools manually:"
  warn "  flameshot/mate-screenshot, notify-send, xdotool, tmux, xclip, kitty/xterm"
fi

info "checking the tools snapshell actually needs..."
command -v flameshot >/dev/null 2>&1 || command -v mate-screenshot >/dev/null 2>&1 \
  || warn "no screenshot tool found — Alt+1 will notify an error. Install flameshot or mate-screenshot."
command -v notify-send >/dev/null 2>&1 || warn "notify-send not found — install libnotify-bin (errors will be silent)."
command -v xdotool >/dev/null 2>&1 || warn "xdotool not found — the popup window won't be centered/focused (it still opens)."
command -v tmux >/dev/null 2>&1 || warn "tmux not found — Alt+2 (command capture) needs it."
command -v kitty >/dev/null 2>&1 || command -v xterm >/dev/null 2>&1 \
  || warn "no terminal emulator found for the popup — install kitty or xterm."

# ---------------------------------------------------------------------------
# 3. Build + install the binary
# ---------------------------------------------------------------------------

info "building snapshell..."
make -C "$REPO_DIR" install

case ":${PATH}:" in
  *":${PREFIX}:"*) ;;
  *) warn "${PREFIX} is not on your PATH — the shell hook won't find snapshell."
     warn "add 'export PATH=\"\${HOME}/go/bin:\${PATH}\"' to your ~/.profile and log in again." ;;
esac

# ---------------------------------------------------------------------------
# 4. Shell hook (bash and/or zsh)
# ---------------------------------------------------------------------------

info "installing the shell hook..."
install_hook() {
  local shell="$1"
  if "$BIN" shellhook install "$shell" 2>/dev/null; then
    info "added $shell hook to your rc file — start a NEW shell/tmux pane for it to take effect"
  else
    warn "$shell hook not installed (already present, or rc file missing). Run '$BIN shellhook install $shell' to retry."
  fi
}

case "$(basename "${SHELL:-bash}")" in
  zsh) install_hook zsh; install_hook bash ;;
  *)   install_hook bash; install_hook zsh ;;
esac

# ---------------------------------------------------------------------------
# 5. systemd user unit
# ---------------------------------------------------------------------------

install_systemd() {
  local unit_dir="${HOME}/.config/systemd/user"
  mkdir -p "$unit_dir"
  cp "$REPO_DIR/systemd/snapshell.service" "$unit_dir/"
  systemctl --user daemon-reload
  systemctl --user enable --now snapshell
  info "snapshell enabled under systemd. Logs: journalctl --user -u snapshell -f"
}

if [ "$ENABLE_SYSTEMD" -eq 1 ]; then
  info "registering the systemd user unit..."
  if install_systemd 2>/dev/null; then
    : # handled above
  else
    warn "could not enable the systemd unit (is systemd --user running under this session?)."
    warn "You can still run the daemon manually: $BIN daemon start"
  fi
else
  info "systemd unit not enabled. To run the daemon under systemd:"
  info "  mkdir -p ~/.config/systemd/user && cp systemd/snapshell.service ~/.config/systemd/user/"
  info "  systemctl --user daemon-reload && systemctl --user enable --now snapshell"
fi

# ---------------------------------------------------------------------------
# 6. Summary
# ---------------------------------------------------------------------------

info ""
info "done. Next steps:"
info "  1. Start a NEW shell/tmux pane (so the shell hook takes effect)."
info "  2. Start the daemon:  $BIN daemon start"
info "  3. Begin a session:   $BIN start my-box"
info "  4. Capture:           Alt+1 screenshot | Alt+2 tmux command | Alt+3 note"