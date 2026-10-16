// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { getTorrents } from '@/lib/api/torrents';
import type { SortByOption } from '@/lib/torrent-filter-storage';
import type { Torrent, TorrentsResponse } from '@/lib/types/api';

export type TorrentListSortBy = SortByOption | 'relevance';

export interface TorrentList extends TorrentsResponse {
  matches?: Torrent[],
  search: string
}

// KnownTorrents is the full list known from the newest answer, with the seq
// that orders the request that fetched it among the loader's requests, or 0
// before any has.
export interface KnownTorrents {
  seq: number,
  torrents?: Torrent[]
}

export interface TorrentListView {
  categoryFilter: string,
  query: string,
  searchSortBy: TorrentListSortBy,
  sortBy: SortByOption
}

// TorrentListLoader loads the torrent list for a search.
export interface TorrentListLoader {
  (search: string): Promise<TorrentList>,
  // known returns the full list known from the newest answer, which a cached
  // answer to an earlier search may predate. It returns the same object until
  // that list changes.
  known: () => KnownTorrents,
  // seqOf returns the seq of the request list answered, or 0 for a list the
  // loader did not return. Answers carry no seq of their own, so an unchanged
  // answer stays equal to the one before it.
  seqOf: (list: TorrentList | undefined) => number,
  // subscribe calls listener whenever known changes, and returns a function
  // that stops it.
  subscribe: (listener: () => void) => () => void
}

// createTorrentListLoader returns a loader that answers both the list and the
// search with one request: while searching, the response holds only the
// matches, and its facets describe every torrent for the category filter and
// the torrent count. The loader remembers the full list from the newest
// answer, so a newly typed query can be matched locally until the server
// answers it. The facets' version tells whether that list is still current;
// when a search requested after it reports another version, the loader
// fetches the full list again in the background, one fetch at a time, and
// answers the search at once. A search made before any full list is known
// waits for one.
export function createTorrentListLoader(): TorrentListLoader {
  let known: KnownTorrents = { seq: 0 };
  let knownVersion: string | undefined;
  // accepted is the seq of the newest full list answered, which may have
  // kept known unchanged.
  let accepted = 0;
  let started = 0;
  // reported is the version the newest answered search reported, and
  // reportedSeq the seq of that search.
  let reported: string | undefined;
  let reportedSeq = 0;
  // fetching settles with the full-list fetch of the refetch in flight, and
  // loadingFull with the newest load of the full list in flight.
  let fetching: Promise<void> | undefined;
  let loadingFull: Promise<void> | undefined;
  // refetchAbort aborts the full-list fetch of the refetch in flight.
  let refetchAbort: AbortController | undefined;
  const seqs = new WeakMap<TorrentList, number>();
  const listeners = new Set<() => void>();

  // accept keeps full as the known list unless a later request has answered.
  // A full list of the known version keeps the known one, so an unchanged
  // refresh notifies no listener and leaves the view to its memoized torrents.
  const accept = (seq: number, full: TorrentsResponse) => {
    if (seq <= accepted) {
      return;
    }
    accepted = seq;
    if (known.torrents && full.facets.version === knownVersion) {
      return;
    }
    known = { seq, torrents: full.torrents };
    knownVersion = full.facets.version;
    listeners.forEach(listener => listener());
  };

  // fetchFull fetches and accepts the full list under the seq it takes when
  // it starts, so a full list requested earlier but answered later cannot
  // roll it back.
  const fetchFull = async () => {
    const seq = ++started;
    const abort = new AbortController();
    refetchAbort = abort;
    try {
      accept(seq, await getTorrents({}, { signal: abort.signal }));
    } finally {
      if (refetchAbort === abort) {
        refetchAbort = undefined;
      }
    }
  };

  // refetch fetches the full list unless a fetch is in flight, and returns a
  // promise that settles with the fetch in flight. A search that started after
  // the newest full list answered and reports a version other than that
  // list's is fetched for again, so the known list catches up without waiting
  // for another search. A failed fetch leaves the known list as it is, to be
  // fetched again when the next search reports another version.
  const refetch = (): Promise<void> => {
    if (!fetching) {
      void (async () => {
        try {
          do {
            const current = fetchFull();
            fetching = current.then(() => undefined, () => undefined);
            await current;
          } while (reportedSeq > accepted && reported !== knownVersion);
        } catch {
          // The next search that reports another version retries.
        } finally {
          fetching = undefined;
        }
      })();
    }
    // The refetch sets fetching before its first await.
    return fetching!;
  };

  const load = async (search: string): Promise<TorrentList> => {
    const seq = ++started;
    let list: TorrentList;
    if (!search) {
      // This load answers a newer full list than a refetch in flight, which
      // it would only roll back, so it aborts that refetch rather than
      // downloading the full list twice. It does not reuse the refetch's
      // answer, which may predate a write the caller revalidates after.
      refetchAbort?.abort();
      const answer = getTorrents({}).then(res => {
        accept(seq, res);
        return res;
      });
      const settled = answer.then(() => undefined, () => undefined);
      loadingFull = settled;
      void settled.then(() => {
        if (loadingFull === settled) {
          loadingFull = undefined;
        }
      });
      list = { ...await answer, search };
    } else {
      const request = getTorrents({ fuzzy: true, search });
      // Without a known list, the next typed query has nothing to match
      // locally, so the search waits for the full list loading or fetched
      // alongside it.
      const seeding = known.torrents ? undefined : loadingFull ?? refetch();
      const res = await request;
      if (seq > reportedSeq) {
        reportedSeq = seq;
        reported = res.facets.version;
      }
      // The seed decides whether the search reports a newer version, and a
      // search requested before the known list reports none. A failed seed
      // is retried by the next search rather than at once.
      await seeding;
      if (known.torrents && seq > accepted && res.facets.version !== knownVersion) {
        void refetch();
      }
      list = { ...res, matches: res.torrents, search, torrents: known.torrents ?? res.torrents };
    }
    seqs.set(list, seq);
    return list;
  };

  const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  };
  return Object.assign(load, {
    known: () => known,
    seqOf: (list: TorrentList | undefined) => (list && seqs.get(list)) ?? 0,
    subscribe,
  });
}

