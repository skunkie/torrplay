#!/bin/sh

# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

set -e

echo "Stopping TorrPlay service..."
systemctl stop torrplay.service

echo "Disabling TorrPlay service..."
systemctl disable torrplay.service
