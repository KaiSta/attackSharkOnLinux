#!/usr/bin/env bash
#
# Undo tools/install.sh: remove the per-user binaries, the autostart entry and
# the installed documentation.
#
# Your DPI values and the remembered DPI stage are kept unless --purge is given,
# because they describe your mouse and nothing can regenerate them.
#
#     tools/uninstall.sh                    # whatever install.sh created
#     tools/uninstall.sh --purge            # also dpi.conf and the saved stage
#     sudo tools/uninstall.sh --with-udev   # also the udev rule in /etc
#
# The udev rule is a system file, so it needs root and is never touched unless
# --with-udev is passed: --purge on its own stays inside your home directory.
set -euo pipefail

BIN_DIR="${HOME}/.local/bin"
AUTOSTART_DIR="${HOME}/.config/autostart"
DOC_DIR="${HOME}/.local/share/doc/attackshark-x11"
CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/attackshark-x11"
STATE_DIR="${XDG_STATE_HOME:-${HOME}/.local/state}/attackshark-x11"
DESKTOP_NAME="attackshark-x11-tray.desktop"
RULE="/etc/udev/rules.d/99-attackshark-x11.rules"
SYMLINK="/dev/attackshark-x11"

PURGE=0
WITH_UDEV=0

usage() {
    cat <<'EOF'
usage: tools/uninstall.sh [--purge] [--with-udev]

Removes what tools/install.sh installed for the current user:

  ~/.local/bin/attackshark-x11-tray
  ~/.local/bin/attackshark-probe
  ~/.config/autostart/attackshark-x11-tray.desktop
  ~/.local/share/doc/attackshark-x11/

--purge      also remove ~/.config/attackshark-x11/dpi.conf and the saved DPI
             stage. Both describe your mouse; nothing can regenerate them.
--with-udev  also remove the udev rule from /etc. Needs root: run it through
             sudo. The /dev symlink goes with it on the next udev reload.

The systemd unit, if you copied systemd/attackshark-x11-tray.service into
~/.config/systemd/user, is not touched:

    systemctl --user disable --now attackshark-x11-tray.service
    rm ~/.config/systemd/user/attackshark-x11-tray.service
EOF
}

for arg in "$@"; do
    case "${arg}" in
        --purge) PURGE=1 ;;
        --with-udev) WITH_UDEV=1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "error: unknown argument '${arg}'" >&2; usage >&2; exit 2 ;;
    esac
done

remove_file() {
    if [[ -e "${1}" ]]; then
        rm -f "${1}"
        echo "removed  ${1}"
    fi
}

# Only ever removes a directory that is already empty, so nothing unexpected can
# be swept up with it.
prune_dir() {
    if [[ -d "${1}" ]] && [[ -z "$(ls -A "${1}")" ]]; then
        rmdir "${1}"
        echo "removed  ${1}/"
    fi
}

remove_file "${BIN_DIR}/attackshark-x11-tray"
remove_file "${BIN_DIR}/attackshark-probe"
remove_file "${AUTOSTART_DIR}/${DESKTOP_NAME}"
remove_file "${DOC_DIR}/README.md"
prune_dir "${DOC_DIR}"

if [[ "${PURGE}" -eq 1 ]]; then
    remove_file "${CONFIG_DIR}/dpi.conf"
    prune_dir "${CONFIG_DIR}"
    remove_file "${STATE_DIR}/stage"
    prune_dir "${STATE_DIR}"
else
    kept=0
    for path in "${CONFIG_DIR}/dpi.conf" "${STATE_DIR}/stage"; do
        if [[ -e "${path}" ]]; then
            echo "kept     ${path}"
            kept=1
        fi
    done
    if [[ "${kept}" -eq 1 ]]; then
        echo "         (--purge removes these as well)"
    fi
fi

if [[ "${WITH_UDEV}" -eq 1 ]]; then
    if [[ "${EUID}" -ne 0 ]]; then
        echo "error: --with-udev removes ${RULE} and must run as root:" >&2
        echo "       sudo $0 --with-udev" >&2
        exit 1
    fi
    if [[ -e "${RULE}" ]]; then
        rm -f "${RULE}"
        echo "removed  ${RULE}"
        if command -v udevadm >/dev/null 2>&1; then
            udevadm control --reload-rules
            # The same action install-udev.sh uses: a plain trigger defaults to
            # "change", which does not re-evaluate the nodes the rule matched.
            udevadm trigger --action=add --subsystem-match=hidraw
            udevadm settle
            echo "         udev reloaded; ${SYMLINK} is gone"
        else
            echo "         udevadm is missing: the rule takes effect at the next boot"
        fi
    else
        echo "kept     ${RULE} (not installed)"
    fi
fi

echo
echo "The tray app, if it is running, keeps running until it is quit or you log"
echo "out. Quit it from its own menu, or: pkill -f attackshark-x11-tray"
