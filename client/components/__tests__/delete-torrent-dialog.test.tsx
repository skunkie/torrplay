// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { DeleteTorrentDialog } from '@/components/delete-torrent-dialog';
import type { Torrent } from '@/lib/types/api';

vi.mock('@/lib/api/torrents', () => ({
  deleteTorrent: vi.fn(),
}));

const mockTorrent: Torrent = {
  hash: '1234567890',
  title: 'Test Torrent',
  name: 'test-torrent',
  magnet: 'magnet:?xt=urn:btih:1234567890',
  files: [{ name: 'video.mp4', path: '/video.mp4', length: 1000 }],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  poster: 'http://example.com/poster.jpg',
  storage: 'file',
  pieceCount: 1,
  pieceSize: 1,
  totalSize: 1000,
};

describe('DeleteTorrentDialog', () => {
  const mockOnSuccess = vi.fn();
  const mockOnOpenChange = vi.fn();

  it('renders nothing when torrent is null', () => {
    render(
      <DeleteTorrentDialog
        torrent={null}
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.queryByText('1234567890')).not.toBeInTheDocument();
  });

  it('renders the dialog when torrent is provided', () => {
    render(
      <DeleteTorrentDialog
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.getByText('Delete Torrent')).toBeInTheDocument();
  });

  it('displays the torrent name', () => {
    render(
      <DeleteTorrentDialog
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.getByText('Test Torrent')).toBeInTheDocument();
  });

  it('displays Cancel and Delete buttons', () => {
    render(
      <DeleteTorrentDialog
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  it('does not render when open is false', () => {
    render(
      <DeleteTorrentDialog
        torrent={mockTorrent}
        open={false}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.queryByText('Delete Torrent')).not.toBeInTheDocument();
  });
});
