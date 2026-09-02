#!/bin/sh

# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

set -e

if ! getent group torrplay >/dev/null 2>&1; then
    groupadd --system torrplay
fi

if ! id -u torrplay >/dev/null 2>&1; then
    useradd --system --gid torrplay --no-create-home --shell /bin/false torrplay
fi

# StateDirectory= handles this; only fixes trees left by older packages.
if [ -d /var/lib/torrplay ] && [ "$(stat -c %U /var/lib/torrplay 2>/dev/null)" != torrplay ]; then
    chown -R torrplay:torrplay /var/lib/torrplay
fi

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

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
fi

# First install only: deb "configure" with no old version, rpm "1".
if { [ "$1" = "configure" ] && [ -z "$2" ]; } || [ "$1" = "1" ]; then
    sd_enable enable torrplay.service
    sd_invoke start torrplay.service
else
    sd_invoke try-restart torrplay.service
fi
