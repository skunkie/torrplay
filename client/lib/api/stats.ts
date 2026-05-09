// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { api } from '../api-client';
import type { MemoryStats, TorrentStats } from '../types/api';

export async function getMemoryStats(): Promise<MemoryStats> {
  return api.get<MemoryStats>('/api/stats/memory');
}

export async function getTorrentStats(hash: string): Promise<TorrentStats> {
  return api.get<TorrentStats>(`/api/stats/torrents/${hash}`);
}
