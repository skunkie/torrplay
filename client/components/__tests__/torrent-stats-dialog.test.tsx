// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TorrentStatsDialog } from '@/components/torrent-stats-dialog';

const mockGetTorrentStats = vi.fn();
const mockSetLiveUpdatesPaused = vi.fn();

vi.mock('@/lib/api/stats', () => ({
  getTorrentStats: (...args: unknown[]) => mockGetTorrentStats(...args),
}));

vi.mock('@/lib/live-updates-context', () => ({
  useLiveUpdates: vi.fn(() => ({
    liveUpdatesPaused: false,
    setLiveUpdatesPaused: mockSetLiveUpdatesPaused,
  })),
}));

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

const baseTorrent = {
  category: 'movies',
  files: [{ length: 1000, name: 'movie.mp4', path: 'movies/movie.mp4' }],
  hash: 'abc123def456',
  magnet: 'magnet:?xt=urn:btih:abc123',
  name: 'Test Movie',
  pieceCount: 10,
  pieceSize: 100,
  storage: 'memory',
  title: 'Test Movie',
  totalSize: 1000,
};

const mockStats = {
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
  connectedSeeders: 3,
  halfOpenPeers: 2,
  inMemory: 5,
  inMemorySize: 250,
  memoryStats: { activeTorrents: 1, maxMemory: 1000, totalPieces: 10, usedMemory: 250 },
  memoryUsagePercentage: 25,
  metadataChunksRead: 20,
  pendingPeers: 1,
  pieces: [{ complete: true, inMemory: true, index: 0, size: 100 }],
  piecesComplete: 1,
  piecesDirtiedBad: 0,
  piecesDirtiedGood: 1,
  totalPeers: 8,
  totalPieces: 10,
  totalSize: 1000,
};

describe('TorrentStatsDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetTorrentStats.mockResolvedValue(mockStats);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('fetches stats on initial open', async () => {
    render(
      <TorrentStatsDialog
        torrent={baseTorrent}
        open={true}
        onOpenChange={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(mockGetTorrentStats).toHaveBeenCalledWith('abc123def456');
    });
  });

  it('does not re-fetch when torrent reference changes but hash is the same', async () => {
    const { rerender } = render(
      <TorrentStatsDialog
        torrent={baseTorrent}
        open={true}
        onOpenChange={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(mockGetTorrentStats).toHaveBeenCalledTimes(1);
    });

    const initialCallCount = mockGetTorrentStats.mock.calls.length;

    const sameTorrentDifferentRef = { ...baseTorrent, title: 'Updated Title' };
    rerender(
      <TorrentStatsDialog
        torrent={sameTorrentDifferentRef}
        open={true}
        onOpenChange={vi.fn()}
      />,
    );

    await new Promise(resolve => setTimeout(resolve, 2500));

    const afterPoll = mockGetTorrentStats.mock.calls.length;

    expect(afterPoll).toBe(initialCallCount + 1);
  });

  it('fetches stats when opening dialog for a different torrent', async () => {
    const { rerender } = render(
      <TorrentStatsDialog
        torrent={baseTorrent}
        open={true}
        onOpenChange={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(mockGetTorrentStats).toHaveBeenCalledTimes(1);
    });

    mockGetTorrentStats.mockClear();

    const differentTorrent = { ...baseTorrent, hash: 'different123', title: 'Other Movie' };
    rerender(
      <TorrentStatsDialog
        torrent={differentTorrent}
        open={true}
        onOpenChange={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(mockGetTorrentStats).toHaveBeenCalledWith('different123');
    });
  });

  it('shows loading state on initial fetch', async () => {
    mockGetTorrentStats.mockReturnValueOnce(new Promise(() => {}));

    render(
      <TorrentStatsDialog
        torrent={baseTorrent}
        open={true}
        onOpenChange={vi.fn()}
      />,
    );

    expect(screen.getByText('Loading statistics...')).toBeInTheDocument();
  });

  it('sets up polling interval when not paused', async () => {
    render(
      <TorrentStatsDialog
        torrent={baseTorrent}
        open={true}
        onOpenChange={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(mockGetTorrentStats).toHaveBeenCalledTimes(1);
    });

    mockGetTorrentStats.mockClear();

    await new Promise(resolve => setTimeout(resolve, 2000));

    expect(mockGetTorrentStats).toHaveBeenCalled();
  });
});
