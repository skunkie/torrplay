// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { renderHook, waitFor } from '@testing-library/react';
import type { MediaPlayerInstance } from '@vidstack/react';
import { describe, expect, it, vi } from 'vitest';

import { useSubtitleTracks } from '../use-subtitle-tracks';

const scan = vi.hoisted(() => ({ options: null as null | { currentTime: () => number } }));

vi.mock('@vidstack/react', () => ({
  TextTrack: class {
    id: string;
    mode = 'disabled';
    cues = [];
    constructor(init: { id: string }) { this.id = init.id; }
  },
}));

vi.mock('@/lib/mkv-subtitles', async importOriginal => ({
  ...await importOriginal<typeof import('@/lib/mkv-subtitles')>(),
  probeEmbeddedSubtitleTracks: () => Promise.resolve([
    { id: 'embedded-2', src: '', label: 'English', embeddedTrackNumber: 2, default: true },
  ]),
  loadEmbeddedSubtitleTrackVtt: (_url: string, _track: number, _fetch: unknown, _signal: AbortSignal,
    _onCues: unknown, options: { currentTime: () => number }) => {
    scan.options = options;
    return new Promise(() => {});
  },
}));

describe('useSubtitleTracks embedded scan', () => {
  it('follows the video element, which Vidstack lags behind after the resume seek', async () => {
    const video = document.createElement('video');
    Object.defineProperty(video, 'currentTime', { value: 40, configurable: true });
    const instance = {
      currentTime: 0,
      el: document.createElement('div'),
      provider: { media: video },
      textTracks: { add: vi.fn(), remove: vi.fn() },
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    } as unknown as MediaPlayerInstance;

    renderHook(() => useSubtitleTracks({
      player: { current: instance },
      sourceKey: 'movie.mkv',
      embeddedStreamUrl: 'http://test-server/movie.mkv',
    }));

    await waitFor(() => expect(scan.options).not.toBeNull());
    expect(scan.options?.currentTime()).toBe(40);
  });
});
