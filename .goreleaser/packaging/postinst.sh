#!/bin/sh

# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

set -e

if ! id -u torrplay >/dev/null 2>&1; then
    echo "Creating user torrplay..."
    useradd --system --user-group --no-create-home --shell /bin/false torrplay
fi

DATA_DIR="/var/lib/torrplay"
if [ ! -d "$DATA_DIR" ]; then
    echo "Creating data directory $DATA_DIR..."
    mkdir -p "$DATA_DIR"
fi

chown -R torrplay:torrplay "$DATA_DIR"

echo "Reloading systemd daemon..."
systemctl daemon-reload

echo "Enabling TorrPlay service..."
systemctl enable torrplay.service

echo "Starting TorrPlay service..."
systemctl start torrplay.service
