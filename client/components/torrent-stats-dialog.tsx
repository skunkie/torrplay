// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';

import { getTorrentStats } from '@/lib/api/stats';
import { HttpError } from '@/lib/api-client';
import { useLiveUpdates } from '@/lib/live-updates-context';
import type { Torrent, TorrentStats as TorrentStatsType } from '@/lib/types/api';

import { TorrentStatsDialogLayout } from './torrent-stats-dialog-layout';

interface TorrentStatsDialogProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void
}

export function TorrentStatsDialog({ torrent, open, onOpenChange }: TorrentStatsDialogProps) {
  const [stats, setStats] = useState<TorrentStatsType | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { liveUpdatesPaused } = useLiveUpdates();

  const loadStats = useCallback(async (isInitialLoad: boolean) => {
    if (!torrent) return;

    if (isInitialLoad) {
      setLoading(true);
    }
    setError(null);
    try {
      const data = await getTorrentStats(torrent.hash);
      setStats(data);
    } catch (err) {
      setStats(null); // Clear stats on error
      if (err instanceof HttpError) {
        setError(err.message);
      } else {
        setError(err instanceof Error ? err.message : 'Failed to load stats');
      }
    } finally {
      if (isInitialLoad) {
        setLoading(false);
      }
    }
  }, [torrent]);

  useEffect(() => {
    let interval: NodeJS.Timeout;
    if (open && torrent) {
      // Reset state when dialog is opened for a new torrent
      setStats(null);
      setError(null);

      loadStats(true);
      if (!liveUpdatesPaused) {
        interval = setInterval(() => loadStats(false), 2000);
      }
    }
    return () => {
      if (interval) {
        clearInterval(interval);
      }
    };
  }, [open, torrent, loadStats, liveUpdatesPaused]);

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
      stats={stats}
      loading={loading}
      error={error}
      handleCopy={handleCopy}
    />
  );
}
