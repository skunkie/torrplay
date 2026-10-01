// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { LogEntry } from '@/lib/types/api';

export const demoLogs: LogEntry[] = [
  { time: '2026-09-22T10:42:18Z', level: 'ERROR', message: 'failed to load torrent metadata', data: { hash: 'd8a4c3e91f2b7a60', error: 'metadata request timed out' } },
  { time: '2026-09-22T10:41:53Z', level: 'INFO', message: 'starting HTTP server', data: { address: ':8090' } },
  { time: '2026-09-22T10:40:07Z', level: 'WARN', message: 'tracker request timed out', data: { tracker: 'udp://tracker.example.org:6969' } },
  { time: '2026-09-22T10:39:34Z', level: 'DEBUG', message: 'request completed', data: { path: '/api/v1/torrents', status: 200 } },
];
