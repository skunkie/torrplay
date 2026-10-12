// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

const PLAYBACK_POSITIONS_STORAGE_KEY = 'torrplay_playback_positions';

// MAX_PLAYBACK_POSITION_TORRENTS is how many torrents keep saved positions.
// Saving a position makes its torrent the most recent, and the least recently
// saved torrents beyond the limit are forgotten.
const MAX_PLAYBACK_POSITION_TORRENTS = 100;

// PlaybackPositionKey identifies the torrent file a playback position belongs to.
export interface PlaybackPositionKey {
  hash: string,
  filePath: string
}

type PlaybackPositions = Record<string, Record<string, number>>;

function readPlaybackPositions(): PlaybackPositions {
  if (typeof window === 'undefined') return {};
  try {
    const raw = window.localStorage.getItem(PLAYBACK_POSITIONS_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};

    const positions: PlaybackPositions = {};
    for (const [hash, storedFiles] of Object.entries(parsed)) {
      if (!storedFiles || typeof storedFiles !== 'object' || Array.isArray(storedFiles)) continue;
      for (const [filePath, position] of Object.entries(storedFiles)) {
        if (typeof position !== 'number' || !isFinite(position) || position <= 0) continue;
        if (!positions[hash]) positions[hash] = {};
        positions[hash][filePath] = position;
      }
    }
    return positions;
  } catch {
    return {};
  }
}

function writePlaybackPositions(positions: PlaybackPositions): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(PLAYBACK_POSITIONS_STORAGE_KEY, JSON.stringify(positions));
  } catch {}
}

// getPlaybackPositionSeconds returns the saved playback position of a torrent
// file in seconds, or 0 when none is saved.
export function getPlaybackPositionSeconds(hash: string, filePath: string): number {
  const position = readPlaybackPositions()[hash]?.[filePath];
  return typeof position === 'number' && isFinite(position) && position > 0 ? position : 0;
}

// savePlaybackPositionSeconds saves the playback position of a torrent file,
// rounded to whole seconds. A position that is not positive clears it.
export function savePlaybackPositionSeconds(hash: string, filePath: string, positionSeconds: number): void {
  if (!hash || !filePath) return;
  const positions = readPlaybackPositions();
  if (!isFinite(positionSeconds) || positionSeconds <= 0) {
    if (!positions[hash]) return;
    delete positions[hash][filePath];
    if (Object.keys(positions[hash]).length === 0) delete positions[hash];
  } else {
    // Object keys keep their insertion order, info hashes never being array
    // indexes, so reinserting the torrent makes it the most recent.
    const files = positions[hash] ?? {};
    delete positions[hash];
    files[filePath] = Math.round(positionSeconds);
    positions[hash] = files;
    const hashes = Object.keys(positions);
    for (const stale of hashes.slice(0, Math.max(hashes.length - MAX_PLAYBACK_POSITION_TORRENTS, 0))) {
      delete positions[stale];
    }
  }
  writePlaybackPositions(positions);
}
