// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import * as RovingFocusGroup from '@radix-ui/react-roving-focus';
import { useEffect, useLayoutEffect, useRef } from 'react';

import { TorrentCard } from '@/components/torrent-card';
import type { Torrent } from '@/lib/types/api';

interface TorrentGridProps {
  torrents: Torrent[],
  onEdit: (torrent: Torrent) => void,
  onViewStats: (torrent: Torrent) => void,
  onDelete: (torrent: Torrent) => void,
  onPlay: (torrent: Torrent) => void,
  onColumnCountChange?: (count: number) => void,
  onAddToDatabase: (torrent: Torrent) => void
}

export function TorrentGrid({
  torrents,
  onEdit,
  onViewStats,
  onDelete,
  onPlay,
  onColumnCountChange,
  onAddToDatabase
}: TorrentGridProps) {
  const gridRef = useRef<HTMLDivElement>(null);

  // Dynamically calculate and report the number of columns for the parent controller.
  useLayoutEffect(() => {
    const gridElement = gridRef.current;
    if (!gridElement) return;

    const updateColumnCount = () => {
      if (gridElement.isConnected) {
        const gridComputedStyle = window.getComputedStyle(gridElement);
        const colsStr = gridComputedStyle.getPropertyValue('grid-template-columns');
        const newColumnCount = colsStr ? colsStr.split(/\s+/).filter(Boolean).length : 1;
        onColumnCountChange?.(Math.max(1, newColumnCount));
      }
    };

    const animationFrameId = requestAnimationFrame(updateColumnCount);
    const resizeObserver = new ResizeObserver(() => requestAnimationFrame(updateColumnCount));
    resizeObserver.observe(gridElement);

    return () => {
      cancelAnimationFrame(animationFrameId);
      resizeObserver.disconnect();
    };
  }, [onColumnCountChange]);

  const hasInitiallyFocused = useRef(false);

  // On initial load only, focus the first card if nothing else on the page has focus.
  useEffect(() => {
    if (
      !hasInitiallyFocused.current &&
      torrents.length > 0 &&
      gridRef.current &&
      !gridRef.current.contains(document.activeElement) &&
      document.activeElement === document.body
    ) {
      hasInitiallyFocused.current = true;
      const firstItem = gridRef.current.querySelector<HTMLElement>('[data-radix-collection-item]');
      firstItem?.focus();
    }
  }, [torrents.length]);

  return (
    <RovingFocusGroup.Root asChild>
      <div
        ref={gridRef}
        className='grid grid-cols-2 xs:grid-cols-3 sm:grid-cols-4 md:grid-cols-5 lg:grid-cols-5 xl:grid-cols-6 2xl:grid-cols-7 3xl:grid-cols-8 gap-2 lg:gap-3 focus:outline-none'
      >
        {torrents.map(torrent => (
          <RovingFocusGroup.Item asChild
            key={torrent.hash}>
            <TorrentCard
              torrent={torrent}
              onEdit={onEdit}
              onViewStats={onViewStats}
              onDelete={onDelete}
              onPlayTorrent={onPlay}
              onAddToDatabase={onAddToDatabase}
            />
          </RovingFocusGroup.Item>
        ))}
      </div>
    </RovingFocusGroup.Root>
  );
}
