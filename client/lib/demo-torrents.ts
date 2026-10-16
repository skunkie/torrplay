// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import type { Torrent, TorrentFacets, TorrentsResponse } from '@/lib/types/api';

// demoTorrentFacets counts torrents and the torrents in each category, ordered
// by category name and leaving out torrents without a category, and versions
// them, as the server does for GET /api/v1/torrents.
export function demoTorrentFacets(torrents: Torrent[]): TorrentFacets {
  const counts = new Map<string, number>();
  for (const torrent of torrents) {
    if (torrent.category) {
      counts.set(torrent.category, (counts.get(torrent.category) ?? 0) + 1);
    }
  }
  const categories = [...counts]
    .sort(([a], [b]) => compareCodePoints(a, b))
    .map(([name, count]) => ({ count, name }));
  return { categories, total: torrents.length, version: demoTorrentsVersion(torrents) };
}

// compareCodePoints orders strings by code point, as Go orders them by UTF-8
// bytes, where comparing UTF-16 code units would put characters beyond U+FFFF
// before those from U+E000 to U+FFFF.
function compareCodePoints(a: string, b: string): number {
  const as = [...a];
  const bs = [...b];
  for (let i = 0; i < Math.min(as.length, bs.length); i++) {
    const diff = as[i].codePointAt(0)! - bs[i].codePointAt(0)!;
    if (diff !== 0) {
      return diff;
    }
  }
  return as.length - bs.length;
}

// demoTorrentsVersion hashes the JSON encoding of torrents with 32-bit FNV-1a,
// so it changes whenever a field of any of them does, as the server's version.
function demoTorrentsVersion(torrents: Torrent[]): string {
  let hash = 0x811c9dc5;
  for (const char of JSON.stringify(torrents)) {
    hash = Math.imul(hash ^ char.codePointAt(0)!, 0x01000193) >>> 0;
  }
  return hash.toString(36);
}

// demoTorrentsResponse answers an unfiltered GET /api/v1/torrents.
export function demoTorrentsResponse(torrents: Torrent[]): TorrentsResponse {
  return { facets: demoTorrentFacets(torrents), limit: 0, offset: 0, torrents, total: torrents.length };
}
