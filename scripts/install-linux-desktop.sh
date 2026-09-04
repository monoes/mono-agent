#!/usr/bin/env bash
# Register the MonoAgent GUI with the Linux desktop (app launcher, dock icon).
#
# Wails builds a bare ELF binary — it does not emit a .desktop entry or install
# an icon, so on GNOME/COSMIC/KDE the app shows a generic placeholder in the
# dock and never appears in the applications menu. This installs both, per-user
# (no sudo). Pass the binary path if it is not already on PATH:
#
#   scripts/install-linux-desktop.sh [/path/to/MonoAgent]
#
# Uninstall:
#   rm ~/.local/share/applications/monoagent.desktop \
#      ~/.local/share/icons/hicolor/256x256/apps/monoagent.png
#   update-desktop-database ~/.local/share/applications

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
icon_src="${repo_root}/wails-app/build/appicon.png"

# Resolve the binary: explicit argument, then the usual build outputs, then PATH.
if [ $# -ge 1 ]; then
  bin="$1"
else
  for candidate in \
    "${repo_root}/bin/MonoAgent" \
    "${repo_root}/wails-app/build/bin/monoagent-ui" \
    "$(command -v MonoAgent 2>/dev/null || true)" \
    "$(command -v monoagent-ui 2>/dev/null || true)"
  do
    [ -n "${candidate}" ] && [ -x "${candidate}" ] && bin="${candidate}" && break
  done
fi

if [ -z "${bin:-}" ]; then
  echo "error: MonoAgent binary not found — build it with 'make build-app', or pass its path" >&2
  exit 1
fi
if [ ! -f "${icon_src}" ]; then
  echo "error: icon not found at ${icon_src}" >&2
  exit 1
fi

bin="$(cd "$(dirname "${bin}")" && pwd)/$(basename "${bin}")"

data_home="${XDG_DATA_HOME:-${HOME}/.local/share}"
apps_dir="${data_home}/applications"
theme_dir="${data_home}/icons/hicolor"
bin_dir="${HOME}/.local/bin"
mkdir -p "${apps_dir}" "${theme_dir}/256x256/apps" "${bin_dir}"

# Icon referenced by name, not path, so the theme engine picks the best size.
# Install every size we actually ship rather than only 256 — some shells pick
# the icon by exact size match before falling back to scaling.
cp "${icon_src}" "${theme_dir}/256x256/apps/monoagent.png"
if [ -f "${repo_root}/wails-app/build/appicon@2x.png" ]; then
  mkdir -p "${theme_dir}/512x512/apps"
  cp "${repo_root}/wails-app/build/appicon@2x.png" "${theme_dir}/512x512/apps/monoagent.png"
fi

# Launch through a stable "monoagent" name. On Wayland there is no per-window
# icon protocol, so the icon comes solely from matching the window's app_id to
# this desktop entry — and GTK derives app_id from basename(argv[0]) unless
# linux.Options.ProgramName overrides it. Our artifacts have three different
# basenames (monoagent-ui, MonoAgent, MonoAgent-linux-amd64), so exec via a
# fixed-name symlink: then app_id is "monoagent" even on a build that predates
# the ProgramName option.
ln -sfn "${bin}" "${bin_dir}/monoagent"
exec_path="${bin_dir}/monoagent"

# StartupWMClass must equal the window's WM_CLASS/app_id for the running window
# to bind to this entry rather than spawning a second, iconless dock item. Wails
# sets it from linux.Options.ProgramName in wails-app/main.go — keep the two in
# sync if either changes.
cat > "${apps_dir}/monoagent.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Version=1.0
Name=Mono Agent
GenericName=Workflow Automation
Comment=Local-first workflow automation dashboard
Exec=${exec_path}
Icon=monoagent
Terminal=false
Categories=Development;
Keywords=workflow;automation;agent;
StartupNotify=true
StartupWMClass=monoagent
DESKTOP

chmod 644 "${apps_dir}/monoagent.desktop"

# Refresh caches where the tools exist; harmless if absent.
command -v update-desktop-database >/dev/null && update-desktop-database "${apps_dir}" || true
command -v gtk-update-icon-cache >/dev/null && \
  gtk-update-icon-cache -f -t "${theme_dir}" >/dev/null 2>&1 || true

echo "Installed desktop entry -> ${apps_dir}/monoagent.desktop"
echo "Installed icon          -> ${theme_dir}/256x256/apps/monoagent.png"
echo "Launch symlink          -> ${exec_path} -> ${bin}"
echo
echo "Desktop shells cache the entry list at startup, so a newly added entry"
echo "usually needs the launcher restarted before it appears:"
echo
echo "  GNOME (Xorg) : Alt+F2, type 'r', Enter"
echo "  COSMIC       : pkill cosmic-app-library cosmic-launcher   # they respawn"
echo "  KDE          : kbuildsycoca6 --noincremental"
echo "  any shell    : log out and back in"
echo
echo "Then quit any running Mono Agent window and relaunch it from the menu —"
echo "the window's app_id is fixed at map time, so an already-open window keeps"
echo "whatever identity it started with."
