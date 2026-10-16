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

export interface TorrentListView {
  categoryFilter: string,
  query: string,
  searchSortBy: TorrentListSortBy,
  sortBy: SortByOption
}

// TorrentListLoader loads the torrent list for a search.
export interface TorrentListLoader {
  (search: string): Promise<TorrentList>,
  // known returns the torrents known from the newest answer, which a cached
  // answer to an earlier search may predate.
  known: () => Torrent[] | undefined
}

// createTorrentListLoader returns a loader that answers both the list and the
// search with one request: while searching, the response holds only the
// matches, and its facets describe every torrent for the category filter and
// the torrent count. The loader remembers the torrents known from the newest
// answer, refreshed with each search's matches, so a newly typed query can be
// matched locally until the server answers it. A search opened before any
// full list was known also fetches the full list, once.
export function createTorrentListLoader(): TorrentListLoader {
  let known: Torrent[] | undefined;
  let started = 0;
  let accepted = 0;
  const load = async (search: string) => {
    const id = ++started;
    // A failed seed leaves the full list unknown, to be fetched again with the
    // next search, rather than failing a search the server did answer.
    const [res, seed] = await Promise.all([
      getTorrents(search ? { fuzzy: true, search } : {}),
      search && !known ? getTorrents({}).catch(() => null) : undefined,
    ]);
    const torrents = search
      ? mergeMatches(seed?.torrents ?? known ?? [], res.torrents, search)
      : res.torrents;
    // An answer to an older request must not roll back a newer one.
    if (id > accepted && seed !== null) {
      accepted = id;
      known = torrents;
    }
    return search ? { ...res, matches: res.torrents, search, torrents } : { ...res, search };
  };
  return Object.assign(load, { known: () => known });
}

// mergeMatches refreshes known torrents with the server's matches for search
// and appends the matches that were not known. Every torrent the local match
// finds is one the server matches too, so a saved torrent the local match finds
// but the server did not return is gone, and is dropped. A torrent merely
// loaded in the client is kept, since the server leaves one out while its
// metadata is still resolving.
function mergeMatches(known: Torrent[], matches: Torrent[], search: string): Torrent[] {
  const byHash = new Map(matches.map(torrent => [torrent.hash, torrent]));
  const gone = new Set(searchTorrentsByText(known, search)
    .filter(torrent => torrent.createdAt !== undefined && !byHash.has(torrent.hash))
    .map(torrent => torrent.hash));
  const knownHashes = new Set(known.map(torrent => torrent.hash));
  return [
    ...known.filter(torrent => !gone.has(torrent.hash)).map(torrent => byHash.get(torrent.hash) ?? torrent),
    ...matches.filter(torrent => !knownHashes.has(torrent.hash)),
  ];
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
