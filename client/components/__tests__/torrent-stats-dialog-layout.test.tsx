// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import { TorrentStatsDialogLayout } from '@/components/torrent-stats-dialog-layout';
import type { Torrent, TorrentStats } from '@/lib/types/api';

const baseTorrent: Torrent = {
  category: 'movies',
  files: [
    { length: 1000, name: 'movie.mp4', path: 'movies/movie.mp4' },
  ],
  hash: 'abc123def456',
  magnet: 'magnet:?xt=urn:btih:abc123',
  name: 'Test Movie',
  pieceCount: 10,
  pieceSize: 100,
  storage: 'memory',
  title: 'Test Movie',
  totalSize: 1000,
};

const makeStats = (overrides?: Partial<TorrentStats>): TorrentStats => ({
  activePeers: 5,
  bytesHashed: 5000,
  bytesRead: 10000,
  bytesReadData: 8000,
  bytesReadUsefulData: 7000,
  bytesReadUsefulIntendedData: 6000,
  bytesWritten: 2000,
  bytesWrittenData: 1500,
  chunksRead: 100,
  chunksReadUseful: 90,
  chunksReadWasted: 10,
  chunksWritten: 50,
  completedSize: 500,
  writtenBytes: 450,
  connectedSeeders: 3,
  halfOpenPeers: 2,
  inMemory: 5,
  inMemorySize: 250,
  memoryStats: { activeTorrents: 1, maxMemory: 1000, totalPieces: 10, usedMemory: 250 },
  memoryUsagePercentage: 25,
  metadataChunksRead: 20,
  pendingPeers: 1,
  pieces: [
    { complete: true, inMemory: true, index: 0, size: 100 },
    { complete: true, inMemory: true, index: 1, size: 100 },
  ],
  piecesComplete: 2,
  piecesDirtiedBad: 0,
  piecesDirtiedGood: 2,
  totalPeers: 8,
  totalPieces: 10,
  totalSize: 1000,
  ...overrides,
});

const defaultProps = {
  torrent: baseTorrent,
  open: true,
  onOpenChange: () => {},
  handleCopy: () => {},
};

