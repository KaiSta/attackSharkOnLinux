#!/usr/bin/env bash
#
# Install the udev rule that lets the desktop user reach the Attack Shark X11
# receiver without root. Run this once:
#
#     sudo tools/install-udev.sh
#
# Undo with:  sudo rm /etc/udev/rules.d/99-attackshark-x11.rules
set -euo pipefail

RULE_NAME="99-attackshark-x11.rules"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE="${REPO_ROOT}/udev/${RULE_NAME}"
DESTINATION="/etc/udev/rules.d/${RULE_NAME}"
SYMLINK="/dev/attackshark-x11"
GROUP_NAME="plugdev"

if [[ ! -f "${SOURCE}" ]]; then
    echo "error: ${SOURCE} not found" >&2
    exit 1
fi

if [[ "${EUID}" -ne 0 ]]; then
    echo "error: this script installs a udev rule and must run as root:" >&2
    echo "       sudo $0" >&2
    exit 1
fi

# Under pkexec there is no SUDO_USER, so fall back to the session logind
# reports as active.
TARGET_USER="${SUDO_USER:-}"
if [[ -z "${TARGET_USER}" ]]; then
    TARGET_USER="$(loginctl list-sessions --no-legend 2>/dev/null | awk 'NF >= 3 { print $3; exit }')"
fi

install -m 0644 "${SOURCE}" "${DESTINATION}"
echo "installed ${DESTINATION}"

udevadm control --reload-rules
# Only hidraw is re-triggered, so no other device is disturbed. The "add"
# action is used because a plain "udevadm trigger" defaults to "change", which
# does not re-evaluate the device the way installing a rule needs.
udevadm trigger --action=add --subsystem-match=hidraw
udevadm settle

echo
echo "--- verification -------------------------------------------------------"

if [[ -e "${SYMLINK}" ]]; then
    echo "symlink         ${SYMLINK} -> $(readlink -f "${SYMLINK}")"
else
    echo "symlink         MISSING"
    echo "                Either the receiver is unplugged, or its configuration"
    echo "                interface is not USB interface #2. Not fatal: the app does"
    echo "                not depend on the symlink and falls back to scanning sysfs."
    echo "                Run 'attackshark-probe info' to see what was discovered."
fi

if [[ -n "${TARGET_USER}" ]]; then
    echo "desktop user    ${TARGET_USER}"
    if ! getent group "${GROUP_NAME}" >/dev/null; then
        echo "group           ${GROUP_NAME} does not exist on this system."
        echo "                Edit GROUP in ${DESTINATION} to a group ${TARGET_USER}"
        echo "                belongs to, then re-run this script."
    elif id -nG "${TARGET_USER}" 2>/dev/null | tr ' ' '\n' | grep -qx "${GROUP_NAME}"; then
        echo "group           ${GROUP_NAME} (${TARGET_USER} is a member)"
    else
        echo "group           ${GROUP_NAME} - ${TARGET_USER} is NOT a member!"
        echo "                Grant access with:  sudo gpasswd -a ${TARGET_USER} ${GROUP_NAME}"
        echo "                then log out and back in, or change GROUP in"
        echo "                ${DESTINATION} to a group they are already in."
    fi
else
    echo "desktop user    could not be determined; access not checked."
fi

echo
echo "permissions on the receiver's hidraw nodes:"
found=0
for node in /dev/hidraw*; do
    [[ -e "${node}" ]] || continue
    if udevadm info -q property -n "${node}" 2>/dev/null | grep -q '^TAGS=.*uaccess'; then
        printf '  %s\n' "$(ls -l "${node}")"
        found=$((found + 1))
    fi
done
if [[ "${found}" -eq 0 ]]; then
    echo "  none - the rule is not matching this receiver."
    echo "  Check that it is plugged in via its 2.4 GHz receiver, then run:"
    echo "      udevadm test /sys/class/hidraw/hidraw0"
fi

echo
echo "next:  attackshark-probe info"