// findTorrent finds the torrent of hash in list, answered for the request
// listSeq, or known, looking first in whichever was requested later: the
// matches of a search answered after known may hold a torrent added since,
// while known fetched after a cached answer to an earlier search holds the
// newer fields.
export function findTorrent(
  list: Pick<TorrentList, 'matches' | 'torrents'> | undefined,
  listSeq: number,
  known: KnownTorrents,
  hash: string,
): Torrent | undefined {
  const byHash = (torrent: Torrent) => torrent.hash === hash;
  const inMatches = () => list?.matches?.find(byHash);
  const inKnown = () => known.torrents?.find(byHash);
  const [first, second] = listSeq > known.seq ? [inMatches, inKnown] : [inKnown, inMatches];
  return first() ?? second() ?? list?.torrents.find(byHash);
}

// Until the server has answered the current query, matching titles and names
// as the server does keeps the list following what is typed, so the shown
// torrents keep their order when it answers. Until then the view also draws on
// known, the torrents known from the newest answer, when given, since list may
// be a cached answer to an earlier search: the local match runs over known,
// and a cleared search shows known until the full list answers.
export function visibleTorrents(
  list: Pick<TorrentList, 'matches' | 'search' | 'torrents'> | undefined,
  view: TorrentListView,
  known?: Torrent[],
): Torrent[] {
  const torrents = list?.torrents ?? [];
  if (!view.query) {
    return filterAndSortTorrents(listedTorrents(list, known), view.categoryFilter, view.sortBy);
  }
  const matches = isAwaitingSearch(list, view.query) ? undefined : list?.matches;
  return filterAndSortTorrents(
    matches ?? searchTorrentsByText(known ?? torrents, view.query),
    view.categoryFilter,
    view.searchSortBy,
  );
}

// torrentTotal returns the number of all torrents, which the count label
// compares the shown torrents with: the facets' total, except while a cleared
// search awaits the full list, when the known torrents shown are counted, so
// the label does not read as filtered.
export function torrentTotal(
  list: Pick<TorrentList, 'facets' | 'search' | 'torrents'> | undefined,
  query: string,
  known?: Torrent[],
): number | undefined {
  if (!list) {
    return undefined;
  }
  return !query && list.search ? listedTorrents(list, known).length : list.facets.total;
}

// listedTorrents returns the torrents an unfiltered view shows: those of the
// full list, or the known ones while a cleared search awaits it.
function listedTorrents(list: Pick<TorrentList, 'search' | 'torrents'> | undefined, known?: Torrent[]): Torrent[] {
  const torrents = list?.torrents ?? [];
  return list?.search ? known ?? torrents : torrents;
}

// A failed search stops waiting, so the local match and the empty state
// show instead of a search that may never be answered.
export function isAwaitingSearch(
  list: Pick<TorrentList, 'search'> | undefined,
  query: string,
  hasFailed = false,
): boolean {
  return !hasFailed && query !== '' && list?.search !== query;
}

// Match qualities as internal/textsearch scores them.
const MATCH_INSIDE_WORD = 2;
const MATCH_WORD_START = 3;
const MATCH_WHOLE_WORD = 4;

