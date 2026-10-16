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
    expect(demoTorrentFacets([])).toMatchObject({ categories: [], total: 0 });
  });

  it('counts torrents per category ordered by name', () => {
    const torrents = [torrent('a', 'Shows'), torrent('b', 'Movies'), torrent('c', 'Shows')];

    expect(demoTorrentFacets(torrents)).toMatchObject({
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

    expect(demoTorrentFacets(torrents)).toMatchObject({ categories: [{ count: 1, name: 'Movies' }], total: 3 });
  });
});

describe('demoTorrentFacets version', () => {
  const torrents = [torrent('a', 'Movies'), torrent('b')];
  const version = demoTorrentFacets(torrents).version;

  it('stays the same for equal torrents', () => {
    expect(demoTorrentFacets([torrent('a', 'Movies'), torrent('b')]).version).toBe(version);
  });

  it('changes with a field, membership, or order', () => {
    expect(demoTorrentFacets([torrent('a', 'Shows'), torrent('b')]).version).not.toBe(version);
    expect(demoTorrentFacets([torrent('a', 'Movies')]).version).not.toBe(version);
    expect(demoTorrentFacets([torrent('b'), torrent('a', 'Movies')]).version).not.toBe(version);
  });
});

describe('demoTorrentsResponse', () => {
  it('lists every torrent with its facets', () => {
    const torrents = [torrent('a', 'Movies'), torrent('b')];

    expect(demoTorrentsResponse(torrents)).toEqual({
      facets: { categories: [{ count: 1, name: 'Movies' }], total: 2, version: demoTorrentFacets(torrents).version },
      limit: 0,
      offset: 0,
      torrents,
      total: 2,
    });
  });
});
