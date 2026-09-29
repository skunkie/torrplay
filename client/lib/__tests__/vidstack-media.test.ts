// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { type MediaPlayerInstance } from '@vidstack/react';
import { describe, expect, it } from 'vitest';

import { getVidstackCurrentTime, getVidstackVideoElement } from '../vidstack-media';

function playerWith(provider: unknown, element: HTMLElement): MediaPlayerInstance {
  return { provider, el: element } as unknown as MediaPlayerInstance;
}

describe('getVidstackVideoElement', () => {
  it('prefers Vidstack provider media', () => {
    const providerVideo = document.createElement('video');
    const domVideo = document.createElement('video');
    const root = document.createElement('div');
    root.append(domVideo);

    expect(getVidstackVideoElement(playerWith({ media: providerVideo }, root))).toBe(providerVideo);
  });

  it('falls back to the light DOM video element', () => {
    const video = document.createElement('video');
    const root = document.createElement('div');
    root.append(video);

    expect(getVidstackVideoElement(playerWith(null, root))).toBe(video);
  });

  it('falls back to the shadow DOM and handles missing players', () => {
    const video = document.createElement('video');
    const root = document.createElement('div');
    root.attachShadow({ mode: 'open' }).append(video);

    expect(getVidstackVideoElement(playerWith({}, root))).toBe(video);
    expect(getVidstackVideoElement(null)).toBeNull();
  });
});

describe('getVidstackCurrentTime', () => {
  it('reads the video element, which Vidstack may lag behind after a seek', () => {
    const video = document.createElement('video');
    Object.defineProperty(video, 'currentTime', { value: 40, configurable: true });
    const player = { provider: { media: video }, el: document.createElement('div'), currentTime: 0 };

    expect(getVidstackCurrentTime(player as unknown as MediaPlayerInstance)).toBe(40);
  });

  it('falls back to Vidstack without a video element, and to zero without a player', () => {
    const player = { provider: null, el: document.createElement('div'), currentTime: 12 };

    expect(getVidstackCurrentTime(player as unknown as MediaPlayerInstance)).toBe(12);
    expect(getVidstackCurrentTime(null)).toBe(0);
  });
});