// searchTorrentsByText matches and orders torrents as internal/textsearch does
// without typo tolerance. Every word of the query must match a whole word, the
// start of a word, or text inside a word of the title or the name, and the best
// matches by summed quality come first, keeping the given order among equals.
// A query of punctuation alone keeps, in the given order, the torrents whose
// title or name contains each of its whitespace-separated parts. For the same
// list, the server's answer then keeps the shown torrents in the same order and
// adds only its typo matches, after them.
export function searchTorrentsByText(torrents: Torrent[], query: string): Torrent[] {
  const terms = splitWords(query);
  if (terms.length === 0) {
    const parts = foldTextParts(query).split(SPACES).filter(Boolean);
    return torrents.filter(torrent => {
      const fields = [torrent.title, torrent.name].map(field => foldTextParts(field ?? ''));
      return parts.every(part => fields.some(field => field.includes(part)));
    });
  }
  return torrents
    .map(torrent => ({ score: scoreTerms(terms, splitWords(`${torrent.title ?? ''} ${torrent.name ?? ''}`)), torrent }))
    .filter(({ score }) => score > 0)
    .sort((a, b) => b.score - a.score)
    .map(({ torrent }) => torrent);
}

// scoreTerms sums the quality of each term's best match in words, or returns 0
// when a term matches none of them.
function scoreTerms(terms: string[], words: string[]): number {
  const joined = words.join(' ');
  let score = 0;
  for (const term of terms) {
    if (words.includes(term)) {
      score += MATCH_WHOLE_WORD;
    } else if (words.some(word => word.startsWith(term))) {
      score += MATCH_WORD_START;
    } else if (joined.includes(term)) {
      score += MATCH_INSIDE_WORD;
    } else {
      return 0;
    }
  }
  return score;
}

// splitWords splits folded text into words of letters and digits as the server
// does, dropping apostrophes, so "Sintel.2010" reads "sintel 2010" and
// "Llama's" reads "llamas".
function splitWords(text: string): string[] {
  return foldText(text).replace(/'/g, '').split(/[^\p{L}\p{Nd}]+/u).filter(Boolean);
}

// SPACES matches runs of the characters Go's unicode.IsSpace accepts, which
// split a query of punctuation alone on the server. JavaScript's \s also
// accepts U+FEFF but not U+0085.
const SPACES = /[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+/;

// foldTextParts folds text matched as plain text, for a query of punctuation
// alone, as the server does: it ignores case and compatibility forms such as
// full-width punctuation, and reads a typographic apostrophe as a plain one,
// but keeps accents, so a lone combining mark matches only itself.
function foldTextParts(text: string): string {
  return foldIotaSubscripts(text)
    .toLowerCase()
    .replace(/ß/g, 'ss')
    .replace(/ς/g, 'σ')
    .normalize('NFKC')
    .replace(/’/g, "'");
}

// foldText ignores case, accents, and compatibility forms such as full-width
// letters, and reads a typographic apostrophe as a plain one, as the server does.
// Lowercasing alone misses the case folding of a sharp s, a final sigma, and
// a Greek iota subscript.
function foldText(text: string): string {
  return foldIotaSubscripts(text)
    .normalize('NFKD')
    .replace(/\p{Mn}/gu, '')
    .replace(/’/g, "'")
    .toLowerCase()
    .replace(/ß/g, 'ss')
    .replace(/ς/g, 'σ');
}

// foldIotaSubscripts reads a Greek iota subscript, alone or in a precomposed
// letter such as "ᾳ", as the iota it case-folds to, so "ᾳ" reads "αι" as on the
// server. It is the only combining mark that case folding changes.
function foldIotaSubscripts(text: string): string {
  return text.normalize('NFD').replace(/\u0345/g, 'ι');
}

export function filterAndSortTorrents(
  torrents: Torrent[],
  categoryFilter: string,
  sortBy: TorrentListSortBy,
): Torrent[] {
  const filtered = torrents.filter(torrent => !categoryFilter || (torrent.category || '') === categoryFilter);
  if (sortBy === 'relevance') {
    return filtered;
  }
  return filtered.sort((a, b) => {
    const aInDb = a.createdAt !== undefined;
    const bInDb = b.createdAt !== undefined;

    if (aInDb !== bInDb) {
      return aInDb ? 1 : -1;
    }
    switch (sortBy) {
      case 'name':
        return (a.title || a.name || '').localeCompare(b.title || b.name || '');
      case 'size':
        return (b.totalSize || 0) - (a.totalSize || 0);
      case 'updated':
        return new Date(b.updatedAt || 0).getTime() - new Date(a.updatedAt || 0).getTime();
      case 'date':
      default:
        return new Date(b.createdAt || 0).getTime() - new Date(a.createdAt || 0).getTime();
    }
  });
}
