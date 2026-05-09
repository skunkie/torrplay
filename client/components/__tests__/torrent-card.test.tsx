// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { TorrentCard } from '@/components/torrent-card';
import type { Torrent } from '@/lib/types/api';

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

const mockTorrent2: Torrent = {
  hash: '0987654321',
  title: 'Test Torrent 2',
  name: 'test-torrent-2',
  magnet: 'magnet:?xt=urn:btih:0987654321',
  files: [{ name: 'video2.mp4', path: '/video2.mp4', length: 2000 }],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  poster: 'http://example.com/poster2.jpg',
  storage: 'file',
  pieceCount: 1,
  pieceSize: 1,
  totalSize: 2000,
};

const mockTorrentNoVideo: Torrent = {
  hash: '1122334455',
  title: 'Test Torrent No Video',
  name: 'test-torrent-no-video',
  magnet: 'magnet:?xt=urn:btih:1122334455',
  files: [{ name: 'document.pdf', path: '/document.pdf', length: 1000 }],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  poster: 'http://example.com/poster3.jpg',
  storage: 'file',
  pieceCount: 1,
  pieceSize: 1,
  totalSize: 1000,
};

const mockTorrentNoPoster: Torrent = {
  hash: '6677889900',
  title: 'Test Torrent No Poster',
  name: 'test-torrent-no-poster',
  magnet: 'magnet:?xt=urn:btih:6677889900',
  files: [{ name: 'video.mp4', path: '/video.mp4', length: 1000 }],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  storage: 'file',
  pieceCount: 1,
  pieceSize: 1,
  totalSize: 1000,
};

