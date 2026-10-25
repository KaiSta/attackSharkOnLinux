#!/usr/bin/env bash
#
# Build the tray app and the probe, and install both for the current user, with
# the documentation. Nothing starts the app with the session: that is the user's
# own choice, made in their desktop's startup settings or with the systemd unit
# in systemd/.
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
DOC_DIR="${HOME}/.local/share/doc/attackshark-x11"
RULE="/etc/udev/rules.d/99-attackshark-x11.rules"
TRAY="${BIN_DIR}/attackshark-x11-tray"
PROBE="${BIN_DIR}/attackshark-probe"

usage() {
    cat <<'EOF'
usage: tools/install.sh [--help]

Builds and installs, for the current user only:

  ~/.local/bin/attackshark-x11-tray                  the tray app
  ~/.local/bin/attackshark-probe                     the diagnostic tool
  ~/.local/share/doc/attackshark-x11/README.md       documentation

No autostart entry is written. Start the tray from your desktop's startup
applications, or copy systemd/attackshark-x11-tray.service into
~/.config/systemd/user.

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

# Referenced by Documentation= in systemd/attackshark-x11-tray.service.
mkdir -p "${DOC_DIR}"
install -m 0644 README.md "${DOC_DIR}/README.md"
echo "installed docs      ${DOC_DIR}/README.md"

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
echo "  ${PROBE} doctor        # checks udev, device access and the session"
echo "  ${TRAY}   # start it now; nothing starts it with the session"
echo
echo "The tray reads the per-stage DPI values, the stage the mouse has selected"
echo "and its polling rate when it starts, using the firmware's 0xA0 permission"
echo "request. If that read fails, the stage shows as \"stage N of 6\" with no"
echo "invented number. Pick a stage from the tray's DPI stages menu, a value from"
echo "its DPI value menu, or a rate from its Polling rate menu, to change the mouse."
