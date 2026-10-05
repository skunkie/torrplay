// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it } from 'vitest';

import { getTorrentStreamUrl } from '@/lib/api/torrents';

describe('getTorrentStreamUrl', () => {
  beforeEach(() => localStorage.clear());

  it('adds the scoped playback token to media URLs', () => {
    localStorage.setItem('playback_token', 'scoped token');

    expect(getTorrentStreamUrl('abc', 'Movie/video.mp4')).toMatch(
      /\/api\/v1\/stream\/abc\?path=Movie%2Fvideo\.mp4&token=scoped%20token$/,
    );
  });
});