describe('TorrentStatsDialogLayout', () => {
  it('returns null when torrent is null', () => {
    const { container } = render(
      <TorrentStatsDialogLayout
        torrent={null}
        open={true}
        onOpenChange={() => {}}
        stats={makeStats()}
        loading={false}
        error={null}
        handleCopy={() => {}}
      />,
    );

    expect(container.firstChild).toBeNull();
  });

  it('displays loading indicator while fetching stats', () => {
    render(
      <TorrentStatsDialogLayout
        {...defaultProps}
        stats={null}
        loading={true}
        error={null}
      />,
    );

    expect(screen.getByText('Loading statistics...')).toBeInTheDocument();
  });

  it('displays error message', () => {
    render(
      <TorrentStatsDialogLayout
        {...defaultProps}
        stats={null}
        loading={false}
        error='Failed to load stats'
      />,
    );

    expect(screen.getByText('Failed to load stats')).toBeInTheDocument();
  });

  it('renders torrent overview stats', () => {
    render(
      <TorrentStatsDialogLayout
        {...defaultProps}
        stats={makeStats()}
        loading={false}
        error={null}
      />,
    );

    expect(screen.getByText('Test Movie')).toBeInTheDocument();
    expect(screen.getByText('1000 Bytes')).toBeInTheDocument();
    expect(screen.getByText('500 Bytes')).toBeInTheDocument();
    expect(screen.getByText('450 Bytes')).toBeInTheDocument();
    expect(screen.getByText('250 Bytes')).toBeInTheDocument();
  });

  it('renders piece grid when pieces are present', async () => {
    const user = userEvent.setup();
    render(
      <TorrentStatsDialogLayout
        {...defaultProps}
        stats={makeStats()}
        loading={false}
        error={null}
      />,
    );

    expect(screen.getByText('Piece Map')).toBeInTheDocument();
    const trigger = screen.getByText('Piece Map').closest('button');
    await user.click(trigger!);
    expect(screen.getByText('Incomplete')).toBeInTheDocument();
    expect(screen.getByText('Complete')).toBeInTheDocument();
  });

  it('renders piece grid without pieces array', () => {
    const statsNoPieces = makeStats({ pieces: [] });

    render(
      <TorrentStatsDialogLayout
        {...defaultProps}
        stats={statsNoPieces}
        loading={false}
        error={null}
      />,
    );

    expect(screen.getByText('Piece Map')).toBeInTheDocument();
  });

  it('renders piece grid as collapsible accordion', () => {
    render(
      <TorrentStatsDialogLayout
        {...defaultProps}
        stats={makeStats()}
        loading={false}
        error={null}
      />,
    );

    const pieceMapTrigger = screen.getByText('Piece Map');
    expect(pieceMapTrigger.closest('button')).toHaveAttribute('aria-expanded', 'false');
  });

  it('shows piece count badge when piece data is available', () => {
    render(
      <TorrentStatsDialogLayout
        {...defaultProps}
        stats={makeStats()}
        loading={false}
        error={null}
      />,
    );

    expect(screen.getByText('(2)')).toBeInTheDocument();
  });

  it('hides badge when piece data is empty', () => {
    const statsNoPieces = makeStats({ pieces: [], totalPieces: 0 });

    render(
      <TorrentStatsDialogLayout
        {...defaultProps}
        stats={statsNoPieces}
        loading={false}
        error={null}
      />,
    );

    const pieceMapTrigger = screen.getByText('Piece Map').closest('button');
    expect(pieceMapTrigger!.textContent).not.toMatch(/\(\d+\)/);
  });

  describe('Reader display', () => {
    it('shows Active Readers section when readers are present', () => {
      const statsWithReaders = makeStats({
        readers: [
          { end: 5, position: 3, start: 1 },
          { end: 8, position: 6, start: 4 },
        ],
      });

      render(
        <TorrentStatsDialogLayout
          {...defaultProps}
          stats={statsWithReaders}
          loading={false}
          error={null}
        />,
      );

      expect(screen.getByText('Active Readers')).toBeInTheDocument();
      expect(screen.getByText('piece 3')).toBeInTheDocument();
      expect(screen.getByText('piece 6')).toBeInTheDocument();
      expect(screen.getByText('pieces 1 – 5')).toBeInTheDocument();
      expect(screen.getByText('pieces 4 – 8')).toBeInTheDocument();
    });

    it('hides Active Readers section when readers is empty', () => {
      const statsNoReaders = makeStats({ readers: [] });

      render(
        <TorrentStatsDialogLayout
          {...defaultProps}
          stats={statsNoReaders}
          loading={false}
          error={null}
        />,
      );

      expect(screen.queryByText('Active Readers')).not.toBeInTheDocument();
    });

    it('hides Active Readers section when readers is undefined', () => {
      const statsNoField = makeStats();
      delete (statsNoField as Partial<TorrentStats>).readers;

      render(
        <TorrentStatsDialogLayout
          {...defaultProps}
          stats={statsNoField}
          loading={false}
          error={null}
        />,
      );

      expect(screen.queryByText('Active Readers')).not.toBeInTheDocument();
    });

    it('renders each reader with position and range', () => {
      const statsWithThree = makeStats({
        readers: [
          { end: 2, position: 0, start: 0 },
          { end: 5, position: 3, start: 1 },
          { end: 9, position: 7, start: 5 },
        ],
      });

      render(
        <TorrentStatsDialogLayout
          {...defaultProps}
          stats={statsWithThree}
          loading={false}
          error={null}
        />,
      );

      const activeReadersSection = screen.getByText('Active Readers').parentElement!;
      expect(activeReadersSection).toHaveTextContent('piece 0');
      expect(activeReadersSection).toHaveTextContent('piece 3');
      expect(activeReadersSection).toHaveTextContent('piece 7');
    });

    it('renders readers correctly with boundary values', () => {
      const statsBoundary = makeStats({
        readers: [
          { end: 0, position: 0, start: 0 },
          { end: 9, position: 9, start: 9 },
        ],
      });

      render(
        <TorrentStatsDialogLayout
          {...defaultProps}
          stats={statsBoundary}
          loading={false}
          error={null}
        />,
      );

      expect(screen.getByText('piece 0')).toBeInTheDocument();
      expect(screen.getByText('piece 9')).toBeInTheDocument();
      expect(screen.getByText('pieces 0 – 0')).toBeInTheDocument();
      expect(screen.getByText('pieces 9 – 9')).toBeInTheDocument();
    });

    it('renders piece grid legend including Read Window', async () => {
      const user = userEvent.setup();
      const stats = makeStats({
        readers: [
          { end: 5, position: 3, start: 1 },
        ],
      });

      render(
        <TorrentStatsDialogLayout
          {...defaultProps}
          stats={stats}
          loading={false}
          error={null}
        />,
      );

      const trigger = screen.getByText('Piece Map').closest('button');
      await user.click(trigger!);
      expect(screen.getByText('Read Window')).toBeInTheDocument();
      expect(screen.getByText('Position')).toBeInTheDocument();
    });
  });
});
