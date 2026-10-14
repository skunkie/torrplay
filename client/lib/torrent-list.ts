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

// Until the server has answered the current query, matching the query's parts
// against titles and names keeps the list following what is typed.
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
    matches ?? filterTorrentsByText(torrents, view.query),
    view.categoryFilter,
    view.searchSortBy,
  );
}

// A failed search stops waiting, so the substring match and the empty state
// show instead of a search that may never be answered.
export function isAwaitingSearch(
  list: Pick<TorrentList, 'search'> | undefined,
  query: string,
  hasFailed = false,
): boolean {
  return !hasFailed && query !== '' && list?.search !== query;
}

// Every whitespace-separated part of the query must occur in the title or the
// name, as with the server's terms, so word order and spacing don't matter;
// within a part, punctuation must match exactly.
export function filterTorrentsByText(torrents: Torrent[], query: string): Torrent[] {
  const parts = foldText(query).split(/\s+/).filter(Boolean);
  return torrents.filter(torrent => {
    const fields = [torrent.title, torrent.name].map(field => foldText(field ?? ''));
    return parts.every(part => fields.some(field => field.includes(part)));
  });
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
