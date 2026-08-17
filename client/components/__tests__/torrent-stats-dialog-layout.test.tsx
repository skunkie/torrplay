// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { TorrentStatsDialogLayout } from '@/components/torrent-stats-dialog-layout';
import type { Torrent, TorrentStats } from '@/lib/types/api';

const mockTorrent: Torrent = {
  active: true,
  category: 'movies',
  completed: false,
  createdAt: '2026-08-17T00:00:00Z',
  downloadSpeed: 1024 * 1024,
  files: [
    { id: 1, length: 100 * 1024 * 1024, path: 'movie.mp4', viewedAt: null },
  ],
  hash: 'abc123hash',
  magnet: 'magnet:?xt=urn:btih:abc123hash',
  poster: '/poster.jpg',
  storage: 'memory',
  title: 'Sample Test Movie',
  totalPeers: 50,
  totalSize: 100 * 1024 * 1024,
  updatedAt: '2026-08-17T00:00:00Z',
  uploadSpeed: 512 * 1024,
};

const mockStats: TorrentStats = {
  activePeers: 15,
  bytesHashed: 50 * 1024 * 1024,
  bytesRead: 10 * 1024 * 1024,
  bytesReadData: 10 * 1024 * 1024,
  bytesReadUsefulData: 10 * 1024 * 1024,
  bytesReadUsefulIntendedData: 10 * 1024 * 1024,
  bytesWritten: 50 * 1024 * 1024,
  bytesWrittenData: 50 * 1024 * 1024,
  chunksRead: 100,
  chunksReadUseful: 95,
  chunksReadWasted: 5,
  chunksWritten: 100,
  completedSize: 50 * 1024 * 1024,
  connectedSeeders: 10,
  halfOpenPeers: 2,
  inMemory: 50,
  inMemorySize: 50 * 1024 * 1024,
  memoryStats: {
    activeTorrents: 1,
    maxMemory: 500 * 1024 * 1024,
    totalPieces: 100,
    usedMemory: 50 * 1024 * 1024,
  },
  memoryUsagePercentage: 10.0,
  metadataChunksRead: 10,
  pendingPeers: 5,
  pieces: [
    { complete: true, inMemory: true, index: 0, size: 1024 * 1024 },
    { complete: false, inMemory: false, index: 1, size: 1024 * 1024 },
  ],
  piecesComplete: 1,
  piecesDirtiedBad: 0,
  piecesDirtiedGood: 1,
  readers: [
    { end: 15, reader: 5, start: 4 },
    { end: 30, reader: 20, start: 19 },
  ],
  totalPeers: 50,
  totalPieces: 100,
  totalSize: 100 * 1024 * 1024,
};

describe('TorrentStatsDialogLayout', () => {
  const mockOnOpenChange = vi.fn();
  const mockHandleCopy = vi.fn();

  it('renders nothing when torrent is null', () => {
    const { container } = render(
      <TorrentStatsDialogLayout
        torrent={null}
        open={true}
        onOpenChange={mockOnOpenChange}
        stats={null}
        loading={false}
        error={null}
        handleCopy={mockHandleCopy}
      />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders dialog with title and magnet copy button when open', () => {
    render(
      <TorrentStatsDialogLayout
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        stats={mockStats}
        loading={false}
        error={null}
        handleCopy={mockHandleCopy}
      />,
    );

    expect(screen.getByText('Statistics')).toBeInTheDocument();
    expect(screen.getByText('Sample Test Movie')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Magnet Link/i })).toBeInTheDocument();
  });

  it('renders Active Readers section with pieces range', () => {
    render(
      <TorrentStatsDialogLayout
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        stats={mockStats}
        loading={false}
        error={null}
        handleCopy={mockHandleCopy}
      />,
    );

    expect(screen.getByText('Active Readers')).toBeInTheDocument();
    expect(screen.getByText('piece 5')).toBeInTheDocument();
    expect(screen.getByText('pieces 4 – 15')).toBeInTheDocument();
    expect(screen.getByText('piece 20')).toBeInTheDocument();
    expect(screen.getByText('pieces 19 – 30')).toBeInTheDocument();
  });

  it('does not render Active Readers section when readers is empty', () => {
    render(
      <TorrentStatsDialogLayout
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        stats={{ ...mockStats, readers: [] }}
        loading={false}
        error={null}
        handleCopy={mockHandleCopy}
      />,
    );

    expect(screen.queryByText('Active Readers')).not.toBeInTheDocument();
  });

  it('renders Piece Map heading and PieceGrid canvas', () => {
    render(
      <TorrentStatsDialogLayout
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        stats={mockStats}
        loading={false}
        error={null}
        handleCopy={mockHandleCopy}
      />,
    );

    expect(screen.getByText('Piece Map')).toBeInTheDocument();
    expect(document.querySelector('canvas')).toBeInTheDocument();
  });

  it('displays memory usage percentage', () => {
    render(
      <TorrentStatsDialogLayout
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        stats={mockStats}
        loading={false}
        error={null}
        handleCopy={mockHandleCopy}
      />,
    );

    expect(screen.getByText('10.00%')).toBeInTheDocument();
  });
});
