#!/bin/sh
# SPDX-FileCopyrightText: 2026 TorrPlay
#
# SPDX-License-Identifier: MIT

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
client_dir=$(dirname -- "$script_dir")
ffmpeg_bin=${FFMPEG_BIN:-ffmpeg}
source_url='https://upload.wikimedia.org/wikipedia/commons/c/c2/This_is_a_10_second_testvideo_with_bars_and_tone.webm'
source_sha1='2bae542f4b216fc51171a25b0dd4f5db0f4a3269'
source_video=${DEMO_VIDEO_SOURCE:-}

if [ -z "$source_video" ]; then
  temp_dir=$(mktemp -d)
  source_video="$temp_dir/wikimedia-test-video.webm"
  trap 'rm -f "$source_video"; rmdir "$temp_dir"' EXIT HUP INT TERM
  curl -L --fail --show-error --silent "$source_url" -o "$source_video"
  printf '%s  %s\n' "$source_sha1" "$source_video" | sha1sum --check --status
fi

"$ffmpeg_bin" -hide_banner -y \
  -i "$source_video" \
  -f lavfi -i 'sine=frequency=660:sample_rate=48000:duration=10.051' \
  -i "$client_dir/public/demo/subtitles/torrplay-demo.embedded.en.vtt" \
  -map 0:v:0 -map 0:a:0 -map 1:a:0 -map 2:s:0 \
  -c:v copy \
  -c:a libopus -b:a 64k -ac 2 -filter:a:0 volume=0.08 -filter:a:1 volume=0.08 \
  -c:s webvtt \
  -metadata:s:a:0 language=eng -metadata:s:a:0 'title=Main Mix' \
  -metadata:s:a:1 language=eng -metadata:s:a:1 'title=Alternate Mix' \
  -metadata:s:s:0 language=eng -metadata:s:s:0 'title=Embedded English' \
  -disposition:a:0 default -disposition:a:1 0 -disposition:s:0 0 \
  -f webm "$client_dir/public/demo/torrplay-demo.webm"
