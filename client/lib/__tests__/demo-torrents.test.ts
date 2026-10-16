// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import { demoTorrentFacets, demoTorrentsResponse } from '@/lib/demo-torrents';
import type { Torrent } from '@/lib/types/api';

function torrent(hash: string, category?: string): Torrent {
  return { category, hash, name: hash } as Torrent;
}

describe('demoTorrentFacets', () => {
  it('returns no categories for no torrents', () => {
    expect(demoTorrentFacets([])).toEqual({ categories: [], total: 0 });
  });

  it('counts torrents per category ordered by name', () => {
    const torrents = [torrent('a', 'Shows'), torrent('b', 'Movies'), torrent('c', 'Shows')];

    expect(demoTorrentFacets(torrents)).toEqual({
      categories: [{ count: 1, name: 'Movies' }, { count: 2, name: 'Shows' }],
      total: 3,
    });
  });

  it('orders categories by code point as the server does', () => {
    const torrents = [torrent('a', '\u{1F3AC} Films'), torrent('b', '\uFF2D\uFF4F\uFF56\uFF49\uFF45\uFF53'), torrent('c', 'Shows')];

    expect(demoTorrentFacets(torrents).categories.map(category => category.name)).toEqual([
      'Shows',
      '\uFF2D\uFF4F\uFF56\uFF49\uFF45\uFF53',
      '\u{1F3AC} Films',
    ]);
  });

  it('counts uncategorized torrents only in the total', () => {
    const torrents = [torrent('a'), torrent('b', ''), torrent('c', 'Movies')];

    expect(demoTorrentFacets(torrents)).toEqual({ categories: [{ count: 1, name: 'Movies' }], total: 3 });
  });
});

describe('demoTorrentsResponse', () => {
  it('lists every torrent with its facets', () => {
    const torrents = [torrent('a', 'Movies'), torrent('b')];

    expect(demoTorrentsResponse(torrents)).toEqual({
      facets: { categories: [{ count: 1, name: 'Movies' }], total: 2 },
      limit: 0,
      offset: 0,
      torrents,
      total: 2,
    });
  });
});
