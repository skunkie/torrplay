// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { EditTorrentDialog } from '@/components/edit-torrent-dialog';
import type { Torrent } from '@/lib/types/api';

vi.mock('@/lib/api/torrents', () => ({
  getCategories: vi.fn(),
  updateTorrent: vi.fn(),
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

describe('EditTorrentDialog', () => {
  const mockOnSuccess = vi.fn();
  const mockOnOpenChange = vi.fn();

  it('renders nothing when torrent is null', () => {
    render(
      <EditTorrentDialog
        torrent={null}
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.queryByText('Edit Torrent')).not.toBeInTheDocument();
  });

  it('renders the dialog when torrent is provided', () => {
    render(
      <EditTorrentDialog
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.getByText(/Edit Torrent/i)).toBeInTheDocument();
    expect(screen.getByText('Test Torrent')).toBeInTheDocument();
  });

  it('populates form with torrent data', () => {
    render(
      <EditTorrentDialog
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.getByDisplayValue('Test Torrent')).toBeInTheDocument();
    expect(screen.getByDisplayValue('http://example.com/poster.jpg')).toBeInTheDocument();
  });

  it('displays title and poster inputs', () => {
    render(
      <EditTorrentDialog
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.getByLabelText(/Title/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Poster URL/i)).toBeInTheDocument();
  });

  it('displays category input', () => {
    render(
      <EditTorrentDialog
        torrent={mockTorrent}
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.getByPlaceholderText('Movies, Series, Cartoons...')).toBeInTheDocument();
  });
});
