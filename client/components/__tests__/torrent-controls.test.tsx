// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';

import type { Torrent } from '@/lib/types/api';

import { TorrentControls } from '../torrent-controls';

describe('TorrentControls', () => {
  const defaultProps = {
    torrentCount: 0,
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

  it('offers the relevance order only while searching', () => {
    const onSortByChange = vi.fn();
    const { container, unmount } = render(<TorrentControls {...defaultProps} />);
    fireEvent.click(container.querySelector('#sort-select')!);
    expect(screen.getByRole('option', { name: 'Name' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'Relevance' })).toBeNull();
    unmount();

    const searching = render(<TorrentControls {...defaultProps}
      isSearching
      onSortByChange={onSortByChange} />);
    fireEvent.click(searching.container.querySelector('#sort-select')!);
    fireEvent.click(screen.getByRole('option', { name: 'Relevance' }));
    expect(onSortByChange).toHaveBeenCalledWith('relevance');
  });

  it('shows a pending search instead of an empty count', () => {
    const { rerender } = render(<TorrentControls {...defaultProps}
      isSearchPending
      titleFilter='sintl' />);
    expect(screen.getAllByText('Searching...').length).toBeGreaterThan(0);

    rerender(<TorrentControls {...defaultProps}
      titleFilter='sintl' />);
    expect(screen.queryByText('Searching...')).toBeNull();
    expect(screen.getAllByText('0 of 0 torrents').length).toBeGreaterThan(0);
  });

  const sampleTorrent: Torrent = {
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

  const sampleTorrent2: Torrent = {
    ...sampleTorrent,
    hash: '456',
    name: 'Test 2',
    title: 'Test 2',
  };

  it('displays unfiltered torrent count properly', () => {
    render(
      <TorrentControls
        {...defaultProps}
        torrentCount={1}
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
        torrentCount={2}
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
        torrentCount={2}
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
        torrentCount={1}
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
        torrentCount={2}
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
        torrentCount={2}
        filteredAndSortedTorrents={[]}
      />
    );

    const counts = screen.getAllByText('0 of 2 torrents');
    expect(counts.length).toBeGreaterThan(0);
  });
});
