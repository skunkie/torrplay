// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it } from 'vitest';

import { getPlaybackPositionSeconds, savePlaybackPositionSeconds } from '@/lib/playback-position';

describe('playback position storage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('stores rounded seconds per torrent file', () => {
    savePlaybackPositionSeconds('hash-a', 'movie.mkv', 123.6);
    savePlaybackPositionSeconds('hash-a', 'episode.mkv', 45.2);

    expect(getPlaybackPositionSeconds('hash-a', 'movie.mkv')).toBe(124);
    expect(getPlaybackPositionSeconds('hash-a', 'episode.mkv')).toBe(45);
    expect(getPlaybackPositionSeconds('hash-b', 'movie.mkv')).toBe(0);
  });

  it('clears completed or invalid positions without touching other files', () => {
    savePlaybackPositionSeconds('hash-a', 'movie.mkv', 123);
    savePlaybackPositionSeconds('hash-a', 'episode.mkv', 45);
    savePlaybackPositionSeconds('hash-a', 'movie.mkv', 0);

    expect(getPlaybackPositionSeconds('hash-a', 'movie.mkv')).toBe(0);
    expect(getPlaybackPositionSeconds('hash-a', 'episode.mkv')).toBe(45);
  });

  it('ignores malformed persisted data', () => {
    localStorage.setItem('torrplay_playback_positions', '{broken');

    expect(getPlaybackPositionSeconds('hash-a', 'movie.mkv')).toBe(0);
  });

  it('discards malformed entries before saving new positions', () => {
    localStorage.setItem('torrplay_playback_positions', JSON.stringify({
      'hash-a': 'invalid',
      'hash-b': { 'movie.mkv': 'invalid' },
    }));

    savePlaybackPositionSeconds('hash-a', 'movie.mkv', 123);

    expect(getPlaybackPositionSeconds('hash-a', 'movie.mkv')).toBe(123);
    expect(getPlaybackPositionSeconds('hash-b', 'movie.mkv')).toBe(0);
  });

  it('forgets the least recently saved torrents beyond the limit', () => {
    const hashOf = (index: number) => `hash-${index}`;
    for (let index = 0; index < 100; index++) {
      savePlaybackPositionSeconds(hashOf(index), 'movie.mkv', 10);
    }
    // Saving again makes the first torrent the most recent.
    savePlaybackPositionSeconds(hashOf(0), 'movie.mkv', 20);

    savePlaybackPositionSeconds(hashOf(100), 'movie.mkv', 10);

    expect(getPlaybackPositionSeconds(hashOf(0), 'movie.mkv')).toBe(20);
    expect(getPlaybackPositionSeconds(hashOf(1), 'movie.mkv')).toBe(0);
    expect(getPlaybackPositionSeconds(hashOf(2), 'movie.mkv')).toBe(10);
    expect(getPlaybackPositionSeconds(hashOf(100), 'movie.mkv')).toBe(10);
  });
});
