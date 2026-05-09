#!/bin/sh

# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

set -e

if [ "$1" = "purge" ]; then
    echo "Removing user torrplay..."
    if id -u torrplay >/dev/null 2>&1; then
        userdel torrplay
    fi
    DATA_DIR="/var/lib/torrplay"
    echo "Removing data directory $DATA_DIR..."
    rm -rf "$DATA_DIR"
fi
