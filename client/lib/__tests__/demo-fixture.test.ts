// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

import { ALL_FORMATS, BufferSource, Input } from 'mediabunny';
import { describe, expect, it } from 'vitest';

import { loadEmbeddedSubtitleTrackVtt, parseMatroskaSubtitleCues, probeEmbeddedSubtitleTracks } from '../mkv-subtitles';
import { rangeFetch } from './fixtures/matroska';

async function readFixture(): Promise<Uint8Array> {
  return readFile(resolve(process.cwd(), 'public/demo/torrplay-demo.webm'));
}

describe('demo media fixture', () => {
  it('uses the 1080p Wikimedia test video', async () => {
    const bytes = await readFixture();
    const input = new Input({
      formats: ALL_FORMATS,
      source: new BufferSource(bytes),
    });

    try {
      const videoTracks = await input.getVideoTracks();
      expect(videoTracks).toHaveLength(1);
      await expect(videoTracks[0].getCodec()).resolves.toBe('vp8');
      await expect(videoTracks[0].getDisplayWidth()).resolves.toBe(1920);
      await expect(videoTracks[0].getDisplayHeight()).resolves.toBe(1080);
    } finally {
      input.dispose();
    }
  });

  it('contains two named Opus audio tracks', async () => {
    const bytes = await readFixture();
    const input = new Input({
      formats: ALL_FORMATS,
      source: new BufferSource(bytes),
    });

    try {
      const audioTracks = await input.getAudioTracks();
      expect(audioTracks).toHaveLength(2);
      await expect(Promise.all(audioTracks.map(track => track.getCodec())))
        .resolves.toEqual(['opus', 'opus']);
      await expect(Promise.all(audioTracks.map(track => track.getName())))
        .resolves.toEqual(['Main Mix', 'Alternate Mix']);
      await expect(Promise.all(audioTracks.map(track => track.getDisposition())))
        .resolves.toEqual([
          expect.objectContaining({ default: true }),
          expect.objectContaining({ default: false }),
        ]);
    } finally {
      input.dispose();
    }
  });

  it('contains synchronized embedded English subtitles', async () => {
    const bytes = await readFixture();
    const fetchFixture = rangeFetch(bytes);
    const tracks = await probeEmbeddedSubtitleTracks('/demo/torrplay-demo.webm', fetchFixture);

    expect(tracks).toHaveLength(1);
    expect(tracks[0]).toMatchObject({
      id: 'embedded:4:D_WEBVTT/SUBTITLES',
      language: 'eng',
      label: 'English [Embedded English] (Embedded VTT)',
    });
    expect(tracks[0].unavailableReason).toBeUndefined();
    expect(parseMatroskaSubtitleCues(
      bytes,
      tracks[0].embeddedTrackNumber!,
      1000000,
      'D_WEBVTT/SUBTITLES'
    )).not.toEqual([]);
    const vtt = await loadEmbeddedSubtitleTrackVtt(
      '/demo/torrplay-demo.webm',
      tracks[0].embeddedTrackNumber!,
      fetchFixture
    );
    expect(decodeURIComponent(vtt)).toContain('This caption is embedded in the demo video.');
  });

  it('ships attribution for the adapted media', async () => {
    const license = await readFile(resolve(process.cwd(), 'public/demo/torrplay-demo.webm.license'), 'utf8');

    expect(license).toContain('SPDX-License-Identifier: CC-BY-SA-4.0');
    expect(license).toContain('Bjorn Falkevik');
    expect(license).toContain('https://commons.wikimedia.org/wiki/File:This_is_a_10_second_testvideo_with_bars_and_tone.webm');
    expect(license).toContain('Changes: remuxed for the TorrPlay demo');
  });
});
