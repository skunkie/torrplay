// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { afterEach, describe, expect, it, vi } from 'vitest';

import { api } from '@/lib/api-client';
import {
  filterAndSortTorrents,
  getTorrentList,
  isAwaitingSearch,
  searchTorrentsByText,
  type TorrentListView,
  visibleTorrents,
} from '@/lib/torrent-list';
import type { Torrent, TorrentsResponse } from '@/lib/types/api';

function torrent(hash: string, overrides: Partial<Torrent> = {}): Torrent {
  return {
    active: false,
    hash,
    magnet: `magnet:?xt=urn:btih:${hash}`,
    name: hash,
    pieceCount: 1,
    pieceSize: 1,
    storage: 'memory',
    totalSize: 0,
    ...overrides,
  } as Torrent;
}

function response(torrents: Torrent[]): TorrentsResponse {
  return { limit: 0, offset: 0, torrents, total: torrents.length };
}

describe('getTorrentList', () => {
  afterEach(() => vi.restoreAllMocks());

  it('fetches only the full list without a search', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue(response([torrent('a')]) as never);

    const list = await getTorrentList('');

    expect(get.mock.calls.map(call => call[0])).toEqual(['/api/v1/torrents']);
    expect(list.torrents.map(t => t.hash)).toEqual(['a']);
    expect(list.matches).toBeUndefined();
    expect(list.search).toBe('');
  });

  it('fetches the fuzzy matches alongside the full list', async () => {
    const get = vi.spyOn(api, 'get').mockImplementation(async (endpoint: string) =>
      (endpoint.includes('search=')
        ? response([torrent('b')])
        : response([torrent('a'), torrent('b')])) as never);

    const list = await getTorrentList('big buck');

    expect(get.mock.calls.map(call => call[0]).sort()).toEqual([
      '/api/v1/torrents',
      '/api/v1/torrents?search=big+buck&fuzzy=true',
    ]);
    expect(list.torrents.map(t => t.hash)).toEqual(['a', 'b']);
    expect(list.matches?.map(t => t.hash)).toEqual(['b']);
    expect(list.search).toBe('big buck');
  });
});

describe('filterAndSortTorrents', () => {
  const torrents = [
    torrent('saved-small', {
      category: 'movie', createdAt: '2026-01-02', title: 'Sintel', totalSize: 1, updatedAt: '2026-01-04',
    }),
    torrent('loaded', { category: 'movie', title: 'Spring', totalSize: 5 }),
    torrent('saved-large', {
      category: 'movie', createdAt: '2026-01-01', title: 'Big Buck Bunny', totalSize: 9, updatedAt: '2026-01-05',
    }),
    torrent('other-category', { category: 'show', createdAt: '2026-01-03', title: 'Show.S01E02' }),
  ];
  const hashes = (sortBy: Parameters<typeof filterAndSortTorrents>[2]) =>
    filterAndSortTorrents(torrents, 'movie', sortBy).map(t => t.hash);

  it('keeps the given order for relevance', () => {
    expect(hashes('relevance')).toEqual(['saved-small', 'loaded', 'saved-large']);
  });

  it('puts client-only torrents first for every other order', () => {
    expect(hashes('date')).toEqual(['loaded', 'saved-small', 'saved-large']);
    expect(hashes('name')).toEqual(['loaded', 'saved-large', 'saved-small']);
    expect(hashes('size')).toEqual(['loaded', 'saved-large', 'saved-small']);
    expect(hashes('updated')).toEqual(['loaded', 'saved-large', 'saved-small']);
  });

  it('keeps every category without a filter', () => {
    expect(filterAndSortTorrents(torrents, '', 'relevance')).toHaveLength(4);
  });

  it('leaves the input untouched', () => {
    filterAndSortTorrents(torrents, '', 'name');
    expect(torrents[0].hash).toBe('saved-small');
  });
});

