// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from 'vitest';

import { addTorrent, getCategories, getTorrents, getTorrentStreamUrl } from '@/lib/api/torrents';
import { api } from '@/lib/api-client';

describe('getTorrentStreamUrl', () => {
  beforeEach(() => localStorage.clear());

  it('adds the scoped playback token to media URLs', () => {
    localStorage.setItem('playback_token', 'scoped token');

    expect(getTorrentStreamUrl('abc', 'Movie/video.mp4')).toMatch(
      /\/api\/v1\/stream\/abc\?path=Movie%2Fvideo\.mp4&token=scoped%20token$/,
    );
  });
});

describe('addTorrent', () => {
  it('forwards the selected storage with a .torrent upload', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({} as never);

    await addTorrent({
      file: new File(['d8:announce0:e'], 'movie.torrent'),
      storage: 'file',
      title: 'Movie',
    });

    const body = post.mock.calls[0][1] as FormData;
    expect(body.get('storage')).toBe('file');
    expect(body.get('title')).toBe('Movie');

    post.mockRestore();
  });
});

describe('getTorrents', () => {
  it('sends the search with the fuzzy flag', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({ torrents: [] } as never);

    await getTorrents({ fuzzy: true, search: 'sintel' });
    await getTorrents({ fuzzy: true });

    expect(get.mock.calls.map(call => call[0])).toEqual([
      '/api/v1/torrents?search=sintel&fuzzy=true',
      '/api/v1/torrents',
    ]);

    get.mockRestore();
  });
});

describe('getCategories', () => {
  it('reads the category names from the facets of a single torrent', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      facets: { categories: [{ count: 2, name: 'Movies' }, { count: 1, name: 'Shows' }], total: 3 },
      limit: 1,
      offset: 0,
      torrents: [],
      total: 3,
    } as never);

    expect(await getCategories()).toEqual(['Movies', 'Shows']);
    expect(get.mock.calls.map(call => call[0])).toEqual(['/api/v1/torrents?limit=1']);

    get.mockRestore();
  });
});
