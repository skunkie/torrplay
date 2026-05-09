#!/bin/bash

# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

# Check if a file path and URL are provided as arguments.
if [ -z "$1" ] || [ -z "$2" ]; then
  echo "Usage: $0 <json_file> <server_url>"
  exit 1
fi

JSON_FILE="$1"
SERVER_URL="$2"

# Check if jq is installed.
if ! command -v jq &> /dev/null; then
  echo "jq is not installed. Please install it to use this script."
  exit 1
fi

# Read the JSON file, reverse the array, and generate curl commands.
jq -c 'reverse | .[]' "$JSON_FILE" | while read -r item; do
  # Use jq to create the JSON payload directly from the item.
  json_payload=$(echo "$item" | jq -c '{
    magnet: (if .magnet and .magnet != "" then .magnet else "magnet:?xt=urn:btih:" + .hash end),
    title: .title,
    category: .category,
    poster: .poster
  }')

  # Escape single quotes to be safely enclosed in a single-quoted string for curl's -d.
  escaped_payload=$(echo "$json_payload" | sed "s/'/'\\\''/g")

  # Generate a single-line curl command.
  echo "curl -X POST -H 'Content-Type: application/json' -d '$escaped_payload' $SERVER_URL/api/v1/torrents"
done
