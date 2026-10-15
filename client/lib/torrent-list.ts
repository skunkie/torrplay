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

// The full list is fetched alongside the matches because the category filter
// and the torrent count describe every torrent, not only the matching ones.
export async function getTorrentList(search: string): Promise<TorrentList> {
  const [all, found] = await Promise.all([
    getTorrents({}),
    search ? getTorrents({ fuzzy: true, search }) : undefined,
  ]);
  return { ...all, matches: found?.torrents, search };
}

// Until the server has answered the current query, matching titles and names
// as the server does keeps the list following what is typed, so the shown
// torrents keep their order when it answers.
export function visibleTorrents(
  list: Pick<TorrentList, 'matches' | 'search' | 'torrents'> | undefined,
  view: TorrentListView,
): Torrent[] {
  const torrents = list?.torrents ?? [];
  if (!view.query) {
    return filterAndSortTorrents(torrents, view.categoryFilter, view.sortBy);
  }
  const matches = isAwaitingSearch(list, view.query) ? undefined : list?.matches;
  return filterAndSortTorrents(
    matches ?? searchTorrentsByText(torrents, view.query),
    view.categoryFilter,
    view.searchSortBy,
  );
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
    const parts = foldText(query).split(/\s+/).filter(Boolean);
    return torrents.filter(torrent => {
      const fields = [torrent.title, torrent.name].map(field => foldText(field ?? ''));
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

// foldText ignores case, accents, and compatibility forms such as full-width
// letters, and reads a typographic apostrophe as a plain one, as the server does.
// Lowercasing alone misses the case folding of a sharp s and a final sigma.
function foldText(text: string): string {
  return text
    .normalize('NFKD')
    .replace(/\p{Mn}/gu, '')
    .replace(/’/g, "'")
    .toLowerCase()
    .replace(/ß/g, 'ss')
    .replace(/ς/g, 'σ');
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
