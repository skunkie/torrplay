#!/bin/sh

# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

set -e

if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
fi

if [ "$1" = "purge" ]; then
    if id -u torrplay >/dev/null 2>&1; then
        userdel torrplay
    fi
    rm -rf /var/lib/torrplay
fi
