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
    usePagination: true,
    torrentsPerPage: 12,
    onTorrentsPerPageChange: vi.fn(),
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

  it('displays torrent count properly', () => {
    render(
      <TorrentControls
        {...defaultProps}
        filteredAndSortedTorrents={[
          {
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
          },
        ]}
      />
    );

    const counts = screen.getAllByText('1 torrent');
    expect(counts.length).toBeGreaterThan(0);
  });
});
