// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import { getDemoSubtitleTracks } from '../demo-subtitles';

describe('demo-subtitles', () => {
  it('returns synchronized external subtitle fixtures', () => {
    expect(getDemoSubtitleTracks('Sintel.webm')).toEqual([
      expect.objectContaining({
        src: '/demo/subtitles/torrplay-demo.en-sdh.vtt',
        label: 'English [SDH] (VTT)',
        language: 'en',
      }),
      expect.objectContaining({
        src: '/demo/subtitles/torrplay-demo.es.vtt',
        label: 'Spanish (VTT)',
        language: 'es',
      }),
    ]);
  });

  it('uses live matching rules for multi-video torrents', () => {
    const files = [
      { name: 'Episode 1.webm', path: 'Show/Episode 1.webm', length: 1000 },
      { name: 'Episode 1.en-sdh.vtt', path: 'Show/Episode 1.en-sdh.vtt', length: 200 },
      { name: 'Episode 10.webm', path: 'Show/Episode 10.webm', length: 1000 },
      { name: 'Episode 10.es.vtt', path: 'Show/Episode 10.es.vtt', length: 272 },
    ];

    expect(getDemoSubtitleTracks(files[0], files).map(track => track.id))
      .toEqual(['Show/Episode 1.en-sdh.vtt']);
    expect(getDemoSubtitleTracks(files[2], files).map(track => track.id))
      .toEqual(['Show/Episode 10.es.vtt']);
  });
});
