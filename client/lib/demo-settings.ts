// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { Settings, TorrentClient } from '@/lib/types/api';

export const demoDefaultSettings: Settings = {
  auth: { enabled: false, type: 'basic' as const, username: '', password: '' },
  enableDlna: false,
  enableDownloader: false,
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
    preferHeaderObfuscation: false,
    seed: false,
    torrentPeersHighWater: 500,
    uploadRateLimit: 3145728,
  } as TorrentClient,
  torrentTrackers: [
    'udp://explodie.org:6969',
    'udp://tracker.leechers-paradise.org:6969,udp://tracker.opentrackr.org:1337',
  ],
};
