// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { type MediaPlayerInstance } from '@vidstack/react';

function asVideoElement(value: unknown): HTMLVideoElement | null {
  return typeof value === 'object' && value !== null && 'tagName' in value && value.tagName === 'VIDEO'
    ? value as HTMLVideoElement
    : null;
}

/**
 * Isolates Vidstack provider/DOM discovery in one upgrade-sensitive adapter.
 * `provider.media` is preferred, while DOM fallbacks support provider timing
 * and rendering differences observed across Vidstack 1.x environments.
 */
export function getVidstackVideoElement(player: MediaPlayerInstance | null): HTMLVideoElement | null {
  if (!player) return null;

  const providerMedia = (player.provider as unknown as { media?: unknown })?.media;
  const providerVideo = asVideoElement(providerMedia);
  if (providerVideo) return providerVideo;

  const playerElement = player.el;
  return asVideoElement(playerElement?.querySelector('video'))
    ?? asVideoElement(playerElement?.shadowRoot?.querySelector('video'));
}

/**
 * Returns the playback position of the player's video element, or Vidstack's
 * own when the element cannot be found. Vidstack updates its position only
 * after a seek reports progress, so right after the resume seek it still
 * reports the start of the file.
 */
export function getVidstackCurrentTime(player: MediaPlayerInstance | null): number {
  return getVidstackVideoElement(player)?.currentTime ?? player?.currentTime ?? 0;
}
