// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { AddTorrentDialog } from '@/components/add-torrent-dialog';

vi.mock('@/lib/api/torrents', () => ({
  addTorrent: vi.fn(),
  getCategories: vi.fn(),
}));

describe('AddTorrentDialog', () => {
  const mockOnSuccess = vi.fn();
  const mockOnOpenChange = vi.fn();

  it('renders the dialog when open is true', () => {
    render(
      <AddTorrentDialog
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.getByText('Add New Torrent')).toBeInTheDocument();
  });

  it('does not render the dialog when open is false', () => {
    render(
      <AddTorrentDialog
        open={false}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.queryByText('Add New Torrent')).not.toBeInTheDocument();
  });

  it('renders all three tabs', () => {
    render(
      <AddTorrentDialog
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    const tabs = screen.getAllByRole('tab');
    expect(tabs.length).toBe(3);
  });

  it('displays magnet input and submit button', () => {
    render(
      <AddTorrentDialog
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.getByPlaceholderText('magnet:?xt=urn:btih:...')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add Torrent' })).toBeInTheDocument();
  });

  it('populates magnet field when initialUrl prop is provided', () => {
    render(
      <AddTorrentDialog
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
        initialUrl='magnet:?xt=urn:btih:initial123'
      />,
    );

    expect(screen.getByDisplayValue('magnet:?xt=urn:btih:initial123')).toBeInTheDocument();
  });

  it('displays optional fields for title, poster, and category', () => {
    render(
      <AddTorrentDialog
        open={true}
        onOpenChange={mockOnOpenChange}
        onSuccess={mockOnSuccess}
      />,
    );

    expect(screen.getByText(/Title \(optional\)/)).toBeInTheDocument();
    expect(screen.getByText(/Poster URL \(optional\)/)).toBeInTheDocument();
    expect(screen.getByText(/Category \(optional\)/)).toBeInTheDocument();
  });
});