describe('searchTorrentsByText', () => {
  const names = (torrents: Torrent[], query: string) => searchTorrentsByText(torrents, query).map(t => t.name);
  const named = (...names: string[]) => names.map(name => torrent(name));

  describe('matching', () => {
    const torrents = [
      torrent('titled', { name: 'unrelated', title: 'Big Buck Bunny' }),
      torrent('named', { name: 'big.buck.bunny.1080p' }),
      torrent('other', { title: 'Sintel - 2010.mkv' }),
    ];
    const hashes = (query: string) => searchTorrentsByText(torrents, query).map(t => t.hash);

    it('matches the title or the name, ignoring case', () => {
      expect(hashes('UNRELATED')).toEqual(['titled']);
      expect(hashes('1080P')).toEqual(['named']);
    });

    it('requires every word of the query in any order', () => {
      expect(hashes('BIG BUCK')).toEqual(['titled', 'named']);
      expect(hashes('  bunny   big ')).toEqual(['titled', 'named']);
      expect(hashes('sintel unrelated')).toEqual([]);
      expect(hashes('sintel 2014')).toEqual([]);
    });

    it('takes the words from the title and the name alike', () => {
      expect(hashes('unrelated bunny')).toEqual(['titled']);
    });

    it('reads punctuation in the query as a word break', () => {
      expect(hashes('buck.')).toEqual(['titled', 'named']);
      expect(names(named('Sintel 2010'), 'sintel.2010')).toEqual(['Sintel 2010']);
    });

    it('matches a query of punctuation alone as text', () => {
      expect(hashes('- .')).toEqual(['other']);
      expect(hashes(' _ ')).toEqual([]);
    });
  });

  describe('folding', () => {
    const folded = [
      torrent('accented', { title: 'Élan.Vital.2001' }),
      torrent('apostrophe', { name: 'Wing’s.Flight.2021' }),
      torrent('plain-apostrophe', { name: "Llama's.Journey.2019" }),
      torrent('sharp-s', { title: 'Hauptstraße.Tour.1998' }),
      torrent('sigma', { name: 'ΚΑΛΟΣ.ΔΡΟΜΟΣ.2008' }),
    ];
    const hashes = (query: string) => searchTorrentsByText(folded, query).map(t => t.hash);

    it('ignores accents and full-width letters', () => {
      expect(hashes('elan')).toEqual(['accented']);
      expect(hashes('ｖｉｔａｌ')).toEqual(['accented']);
    });

    it('drops apostrophes of either style', () => {
      expect(hashes("wing's")).toEqual(['apostrophe']);
      expect(hashes('wings flight')).toEqual(['apostrophe']);
      expect(hashes('llamas journey')).toEqual(['plain-apostrophe']);
    });

    it('folds a sharp s and a final sigma as the server does', () => {
      expect(hashes('hauptstrasse')).toEqual(['sharp-s']);
      expect(hashes('καλοσ')).toEqual(['sigma']);
      expect(hashes('δρομος')).toEqual(['sigma']);
    });
  });

  describe('ranking', () => {
    it('ranks whole words above word starts', () => {
      expect(names(named('Springtime.Walk', 'Spring.2019'), 'spring')).toEqual(['Spring.2019', 'Springtime.Walk']);
    });

    it('ranks word starts above matches inside words', () => {
      expect(names(named('Bigllama.Demo', 'Llamaland.Demo'), 'llama')).toEqual(['Llamaland.Demo', 'Bigllama.Demo']);
    });

    it('sums the quality of every query word', () => {
      expect(names(named('Show.Bigllamaland', 'Llamalandia.Show', 'Show.Llamaland'), 'llamaland show'))
        .toEqual(['Show.Llamaland', 'Llamalandia.Show', 'Show.Bigllamaland']);
    });

    it('keeps the given order among equal matches', () => {
      // Long enough that an unstable sort would reorder ties.
      const wholeWords = Array.from({ length: 40 }, (_, i) => `Sintel.${String(39 - i).padStart(2, '0')}`);
      const insideWords = wholeWords.map(name => `X${name.toLowerCase()}`);
      const interleaved = wholeWords.flatMap((name, i) => [name, insideWords[i]]);

      expect(names(named(...interleaved), 'sintel')).toEqual([...wholeWords, ...insideWords]);
    });

    it('reads release names and apostrophes as words', () => {
      expect(names(named('Llamasaurus.Journey', "Llama's.Journey.2019"), 'llamas')).toEqual(["Llama's.Journey.2019", 'Llamasaurus.Journey']);
      expect(names(named('Sintel2010', 'Sintel.2010'), '2010')).toEqual(['Sintel.2010', 'Sintel2010']);
    });

    it('ranks the title and the name together', () => {
      const torrents = [
        torrent('named', { name: 'Llamaland.Demo', title: 'Bigllama' }),
        torrent('titled', { name: 'llamaland', title: 'Llama' }),
      ];

      expect(searchTorrentsByText(torrents, 'llama').map(t => t.hash)).toEqual(['titled', 'named']);
    });

    it('keeps the given order for a query of punctuation alone', () => {
      expect(names(named('Sintel.2010', 'Big.Buck.Bunny'), ' . ')).toEqual(['Sintel.2010', 'Big.Buck.Bunny']);
    });
  });
});

