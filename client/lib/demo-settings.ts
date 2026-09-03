// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { Settings, TorrentClient } from '@/lib/types/api';

export const demoDefaultSettings: Settings = {
  corsAllowedOrigins: [],
  auth: { enabled: false, type: 'basic' as const, username: '', password: '' },
  enableDlna: false,
  enableDownloader: false,
  enableStremio: false,
  stremioToken: 'demo-stremio-token',
  fileStoragePath: '',
  friendlyName: 'TorrPlay',
  logLevel: 'INFO',
  logFormat: 'text' as const,
  maxMemory: 536870912,
  torrentClient: {
    disableDht: false,
    disableIpv6: true,
    disablePex: false,
    disableTcp: false,
    disableUtp: false,
    downloadRateLimit: 0,
    establishedConnsPerTorrent: 50,
    halfOpenConnsPerTorrent: 25,
    maxAllocPeerRequestDataPerConn: 1048576,
    preferHeaderObfuscation: false,
    seed: false,
    torrentPeersHighWater: 500,
    torrentPeersLowWater: 50,
    totalHalfOpenConns: 100,
    uploadRateLimit: 3145728,
  } as TorrentClient,
  torrentTrackers: [
    'udp://explodie.org:6969',
    'udp://tracker.leechers-paradise.org:6969,udp://tracker.opentrackr.org:1337',
  ],
};
