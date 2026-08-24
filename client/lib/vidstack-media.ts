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