describe('TorrentCard', () => {
  it('renders the torrent title', () => {
    render(
      <TorrentCard
        torrent={mockTorrent}
        onEdit={() => {}}
        onViewStats={() => {}}
        onDelete={() => {}}
        onPlayTorrent={() => {}}
      />,
    );
    expect(screen.getByText('Test Torrent')).toBeInTheDocument();
  });

  it('handles keyboard navigation correctly', () => {
    const onPlayTorrent = vi.fn();
    render(
      <TorrentCard
        torrent={mockTorrent}
        onEdit={() => {}}
        onViewStats={() => {}}
        onDelete={() => {}}
        onPlayTorrent={onPlayTorrent}
      />,
    );

    const card = screen.getByText('Test Torrent').parentElement!.parentElement!;
    card.focus();

    // Enter internal navigation.
    fireEvent.keyDown(card, { key: 'Enter' });

    const poster = screen.getByRole('button', { name: /Play torrent/i });
    expect(poster).toHaveFocus();

    // Navigate down to stats button.
    fireEvent.keyDown(poster, { key: 'ArrowDown' });
    const statsButton = screen.getByText('Stats').parentElement!;
    expect(statsButton).toHaveFocus();

    // Navigate right to edit button.
    fireEvent.keyDown(statsButton, { key: 'ArrowRight' });
    const editButton = screen.getByText('Edit').parentElement!;
    expect(editButton).toHaveFocus();

    // Navigate right to delete button.
    fireEvent.keyDown(editButton, { key: 'ArrowRight' });
    const deleteButton = screen.getByText('Delete').parentElement!;
    expect(deleteButton).toHaveFocus();

    // Navigate left to edit button.
    fireEvent.keyDown(deleteButton, { key: 'ArrowLeft' });
    expect(editButton).toHaveFocus();

    // Navigate up to poster.
    fireEvent.keyDown(editButton, { key: 'ArrowUp' });
    expect(poster).toHaveFocus();

    // Press enter on poster to play.
    fireEvent.keyDown(poster, { key: 'Enter' });
    expect(onPlayTorrent).toHaveBeenCalledWith(mockTorrent);

    // Exit internal navigation.
    fireEvent.keyDown(poster, { key: 'Escape' });
    expect(card).toHaveFocus();
  });

  it('restores focus to the correct button after a dialog is closed with Escape', async () => {
    const onViewStats = vi.fn();
    const { container } = render(
      <TorrentCard
        torrent={mockTorrent}
        onEdit={() => {}}
        onViewStats={onViewStats}
        onDelete={() => {}}
        onPlayTorrent={() => {}}
      />,
    );

    const card = container.querySelector('.group') as HTMLElement;
    card.focus();
    expect(card).toHaveFocus();

    // Enter internal navigation.
    fireEvent.keyDown(card, { key: 'Enter' });
    const poster = screen.getByRole('button', { name: /Play torrent/i });
    expect(poster).toHaveFocus();

    // Navigate to stats button.
    fireEvent.keyDown(poster, { key: 'ArrowDown' });
    const statsButton = screen.getByText('Stats').parentElement as HTMLButtonElement;
    expect(statsButton).toHaveFocus();

    // Simulate opening a dialog by clicking the stats button.
    fireEvent.click(statsButton);
    expect(onViewStats).toHaveBeenCalledWith(mockTorrent);

    // The button would lose focus to a dialog in a real app.
    statsButton.blur();
    expect(statsButton).not.toHaveFocus();

    // Simulate pressing Escape, which would close the dialog.
    fireEvent.keyDown(document, { key: 'Escape' });

    // The card should remain in navigation mode.
    expect(card).toHaveAttribute('data-nav-inside', 'true');

    // Focus should be restored to the button that opened the dialog.
    await waitFor(() => {
      expect(statsButton).toHaveFocus();
    });
  });

  it('handles a full keyboard navigation cycle correctly', () => {
    render(
      <TorrentCard
        torrent={mockTorrent}
        onEdit={() => {}}
        onViewStats={() => {}}
        onDelete={() => {}}
        onPlayTorrent={() => {}}
      />,
    );

    const card = screen.getByText('Test Torrent').parentElement!.parentElement!;
    card.focus();

    // Enter internal navigation.
    fireEvent.keyDown(card, { key: 'Enter' });
    const poster = screen.getByRole('button', { name: /Play torrent/i });
    expect(poster).toHaveFocus();

    // Navigate down to stats button.
    fireEvent.keyDown(poster, { key: 'ArrowDown' });
    const statsButton = screen.getByText('Stats').parentElement!;
    expect(statsButton).toHaveFocus();

    // Navigate right to edit button.
    fireEvent.keyDown(statsButton, { key: 'ArrowRight' });
    const editButton = screen.getByText('Edit').parentElement!;
    expect(editButton).toHaveFocus();

    // Navigate right to delete button.
    fireEvent.keyDown(editButton, { key: 'ArrowRight' });
    const deleteButton = screen.getByText('Delete').parentElement!;
    expect(deleteButton).toHaveFocus();

    // Navigate left back to edit button.
    fireEvent.keyDown(deleteButton, { key: 'ArrowLeft' });
    expect(editButton).toHaveFocus();

    // Navigate left back to stats button.
    fireEvent.keyDown(editButton, { key: 'ArrowLeft' });
    expect(statsButton).toHaveFocus();

    // Navigate up to poster from stats button.
    fireEvent.keyDown(statsButton, { key: 'ArrowUp' });
    expect(poster).toHaveFocus();

    // Go back down to buttons to test ArrowUp from other buttons.
    fireEvent.keyDown(poster, { key: 'ArrowDown' });
    fireEvent.keyDown(statsButton, { key: 'ArrowRight' }); // now on edit.

    // Navigate up to poster from edit button.
    fireEvent.keyDown(editButton, { key: 'ArrowUp' });
    expect(poster).toHaveFocus();

    // Go back down to buttons.
    fireEvent.keyDown(poster, { key: 'ArrowDown' });
    fireEvent.keyDown(statsButton, { key: 'ArrowRight' });
    fireEvent.keyDown(editButton, { key: 'ArrowRight' }); // now on delete.

    // Navigate up to poster from delete button.
    fireEvent.keyDown(deleteButton, { key: 'ArrowUp' });
    expect(poster).toHaveFocus();

    // Exit internal navigation.
    fireEvent.keyDown(poster, { key: 'Escape' });
    expect(card).toHaveFocus();
    expect(card).toHaveAttribute('data-nav-inside', 'false');
  });

  it('allows navigation between multiple torrent cards', () => {
    render(
      <div>
        <TorrentCard
          torrent={mockTorrent}
          onEdit={() => {}}
          onViewStats={() => {}}
          onDelete={() => {}}
          onPlayTorrent={() => {}}
        />
        <TorrentCard
          torrent={mockTorrent2}
          onEdit={() => {}}
          onViewStats={() => {}}
          onDelete={() => {}}
          onPlayTorrent={() => {}}
        />
      </div>,
    );

    const card1 = screen.getByText('Test Torrent').closest('.group')! as HTMLElement;
    const card2 = screen.getByText('Test Torrent 2').closest('.group')! as HTMLElement;

    // Start with focus on the first card.
    card1.focus();
    expect(card1).toHaveFocus();

    // Enter internal navigation on the first card.
    fireEvent.keyDown(card1, { key: 'Enter' });
    expect(card1).toHaveAttribute('data-nav-inside', 'true');

    // Exit internal navigation on the first card.
    fireEvent.keyDown(card1.querySelector('[role="button"]')!, { key: 'Escape' });
    expect(card1).toHaveFocus();
    expect(card1).toHaveAttribute('data-nav-inside', 'false');

    // Move focus to the second card.
    card2.focus();
    expect(card2).toHaveFocus();

    // Enter internal navigation on the second card.
    fireEvent.keyDown(card2, { key: 'Enter' });
    expect(card2).toHaveAttribute('data-nav-inside', 'true');
    const poster2 = card2.querySelector('[role="button"]');
    expect(poster2).toHaveFocus();
  });

  it('calls the correct callbacks for stats, edit, and delete buttons', () => {
    const onEdit = vi.fn();
    const onViewStats = vi.fn();
    const onDelete = vi.fn();

    render(
      <TorrentCard
        torrent={mockTorrent}
        onEdit={onEdit}
        onViewStats={onViewStats}
        onDelete={onDelete}
        onPlayTorrent={() => {}}
      />,
    );

    const statsButton = screen.getByText('Stats').parentElement!;
    const editButton = screen.getByText('Edit').parentElement!;
    const deleteButton = screen.getByText('Delete').parentElement!;

    fireEvent.click(statsButton);
    expect(onViewStats).toHaveBeenCalledWith(mockTorrent);
    expect(onViewStats).toHaveBeenCalledTimes(1);

    fireEvent.click(editButton);
    expect(onEdit).toHaveBeenCalledWith(mockTorrent);
    expect(onEdit).toHaveBeenCalledTimes(1);

    fireEvent.click(deleteButton);
    expect(onDelete).toHaveBeenCalledWith(mockTorrent);
    expect(onDelete).toHaveBeenCalledTimes(1);
  });

  it('does not trigger play for torrents with no video files', () => {
    const onPlayTorrent = vi.fn();
    const { container } = render(
      <TorrentCard
        torrent={mockTorrentNoVideo}
        onEdit={() => {}}
        onViewStats={() => {}}
        onDelete={() => {}}
        onPlayTorrent={onPlayTorrent}
      />,
    );

    const poster = screen.getByRole('button', { name: /Play torrent/i });
    fireEvent.click(poster);
    expect(onPlayTorrent).not.toHaveBeenCalled();

    fireEvent.keyDown(poster, { key: 'Enter' });
    expect(onPlayTorrent).not.toHaveBeenCalled();

    const playIcon = container.querySelector('[data-lucide="play"]');
    expect(playIcon).not.toBeInTheDocument();
  });

  it('shows a placeholder when the torrent has no poster', () => {
    render(
      <TorrentCard
        torrent={mockTorrentNoPoster}
        onEdit={() => {}}
        onViewStats={() => {}}
        onDelete={() => {}}
        onPlayTorrent={() => {}}
      />,
    );

    const imageOffIcon = screen.getByTestId('no-poster-placeholder');
    expect(imageOffIcon).toBeInTheDocument();

    const image = screen.queryByAltText('Torrent');
    expect(image).not.toBeInTheDocument();
  });

  it('displays a white rectangle on focus', () => {
    const { container } = render(
      <TorrentCard
        torrent={mockTorrent}
        onEdit={() => {}}
        onViewStats={() => {}}
        onDelete={() => {}}
        onPlayTorrent={() => {}}
      />,
    );

    const card = container.querySelector('.group') as HTMLElement;
    card.focus();

    expect(card).toHaveFocus();
    // Check for the focus ring utility classes
    expect(card.className).toContain('focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2');
  });

  it('displays a white rectangle on focus for inner elements', () => {
    const { container } = render(
      <TorrentCard
        torrent={mockTorrent}
        onEdit={() => {}}
        onViewStats={() => {}}
        onDelete={() => {}}
        onPlayTorrent={() => {}}
      />,
    );

    const card = container.querySelector('.group') as HTMLElement;
    card.focus();

    // Enter internal navigation.
    fireEvent.keyDown(card, { key: 'Enter' });

    const poster = screen.getByRole('button', { name: /Play torrent/i });
    expect(poster).toHaveFocus();
    expect(poster.className).toContain('focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2');

    // Navigate down to stats button.
    fireEvent.keyDown(poster, { key: 'ArrowDown' });
    const statsButton = screen.getByText('Stats').parentElement!;
    expect(statsButton).toHaveFocus();
    expect(statsButton.className).toContain('focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2');
  });

  it('shows play icon on poster focus and handles overflow', async () => {
    const { container } = render(
      <TorrentCard
        torrent={mockTorrent}
        onEdit={() => {}}
        onViewStats={() => {}}
        onDelete={() => {}}
        onPlayTorrent={() => {}}
      />,
    );

    const card = container.querySelector('.group') as HTMLElement;
    const playIconOverlay = screen.getByTestId('play-icon-overlay');

    // Initially, the overlay should have the opacity-0 class
    expect(playIconOverlay.classList.contains('opacity-0')).toBe(true);

    // Focus the card
    card.focus();

    // Opacity becomes 1 because of group-focus-within
    await waitFor(() => {
      const computedStyle = window.getComputedStyle(playIconOverlay);
      expect(computedStyle.opacity).toBe('1');
    });

    // Enter internal navigation.
    fireEvent.keyDown(card, { key: 'Enter' });

    const poster = screen.getByRole('button', { name: /Play torrent/i });
    expect(poster).toHaveFocus();

    // Opacity should still be 1
    const computedStyle = window.getComputedStyle(playIconOverlay);
    expect(computedStyle.opacity).toBe('1');

    // Check that overflow-hidden is applied to the right element
    const posterInner = poster.querySelector('.overflow-hidden');
    expect(posterInner).toBeInTheDocument();
    expect(card).not.toHaveClass('overflow-hidden');
  });
});
