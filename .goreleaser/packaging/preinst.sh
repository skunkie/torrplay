#!/bin/sh

# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

set -e

if systemctl is-active --quiet torrplay.service; then
    echo "Stopping existing TorrPlay service for upgrade..."
    systemctl stop torrplay.service
fi