describe('visibleTorrents', () => {
  const torrents = [
    torrent('sintel', { category: 'movie', createdAt: '2026-01-01', title: 'Sintel' }),
    torrent('spring', { category: 'movie', createdAt: '2026-01-02', title: 'Spring' }),
    torrent('sintel-show', { category: 'show', createdAt: '2026-01-03', title: 'Sintel Show' }),
  ];
  const view: TorrentListView = { categoryFilter: '', query: '', searchSortBy: 'relevance', sortBy: 'date' };
  const hashes = (list: Parameters<typeof visibleTorrents>[0], overrides: Partial<TorrentListView> = {}) =>
    visibleTorrents(list, { ...view, ...overrides }).map(t => t.hash);

  it('shows every torrent in the stored order without a query', () => {
    expect(hashes({ search: '', torrents })).toEqual(['sintel-show', 'spring', 'sintel']);
    expect(hashes({ search: '', torrents }, { categoryFilter: 'movie' })).toEqual(['spring', 'sintel']);
  });

  it('shows the server matches for the current query in the search order', () => {
    const list = { matches: [torrents[0], torrents[2]], search: 'sintl', torrents };

    expect(hashes(list, { query: 'sintl' })).toEqual(['sintel', 'sintel-show']);
    expect(hashes(list, { query: 'sintl', searchSortBy: 'date' })).toEqual(['sintel-show', 'sintel']);
    expect(hashes(list, { categoryFilter: 'show', query: 'sintl' })).toEqual(['sintel-show']);
  });

  it('narrows by title until the server answers the current query', () => {
    const answered = { matches: [torrents[1]], search: 'spring', torrents };

    expect(hashes(answered, { query: 'sint' })).toEqual(['sintel', 'sintel-show']);
    expect(hashes({ search: '', torrents }, { query: 'SPR' })).toEqual(['spring']);
    expect(hashes(undefined, { query: 'sint' })).toEqual([]);
  });

  it('ranks the title matches as the server does until it answers', () => {
    const list = { search: '', torrents: [torrent('showcase', { createdAt: '2026-01-04', title: 'Showcase' }), ...torrents] };

    expect(hashes(list, { query: 'show' })).toEqual(['sintel-show', 'showcase']);
    expect(hashes(list, { query: 'show', searchSortBy: 'date' })).toEqual(['showcase', 'sintel-show']);
  });
});

describe('isAwaitingSearch', () => {
  it('waits only for a query the list was not fetched for', () => {
    expect(isAwaitingSearch({ search: '' }, '')).toBe(false);
    expect(isAwaitingSearch({ search: 'sintel' }, 'sintel')).toBe(false);
    expect(isAwaitingSearch({ search: 'sint' }, 'sintel')).toBe(true);
    expect(isAwaitingSearch(undefined, 'sintel')).toBe(true);
    expect(isAwaitingSearch({ search: 'sint' }, 'sintel', true)).toBe(false);
  });
});
