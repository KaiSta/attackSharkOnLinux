#!/usr/bin/env bash
#
# Build the tray app and the probe, and install both for the current user, with
# an autostart entry and a template at ~/.config/attackshark-x11/dpi.conf.
#
# No root is needed here. Device access is the one privileged step, and it is
# separate and one-off:
#
#     sudo tools/install-udev.sh
#
# Undo with tools/uninstall.sh.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${HOME}/.local/bin"
AUTOSTART_DIR="${HOME}/.config/autostart"
DOC_DIR="${HOME}/.local/share/doc/attackshark-x11"
RULE="/etc/udev/rules.d/99-attackshark-x11.rules"
DESKTOP_NAME="attackshark-x11-tray.desktop"
TRAY="${BIN_DIR}/attackshark-x11-tray"
PROBE="${BIN_DIR}/attackshark-probe"

usage() {
    cat <<'EOF'
usage: tools/install.sh [--help]

Builds and installs, for the current user only:

  ~/.local/bin/attackshark-x11-tray                  the tray app
  ~/.local/bin/attackshark-probe                     the diagnostic tool
  ~/.config/autostart/attackshark-x11-tray.desktop   starts it with the session
  ~/.config/attackshark-x11/dpi.conf                 template, if not there yet
  ~/.local/share/doc/attackshark-x11/README.md       documentation

No root is needed. Device access is the one privileged step, and it is separate:

    sudo tools/install-udev.sh

Undo with tools/uninstall.sh.
EOF
}

for arg in "$@"; do
    case "${arg}" in
        -h|--help) usage; exit 0 ;;
        *) echo "error: unknown argument '${arg}'" >&2; usage >&2; exit 2 ;;
    esac
done

if ! command -v go >/dev/null 2>&1; then
    echo "error: go is not on PATH, and this script builds both binaries from source" >&2
    exit 1
fi

cd "${REPO_ROOT}"
mkdir -p "${BIN_DIR}"

echo "building..."
go build -o "${TRAY}" ./cmd/attackshark-x11-tray
go build -o "${PROBE}" ./cmd/attackshark-probe

echo "installed binary    ${TRAY}"
echo "installed binary    ${PROBE}"

mkdir -p "${AUTOSTART_DIR}"
cat > "${AUTOSTART_DIR}/${DESKTOP_NAME}" <<EOF
[Desktop Entry]
Type=Application
Name=Attack Shark X11 tray
Comment=Battery level and DPI stage for the Attack Shark X11 mouse
Exec=${TRAY}
Terminal=false
NoDisplay=false
X-GNOME-Autostart-enabled=true
EOF

echo "installed autostart ${AUTOSTART_DIR}/${DESKTOP_NAME}"

# Referenced by Documentation= in systemd/attackshark-x11-tray.service.
mkdir -p "${DOC_DIR}"
install -m 0644 README.md "${DOC_DIR}/README.md"
echo "installed docs      ${DOC_DIR}/README.md"

# Annotated template for the per-stage DPI values. Never overwrites an existing
# file, because those values belong to the user's mouse.
DPI_CONF="${XDG_CONFIG_HOME:-${HOME}/.config}/attackshark-x11/dpi.conf"
mkdir -p "$(dirname "${DPI_CONF}")"
if [[ -e "${DPI_CONF}" ]]; then
    echo "kept existing       ${DPI_CONF}"
else
    install -m 0644 examples/dpi.conf "${DPI_CONF}"
    echo "installed template  ${DPI_CONF}"
fi

# The rule itself needs root, so it is not installed here. Saying so now is
# better than a permission error from the tray later.
if [[ -e "${RULE}" ]]; then
    echo "udev rule           ${RULE}"
else
    echo "udev rule           ${RULE} is MISSING"
    echo "                    sudo tools/install-udev.sh   (once, for device access)"
fi

case ":${PATH}:" in
    *":${BIN_DIR}:"*) ;;
    *)
        echo
        echo "note: ${BIN_DIR} is not on your PATH, so the commands below need their"
        echo "      full path until it is. Most desktops already add it; if not:"
        echo "          echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.profile"
        ;;
esac

echo
echo "Next:"
echo "  ${PROBE} doctor        # checks udev, device access, config and the session"
echo "  log out and back in, or run: ${TRAY}"
echo
echo "Until ${DPI_CONF} holds your six DPI values, the tray"
echo "shows the stage as \"stage N of 6\" without inventing a number to go with it."
