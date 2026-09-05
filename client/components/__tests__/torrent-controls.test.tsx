// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';

import { TorrentControls } from '../torrent-controls';

describe('TorrentControls', () => {
  const defaultProps = {
    torrentsData: { torrents: [] },
    torrents: ['Movies', 'TV Shows'],
    filteredAndSortedTorrents: [],
    categoryFilter: 'all',
    onCategoryFilterChange: vi.fn(),
    sortBy: 'date',
    onSortByChange: vi.fn(),
    onAddTorrent: vi.fn(),
  };

  it('renders Add Torrent buttons and calls onAddTorrent on click', () => {
    const onAddTorrent = vi.fn();
    render(<TorrentControls {...defaultProps}
      onAddTorrent={onAddTorrent} />);

    const addButtons = screen.getAllByRole('button', { name: /Add Torrent/i });
    expect(addButtons.length).toBeGreaterThanOrEqual(1);

    fireEvent.click(addButtons[0]);
    expect(onAddTorrent).toHaveBeenCalled();
  });

  const sampleTorrent = {
    hash: '123',
    name: 'Test',
    title: 'Test',
    magnet: 'magnet:?xt=urn:btih:123',
    createdAt: '2026-01-01',
    updatedAt: '2026-01-01',
    totalSize: 1000,
    pieceCount: 10,
    pieceSize: 100,
    storage: 'file',
    active: true,
    files: [],
  };

  const sampleTorrent2 = {
    ...sampleTorrent,
    hash: '456',
    name: 'Test 2',
    title: 'Test 2',
  };

  it('displays unfiltered torrent count properly', () => {
    render(
      <TorrentControls
        {...defaultProps}
        torrentsData={{ torrents: [sampleTorrent] }}
        filteredAndSortedTorrents={[sampleTorrent]}
      />
    );

    const counts = screen.getAllByText('1 torrent');
    expect(counts.length).toBeGreaterThan(0);
  });

  it('displays plural unfiltered torrent count', () => {
    render(
      <TorrentControls
        {...defaultProps}
        torrentsData={{ torrents: [sampleTorrent, sampleTorrent2] }}
        filteredAndSortedTorrents={[sampleTorrent, sampleTorrent2]}
      />
    );

    const counts = screen.getAllByText('2 torrents');
    expect(counts.length).toBeGreaterThan(0);
  });

  it('displays filtered count and total count when items are filtered out', () => {
    render(
      <TorrentControls
        {...defaultProps}
        torrentsData={{ torrents: [sampleTorrent, sampleTorrent2] }}
        filteredAndSortedTorrents={[sampleTorrent]}
      />
    );

    const counts = screen.getAllByText('1 of 2 torrents');
    expect(counts.length).toBeGreaterThan(0);
  });

  it('displays filtered count when category filter is active even if all match', () => {
    render(
      <TorrentControls
        {...defaultProps}
        categoryFilter='Movies'
        torrentsData={{ torrents: [sampleTorrent] }}
        filteredAndSortedTorrents={[sampleTorrent]}
      />
    );

    const counts = screen.getAllByText('1 of 1 torrent');
    expect(counts.length).toBeGreaterThan(0);
  });

  it('displays filtered count when title filter is active', () => {
    render(
      <TorrentControls
        {...defaultProps}
        titleFilter='Test'
        torrentsData={{ torrents: [sampleTorrent, sampleTorrent2] }}
        filteredAndSortedTorrents={[sampleTorrent, sampleTorrent2]}
      />
    );

    const counts = screen.getAllByText('2 of 2 torrents');
    expect(counts.length).toBeGreaterThan(0);
  });

  it('displays 0 of total when no torrents match filter', () => {
    render(
      <TorrentControls
        {...defaultProps}
        titleFilter='nonexistent'
        torrentsData={{ torrents: [sampleTorrent, sampleTorrent2] }}
        filteredAndSortedTorrents={[]}
      />
    );

    const counts = screen.getAllByText('0 of 2 torrents');
    expect(counts.length).toBeGreaterThan(0);
  });
});
