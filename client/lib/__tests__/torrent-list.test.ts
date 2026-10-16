// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { api } from '@/lib/api-client';
import {
  createTorrentListLoader,
  filterAndSortTorrents,
  findTorrent,
  isAwaitingSearch,
  searchTorrentsByText,
  type TorrentListView,
  torrentTotal,
  visibleTorrents,
} from '@/lib/torrent-list';
import type { Torrent, TorrentFacets, TorrentsResponse } from '@/lib/types/api';

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

// versionOf stands in for the server's version of the full list all.
function versionOf(all: Torrent[]): string {
  return JSON.stringify(all);
}

function response(
  torrents: Torrent[],
  facets: TorrentFacets = { categories: [], total: torrents.length, version: versionOf(torrents) },
): TorrentsResponse {
  return { facets, limit: 0, offset: 0, torrents, total: torrents.length };
}

describe('createTorrentListLoader', () => {
  afterEach(() => vi.restoreAllMocks());

  const createdAt = '2026-01-01T00:00:00Z';
  const sintel = torrent('a', { createdAt, title: 'Sintel' });
  const spring = torrent('b', { createdAt, title: 'Spring' });
  const tearsOfSteel = torrent('c', { createdAt, title: 'Tears of Steel' });

  // answerWith answers a full listing with the current all and a search with
  // the torrents that matches maps it to, both versioned by all, as the server
  // versions every listing by its full list. Changing the returned server's
  // all changes the version of later answers.
  function answerWith(all: Torrent[], matches: Record<string, Torrent[]> = {}) {
    const server = { all };
    const get = vi.spyOn(api, 'get').mockImplementation(async (endpoint: string) => {
      const search = new URLSearchParams(endpoint.split('?')[1]).get('search');
      const facets = { categories: [], total: server.all.length, version: versionOf(server.all) };
      return response(search ? matches[search] ?? [] : server.all, facets) as never;
    });
    return Object.assign(server, { get });
  }

  function endpoints(server: ReturnType<typeof answerWith>): string[] {
    return server.get.mock.calls.map(call => call[0]);
  }

  it('fetches only the full list without a search', async () => {
    const server = answerWith([sintel]);

    const list = await createTorrentListLoader()('');

    expect(endpoints(server)).toEqual(['/api/v1/torrents']);
    expect(list.torrents).toEqual([sintel]);
    expect(list.matches).toBeUndefined();
    expect(list.search).toBe('');
  });

  it('fetches only the fuzzy matches while the full list is current', async () => {
    const server = answerWith([sintel, spring], { sp: [spring], spr: [spring] });
    const load = createTorrentListLoader();
    await load('');
    server.get.mockClear();

    const list = await load('sp');
    await load('spr');

    expect(endpoints(server)).toEqual([
      '/api/v1/torrents?search=sp&fuzzy=true',
      '/api/v1/torrents?search=spr&fuzzy=true',
    ]);
    expect(list.facets.version).toBe(versionOf([sintel, spring]));
    expect(list.matches).toEqual([spring]);
    expect(list.torrents).toEqual([sintel, spring]);
    expect(list.search).toBe('sp');
  });

  it('waits for the full list with a search made before it was known', async () => {
    const server = answerWith([sintel, spring], { sp: [spring], spr: [spring] });
    const load = createTorrentListLoader();

    const list = await load('sp');

    expect(list.torrents).toEqual([sintel, spring]);
    expect(list.matches).toEqual([spring]);
    expect(load.known().torrents).toEqual([sintel, spring]);
    expect(endpoints(server)).toEqual(['/api/v1/torrents?search=sp&fuzzy=true', '/api/v1/torrents']);

    server.get.mockClear();
    expect((await load('spr')).torrents).toEqual([sintel, spring]);

    expect(endpoints(server)).toEqual(['/api/v1/torrents?search=spr&fuzzy=true']);
  });

  it('fetches the full list again once a search reports a new version', async () => {
    const server = answerWith([sintel, spring, tearsOfSteel], { s: [spring, tearsOfSteel], z: [] });
    const load = createTorrentListLoader();
    await load('');
    const changes = vi.fn();
    load.subscribe(changes);
    // Sintel is deleted on the server.
    server.all = [spring, tearsOfSteel];
    server.get.mockClear();

    await load('s');

    await vi.waitFor(() => expect(load.known().torrents).toEqual([spring, tearsOfSteel]));
    expect(changes).toHaveBeenCalledTimes(1);
    expect(endpoints(server)).toEqual(['/api/v1/torrents?search=s&fuzzy=true', '/api/v1/torrents']);

    server.get.mockClear();
    expect((await load('z')).torrents).toEqual([spring, tearsOfSteel]);
    expect(endpoints(server)).toEqual(['/api/v1/torrents?search=z&fuzzy=true']);
  });

  it('answers a search without waiting for the full list', async () => {
    const server = answerWith([sintel, spring], { sp: [spring], spr: [spring] });
    const load = createTorrentListLoader();
    await load('');
    let answerFull: () => void = () => {};
    server.all = [spring];
    server.get.mockImplementation(async (endpoint: string) => {
      const facets = { categories: [], total: 1, version: versionOf([spring]) };
      if (endpoint.includes('search=')) {
        return response([spring], facets) as never;
      }
      return new Promise(resolve => {
        answerFull = () => resolve(response([spring], facets) as never);
      });
    });

    const list = await load('sp');

    expect(list.matches).toEqual([spring]);
    expect(list.torrents).toEqual([sintel, spring]);
    expect(load.known().torrents).toEqual([sintel, spring]);

    // A refresh while the full list is still loading does not fetch it again.
    await load('spr');
    expect(endpoints(server).filter(endpoint => !endpoint.includes('search='))).toEqual([
      '/api/v1/torrents',
      '/api/v1/torrents',
    ]);

    answerFull();
    await vi.waitFor(() => expect(load.known().torrents).toEqual([spring]));
  });

  it('fetches the full list again when a search during a refetch reports a newer version', async () => {
    const server = answerWith([sintel], { s: [sintel, spring] });
    const load = createTorrentListLoader();
    await load('');
    server.all = [sintel, spring];
    const answers: Array<() => void> = [];
    server.get.mockImplementation(async (endpoint: string) => {
      const all = server.all;
      const facets = { categories: [], total: all.length, version: versionOf(all) };
      if (endpoint.includes('search=')) {
        return response([spring], facets) as never;
      }
      return new Promise(resolve => answers.push(() => resolve(response(all, facets) as never)));
    });

    // The first search starts a refetch of the version with Spring.
    await load('s');
    expect(answers).toHaveLength(1);
    // Tears of Steel is added while the refetch is in flight, and a later
    // search reports that version without starting a second fetch.
    server.all = [sintel, spring, tearsOfSteel];
    await load('s');
    expect(answers).toHaveLength(1);

    answers[0]();
    await vi.waitFor(() => expect(answers).toHaveLength(2));
    expect(load.known().torrents).toEqual([sintel, spring]);
    answers[1]();

    await vi.waitFor(() => expect(load.known().torrents).toEqual([sintel, spring, tearsOfSteel]));
  });

  it('does not let a full list requested before a refetch roll it back', async () => {
    const server = answerWith([sintel]);
    const load = createTorrentListLoader();
    await load('');
    const pending: Record<string, () => void> = {};
    server.all = [sintel, spring];
    server.get.mockImplementation(async (endpoint: string) => {
      const facets = { categories: [], total: 2, version: versionOf([sintel, spring]) };
      if (endpoint.includes('search=')) {
        return new Promise(resolve => {
          pending.search = () => resolve(response([spring], facets) as never);
        });
      }
      if (!pending.older) {
        // The full list of a cleared search, requested while the search is
        // pending, answers late with the torrents from before Spring.
        return new Promise(resolve => {
          pending.older = () => resolve(response([sintel]) as never);
        });
      }
      return response(server.all, facets) as never;
    });

    const search = load('s');
    const older = load('');
    pending.search();
    await search;
    await vi.waitFor(() => expect(load.known().torrents).toEqual([sintel, spring]));

    pending.older();
    await older;
    expect(load.known().torrents).toEqual([sintel, spring]);
  });

  it('answers a search whose full-list refetch failed and fetches it again', async () => {
    const server = answerWith([sintel, spring], { sp: [spring], spr: [spring] });
    server.get.mockImplementation(async (endpoint: string) => {
      if (!endpoint.includes('search=')) {
        throw new Error('network down');
      }
      return response([spring], { categories: [], total: 2, version: versionOf([sintel, spring]) }) as never;
    });
    const load = createTorrentListLoader();

    const list = await load('sp');

    expect(list.matches).toEqual([spring]);
    expect(list.torrents).toEqual([spring]);
    await vi.waitFor(() => expect(endpoints(server)).toHaveLength(2));
    expect(load.known().torrents).toBeUndefined();

    server.get.mockClear();
    await load('spr');

    await vi.waitFor(() => expect(endpoints(server)).toEqual([
      '/api/v1/torrents?search=spr&fuzzy=true',
      '/api/v1/torrents',
    ]));
  });

  it('keeps the known list and notifies no listener while the version is unchanged', async () => {
    const server = answerWith([sintel, spring]);
    const load = createTorrentListLoader();
    const changes = vi.fn();
    load.subscribe(changes);
    await load('');
    const known = load.known();

    await load('');

    expect(load.known()).toBe(known);
    expect(changes).toHaveBeenCalledTimes(1);

    server.all = [spring];
    await load('');

    expect(load.known().torrents).toEqual([spring]);
    expect(changes).toHaveBeenCalledTimes(2);
  });

  it('answers an unchanged listing equal to the answer before it', async () => {
    answerWith([sintel, spring], { sp: [spring] });
    const load = createTorrentListLoader();

    expect(await load('')).toEqual(await load(''));
    expect(await load('sp')).toEqual(await load('sp'));
  });

  it('orders its answers and its known list by request', async () => {
    answerWith([sintel, spring], { sp: [spring] });
    const load = createTorrentListLoader();

    const full = await load('');
    expect(load.seqOf(full)).toBe(load.known().seq);
    const search = await load('sp');
    expect(load.seqOf(search)).toBeGreaterThan(load.known().seq);
    expect(load.seqOf({ ...search })).toBe(0);
    expect(load.seqOf(undefined)).toBe(0);
  });

  it('does not fetch the full list for a search requested before the known list', async () => {
    const server = answerWith([sintel]);
    const load = createTorrentListLoader();
    await load('');
    let answerSearch: () => void = () => {};
    server.get.mockImplementationOnce(() => new Promise(resolve => {
      answerSearch = () => resolve(response([sintel], { categories: [], total: 1, version: versionOf([sintel]) }) as never);
    }));
    const search = load('s');
    // Spring is added and a refresh answers with it before the search does.
    server.all = [sintel, spring];
    await load('');
    server.get.mockClear();

    answerSearch();
    await search;

    expect(endpoints(server)).toEqual([]);
    expect(load.known().torrents).toEqual([sintel, spring]);
  });

  it('answers a search made before the full list was known after its first fetch', async () => {
    const server = answerWith([sintel]);
    const answers: Array<() => void> = [];
    server.get.mockImplementation(async (endpoint: string) => {
      const all = server.all;
      const facets = { categories: [], total: all.length, version: versionOf(all) };
      if (endpoint.includes('search=')) {
        return response([], facets) as never;
      }
      return new Promise(resolve => answers.push(() => resolve(response(all, facets) as never)));
    });
    const load = createTorrentListLoader();

    const first = load('s');
    // Spring is added and a second search reports it while the seed is in
    // flight, so the seed fetches the full list twice.
    server.all = [sintel, spring];
    const second = load('sp');
    await vi.waitFor(() => expect(answers).toHaveLength(1));
    answers[0]();

    expect((await first).torrents).toEqual([sintel]);
    expect((await second).torrents).toEqual([sintel]);
    await vi.waitFor(() => expect(answers).toHaveLength(2));
    answers[1]();
    await vi.waitFor(() => expect(load.known().torrents).toEqual([sintel, spring]));
  });

  it('does not fetch again for a search a newer full list supersedes', async () => {
    const server = answerWith([sintel]);
    const load = createTorrentListLoader();
    await load('');
    const pending: Record<string, () => void> = {};
    server.get.mockImplementation(async (endpoint: string) => {
      const all = server.all;
      const facets = { categories: [], total: all.length, version: versionOf(all) };
      const key = endpoint.includes('search=') ? 'search' : !pending.refetch ? 'refetch' : 'cleared';
      return new Promise(resolve => {
        pending[key] = () => resolve(response(endpoint.includes('search=') ? [] : all, facets) as never);
      });
    });

    // A search reports the version with Spring and starts a refetch.
    server.all = [sintel, spring];
    const first = load('s');
    pending.search();
    await first;
    await vi.waitFor(() => expect(pending.refetch).toBeDefined());
    // While the refetch is in flight, a later search reports the version with
    // Tears of Steel, and a cleared search then answers a newer full list.
    server.all = [sintel, spring, tearsOfSteel];
    const second = load('s');
    pending.search();
    await second;
    server.all = [spring, tearsOfSteel];
    const cleared = load('');
    pending.cleared();
    await cleared;
    server.get.mockClear();

    pending.refetch();

    await vi.waitFor(() => expect(load.known().torrents).toEqual([spring, tearsOfSteel]));
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(endpoints(server)).toEqual([]);
  });

  it('seeds a search with the full list already loading', async () => {
    const server = answerWith([sintel, spring], { sp: [spring] });
    const load = createTorrentListLoader();

    const full = load('');
    const search = await load('sp');
    await full;

    expect(search.torrents).toEqual([sintel, spring]);
    expect(endpoints(server)).toEqual(['/api/v1/torrents', '/api/v1/torrents?search=sp&fuzzy=true']);
  });

  it('does not refetch for a search answered before the full list already loading', async () => {
    const server = answerWith([sintel, spring], { sp: [spring] });
    let answerFull: () => void = () => {};
    server.get.mockImplementationOnce(() => new Promise(resolve => {
      answerFull = () => resolve(response([sintel, spring]) as never);
    }));
    const load = createTorrentListLoader();

    const full = load('');
    const search = load('sp');
    await vi.waitFor(() => expect(endpoints(server)).toHaveLength(2));
    answerFull();

    expect((await search).torrents).toEqual([sintel, spring]);
    await full;
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(endpoints(server)).toEqual(['/api/v1/torrents', '/api/v1/torrents?search=sp&fuzzy=true']);
  });

  it('aborts a refetch in flight when a cleared search loads the full list', async () => {
    const server = answerWith([sintel], { s: [sintel, spring] });
    const load = createTorrentListLoader();
    await load('');
    server.all = [sintel, spring];
    let refetchSignal: AbortSignal | undefined;
    server.get.mockImplementationOnce(async () => response([sintel, spring]) as never);
    server.get.mockImplementationOnce((_endpoint: string, options?: { signal?: AbortSignal | null }) => {
      refetchSignal = options?.signal ?? undefined;
      return new Promise((_resolve, reject) => {
        refetchSignal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
      });
    });

    // The search reports the version with Spring and starts a refetch.
    await load('s');
    await vi.waitFor(() => expect(refetchSignal).toBeDefined());
    expect(refetchSignal?.aborted).toBe(false);

    const cleared = await load('');

    expect(refetchSignal?.aborted).toBe(true);
    expect(cleared.torrents).toEqual([sintel, spring]);
    expect(load.known().torrents).toEqual([sintel, spring]);
    // A later search reporting the same version starts no refetch.
    server.get.mockClear();
    await load('s');
    expect(endpoints(server)).toEqual(['/api/v1/torrents?search=s&fuzzy=true']);
  });

  it('stops notifying an unsubscribed listener', async () => {
    answerWith([sintel]);
    const load = createTorrentListLoader();
    const changes = vi.fn();
    const unsubscribe = load.subscribe(changes);

    await load('');
    unsubscribe();
    await load('');

    expect(changes).toHaveBeenCalledTimes(1);
  });

  it('exposes the full list known from the newest answer', async () => {
    answerWith([sintel, spring], { sintel: [sintel] });
    const load = createTorrentListLoader();

    expect(load.known().torrents).toBeUndefined();
    await load('');
    expect(load.known().torrents).toEqual([sintel, spring]);
    await load('sintel');
    expect(load.known().torrents).toEqual([sintel, spring]);
  });

  it('does not let an older answer roll back a newer one', async () => {
    let answerOlder: () => void = () => {};
    const server = answerWith([sintel, spring, tearsOfSteel]);
    server.get.mockImplementationOnce(() => new Promise(resolve => {
      answerOlder = () => resolve(response([sintel]) as never);
    }));
    const load = createTorrentListLoader();
    const older = load('');
    await load('');

    answerOlder();
    expect((await older).torrents).toEqual([sintel]);

    expect(load.known().torrents).toEqual([sintel, spring, tearsOfSteel]);
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

  // internal/textsearch runs these cases too, so a change on either side that
  // alters these results fails until both agree.
  describe('cases shared with internal/textsearch', () => {
    const shared: {
      cases: { name: string, query: string, want: string[] }[],
      library: { name: string, title: string }[]
    } = JSON.parse(readFileSync(join(__dirname, '../../../internal/textsearch/testdata/shared-cases.json'), 'utf8'));
    const library = shared.library.map(({ name, title }) => torrent(name, { title: title || undefined }));

    it('has cases', () => {
      expect(shared.cases.length).toBeGreaterThan(0);
    });

    it.each(shared.cases)('$name', ({ query, want }) => {
      expect(names(library, query)).toEqual(want);
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

  it('matches the known torrents rather than a cached list until the server answers', () => {
    const cached = { matches: torrents, search: 's', torrents };
    const known = [torrents[1], torrents[2]];
    const visible = (query: string) => visibleTorrents(cached, { ...view, query }, known).map(t => t.hash);

    expect(visible('sint')).toEqual(['sintel-show']);
    // The server's answer to the list's own query still wins.
    expect(visible('s')).toEqual(['sintel', 'spring', 'sintel-show']);
    // Without a query the known torrents show until the full list answers.
    expect(visible('')).toEqual(['sintel-show', 'spring']);
  });

  it('shows the known torrents while a cleared search awaits the full list', () => {
    const searched = { matches: [torrents[0]], search: 'sintel', torrents };
    const known = [torrents[0], torrents[1]];

    expect(visibleTorrents(searched, view, known).map(t => t.hash)).toEqual(['spring', 'sintel']);
    expect(visibleTorrents(searched, view).map(t => t.hash)).toEqual(['sintel-show', 'spring', 'sintel']);
  });

  it('ranks the title matches as the server does until it answers', () => {
    const list = { search: '', torrents: [torrent('showcase', { createdAt: '2026-01-04', title: 'Showcase' }), ...torrents] };

    expect(hashes(list, { query: 'show' })).toEqual(['sintel-show', 'showcase']);
    expect(hashes(list, { query: 'show', searchSortBy: 'date' })).toEqual(['showcase', 'sintel-show']);
  });
});

describe('torrentTotal', () => {
  const torrents = [torrent('a'), torrent('b'), torrent('c')];
  const facets = { categories: [], total: 5, version: 'v1' };

  it('counts nothing before the first answer', () => {
    expect(torrentTotal(undefined, '')).toBeUndefined();
  });

  it('reads the facets for the full list and for a search', () => {
    expect(torrentTotal({ facets, search: '', torrents }, '')).toBe(5);
    expect(torrentTotal({ facets, search: 'a', torrents }, 'a')).toBe(5);
    expect(torrentTotal({ facets, search: 'a', torrents }, 'b')).toBe(5);
  });

  it('counts the shown torrents while a cleared search awaits the full list', () => {
    expect(torrentTotal({ facets, search: 'a', torrents }, '', [torrent('a')])).toBe(1);
    expect(torrentTotal({ facets, search: 'a', torrents }, '')).toBe(3);
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

describe('findTorrent', () => {
  const old = torrent('a', { title: 'Sintel' });
  const renamed = torrent('a', { title: 'Sintel 2010' });
  const added = torrent('b', { title: 'Spring' });

  it('prefers the matches of a search answered after the known list', () => {
    const list = { matches: [renamed, added], torrents: [old] };

    expect(findTorrent(list, 3, { seq: 2, torrents: [old] }, 'a')).toBe(renamed);
    expect(findTorrent(list, 3, { seq: 2, torrents: [old] }, 'b')).toBe(added);
  });

  it('prefers a known list fetched after a cached search answer', () => {
    const list = { matches: [old], torrents: [old] };

    expect(findTorrent(list, 2, { seq: 3, torrents: [renamed] }, 'a')).toBe(renamed);
  });

  it('falls back to the other source and then to the listed torrents', () => {
    expect(findTorrent({ matches: [added], torrents: [] }, 2, { seq: 3, torrents: [old] }, 'b')).toBe(added);
    expect(findTorrent({ torrents: [old] }, 1, { seq: 0 }, 'a')).toBe(old);
    expect(findTorrent({ torrents: [old] }, 0, { seq: 0, torrents: [renamed] }, 'a')).toBe(renamed);
    expect(findTorrent(undefined, 0, { seq: 0 }, 'a')).toBeUndefined();
  });
});
