#!/usr/bin/env bash
#
# Undo tools/install.sh: remove the per-user binaries and the installed
# documentation.
#
#     tools/uninstall.sh                    # whatever install.sh created
#     sudo tools/uninstall.sh --with-udev   # also the udev rule in /etc
#
# A system-wide install made from the .deb (see `make package`) is a dpkg
# package rather than a copy of these files, so it is removed as one:
#
#     sudo tools/uninstall.sh --with-package
#
# The udev rule and the package are system files, so they need root and are
# never touched unless the matching flag is passed.
set -euo pipefail

# Under sudo, HOME is root's, so every path below would point at /root and this
# script would quietly remove nothing at all. Take the invoking user's home
# instead, the same way tools/install-udev.sh resolves the desktop user.
TARGET_HOME="${HOME}"
if [[ -n "${SUDO_USER:-}" ]] && command -v getent >/dev/null 2>&1; then
    resolved="$(getent passwd "${SUDO_USER}" | cut -d: -f6)"
    if [[ -n "${resolved}" ]]; then
        TARGET_HOME="${resolved}"
    fi
fi

BIN_DIR="${TARGET_HOME}/.local/bin"
DOC_DIR="${TARGET_HOME}/.local/share/doc/attackshark-x11"
RULE="/etc/udev/rules.d/99-attackshark-x11.rules"
SYMLINK="/dev/attackshark-x11"

# The system-wide install, as packaged by `make package`.
PACKAGE_NAME="attackshark-x11"
PACKAGE_BIN="/usr/bin/attackshark-x11-tray"

WITH_UDEV=0
WITH_PACKAGE=0

usage() {
    cat <<'EOF'
usage: tools/uninstall.sh [--with-udev] [--with-package]

Removes what tools/install.sh installed for the current user:

  ~/.local/bin/attackshark-x11-tray
  ~/.local/bin/attackshark-probe
  ~/.local/share/doc/attackshark-x11/

--with-udev     also remove the udev rule from /etc. Needs root: run it through
                sudo. The /dev symlink goes with it on the next udev reload.
--with-package  also purge a system-wide install made from the .deb that
                `make package` builds. Needs root. Off by default so that this
                script never touches a package unless it is asked to.

The systemd unit, if you copied systemd/attackshark-x11-tray.service into
~/.config/systemd/user, is not touched:

    systemctl --user disable --now attackshark-x11-tray.service
    rm ~/.config/systemd/user/attackshark-x11-tray.service
EOF
}

for arg in "$@"; do
    case "${arg}" in
        --with-udev) WITH_UDEV=1 ;;
        --with-package) WITH_PACKAGE=1 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "error: unknown argument '${arg}'" >&2; usage >&2; exit 2 ;;
    esac
done

# True when dpkg knows the package and considers it installed, which is not the
# same as the files existing: a removed-but-not-purged package is not installed,
# yet still owns its conffiles.
package_installed() {
    command -v dpkg-query >/dev/null 2>&1 || return 1
    [[ "$(dpkg-query -W -f='${Status}' "${PACKAGE_NAME}" 2>/dev/null || true)" == "install ok installed" ]]
}

# The two root-only flags are checked before anything is removed, so that a
# forgotten sudo leaves the installation untouched rather than half gone.
if [[ "${EUID}" -ne 0 ]]; then
    if [[ "${WITH_UDEV}" -eq 1 ]]; then
        echo "error: --with-udev removes ${RULE} and must run as root:" >&2
        echo "       sudo $0 --with-udev" >&2
        exit 1
    fi
    if [[ "${WITH_PACKAGE}" -eq 1 ]]; then
        echo "error: --with-package purges the ${PACKAGE_NAME} package and must run as root:" >&2
        echo "       sudo $0 --with-package" >&2
        exit 1
    fi
fi

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

if [[ "${TARGET_HOME}" != "${HOME}" ]]; then
    echo "note     running under sudo, so the per-user files below are taken from"
    echo "         ${TARGET_HOME} (${SUDO_USER})"
fi

remove_file "${BIN_DIR}/attackshark-x11-tray"
remove_file "${BIN_DIR}/attackshark-probe"
remove_file "${DOC_DIR}/README.md"
prune_dir "${DOC_DIR}"

# Root was already established above if this flag was given.
if [[ "${WITH_UDEV}" -eq 1 ]]; then
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

# Root was already established above if this flag was given.
if [[ "${WITH_PACKAGE}" -eq 1 ]]; then
    if package_installed; then
        # --purge rather than -r, so nothing the package owns is left behind.
        dpkg --purge "${PACKAGE_NAME}"
    else
        echo "kept     the ${PACKAGE_NAME} package (not installed)"
    fi
elif package_installed; then
    # Not removed without being asked, but worth saying: otherwise a package
    # install looks like an uninstall that did not work.
    echo "note     the ${PACKAGE_NAME} package is installed system-wide:"
    echo "           ${PACKAGE_BIN}"
    echo "         sudo $0 --with-package   removes it"
fi

echo
echo "The tray app, if it is running, keeps running until it is quit or you log"
echo "out. Quit it from its own menu, or with:"
echo
echo "    pkill -f attackshark-x11-tra[y]"
echo
echo "The pattern is bracketed so that it still matches the app while no longer"
echo "matching the shell you paste it into, whose command line holds the same text."
