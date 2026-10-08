// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import { DEMO_TORRENT_AUDIO_TRACKS, getDemoAudioTracks } from '../demo-audio-tracks';

describe('demo-audio-tracks', () => {
  it('defines audio tracks for all 4 demo torrents', () => {
    expect(DEMO_TORRENT_AUDIO_TRACKS['08ada5a7a6183aae1e09d831df6748d566095a10']).toBeDefined(); // Sintel
    expect(DEMO_TORRENT_AUDIO_TRACKS['dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c']).toBeDefined(); // Big Buck Bunny
    expect(DEMO_TORRENT_AUDIO_TRACKS['c9e15763f722f23e98a29decdfae341b98d53056']).toBeDefined(); // Cosmos Laundromat
    expect(DEMO_TORRENT_AUDIO_TRACKS['209c8226b299b308beaf2b9cd3fb49212dbd13ec']).toBeDefined(); // Tears of Steel
  });

  it('retrieves tracks by hash', () => {
    const sintelTracks = getDemoAudioTracks('08ada5a7a6183aae1e09d831df6748d566095a10');
    expect(sintelTracks.length).toBeGreaterThan(0);
    expect(sintelTracks[0].name).toBe('Original 5.1');
    expect(sintelTracks[0].codec).toBe('ac3');
  });

  it('retrieves tracks by title/filename substring', () => {
    const sintel = getDemoAudioTracks('Sintel.mp4');
    expect(sintel[0].label).toContain('English (AC3, 5.1)');

    const bunny = getDemoAudioTracks('Big Buck Bunny.mp4');
    expect(bunny[0].name).toBe('Surround 5.1');

    const cosmos = getDemoAudioTracks('Cosmos Laundromat.mp4');
    expect(cosmos.some(t => t.language === 'fra')).toBe(true);

    const tears = getDemoAudioTracks('Tears of Steel.webm');
    expect(tears[0].codec).toBe('dts');
  });

  it('falls back to default tracks when identifier is undefined or unknown', () => {
    const defaultTracks = getDemoAudioTracks(undefined);
    expect(defaultTracks.length).toBeGreaterThan(0);

    const unknownTracks = getDemoAudioTracks('Unknown Video.mp4');
    expect(unknownTracks.length).toBeGreaterThan(0);
  });
});
