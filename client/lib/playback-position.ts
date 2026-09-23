// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

const PLAYBACK_POSITIONS_STORAGE_KEY = 'torrplay_playback_positions';

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

export function getPlaybackPositionSeconds(hash: string, filePath: string): number {
  const position = readPlaybackPositions()[hash]?.[filePath];
  return typeof position === 'number' && isFinite(position) && position > 0 ? position : 0;
}

export function savePlaybackPositionSeconds(hash: string, filePath: string, positionSeconds: number): void {
  if (!hash || !filePath) return;
  const positions = readPlaybackPositions();
  if (!isFinite(positionSeconds) || positionSeconds <= 0) {
    if (!positions[hash]) return;
    delete positions[hash][filePath];
    if (Object.keys(positions[hash]).length === 0) delete positions[hash];
  } else {
    if (!positions[hash]) positions[hash] = {};
    positions[hash][filePath] = Math.round(positionSeconds);
  }
  writePlaybackPositions(positions);
}
