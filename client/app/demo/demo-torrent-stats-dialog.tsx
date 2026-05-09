// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useMemo } from 'react';
import { toast } from 'sonner';

import { TorrentStatsDialogLayout } from '@/components/torrent-stats-dialog-layout';
import type { Torrent, TorrentStats } from '@/lib/types/api';

interface DemoTorrentStatsDialogProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void,
  stats: Record<string, TorrentStats>
}

export function DemoTorrentStatsDialog({ torrent, open, onOpenChange, stats }: DemoTorrentStatsDialogProps) {
  const torrentStats = useMemo(() => {
    if (!torrent) return null;
    return stats[torrent.hash] || null;
  }, [torrent, stats]);

  if (!open || !torrent || !torrentStats) return null;

  const handleCopy = (value: string, label: string) => {
    navigator.clipboard.writeText(value).then(() => {
      toast.success(`${label} copied to clipboard`);
    }).catch(err => {
      toast.error('Failed to copy', {
        description: err instanceof Error ? err.message : 'Could not copy to clipboard.',
      });
    });
  };

  return (
    <TorrentStatsDialogLayout
      torrent={torrent}
      open={open}
      onOpenChange={onOpenChange}
      stats={torrentStats}
      loading={false}
      error={null}
      handleCopy={handleCopy}
    />
  );
}
