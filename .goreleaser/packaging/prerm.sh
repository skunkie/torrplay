#!/bin/sh

# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

set -e

# deb-systemd-* honour policy-rc.d; systemctl is the fallback elsewhere.
sd_enable() {
    if [ -x /usr/bin/deb-systemd-helper ]; then
        deb-systemd-helper "$@" || true
    elif [ -d /run/systemd/system ]; then
        systemctl "$@" || true
    fi
}

sd_invoke() {
    [ -d /run/systemd/system ] || return 0

    if [ -x /usr/bin/deb-systemd-invoke ]; then
        deb-systemd-invoke "$@" || true
    else
        systemctl "$@" || true
    fi
}

# Removal only: deb "remove", rpm "0". Upgrades keep the unit enabled.
if [ "$1" = "remove" ] || [ "$1" = "0" ]; then
    sd_invoke stop torrplay.service
    sd_enable disable torrplay.service
fi
