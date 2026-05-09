// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { TorrentPlayerDialog } from '@/components/torrent-player-dialog';
import { type Torrent } from '@/lib/types/api';

const mockTorrentSingleVideo: Torrent = {
  hash: '1234567890',
  title: 'Test Torrent',
  name: 'test-torrent',
  magnet: 'magnet:?xt=urn:btih:1234567890',
  files: [
    { name: 'video.mp4', path: '/video.mp4', length: 1000 },
  ],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  poster: 'http://example.com/poster.jpg',
  storage: 'file',
  pieceCount: 1,
  pieceSize: 1,
  totalSize: 1000,
};

const mockTorrentMultipleVideos: Torrent = {
  hash: '0987654321',
  title: 'Test Torrent Multiple',
  name: 'test-torrent-multiple',
  magnet: 'magnet:?xt=urn:btih:0987654321',
  files: [
    { name: 'video1.mp4', path: '/video1.mp4', length: 1000 },
    { name: 'video2.mp4', path: '/video2.mp4', length: 2000 },
    { name: 'video3.mkv', path: '/video3.mkv', length: 3000 },
  ],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  poster: 'http://example.com/poster2.jpg',
  storage: 'file',
  pieceCount: 1,
  pieceSize: 1,
  totalSize: 6000,
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

describe('TorrentPlayerDialog', () => {
  it('shows "Select a video to play" dialog when opened with multiple video files', () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    expect(screen.getByText('Select a video to play')).toBeInTheDocument();
    expect(screen.getByText('video1.mp4')).toBeInTheDocument();
    expect(screen.getByText('video2.mp4')).toBeInTheDocument();
    expect(screen.getByText('video3.mkv')).toBeInTheDocument();
  });

  it('shows video player immediately when torrent has only one video file', () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentSingleVideo}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    expect(screen.getAllByText('video.mp4')).toHaveLength(2);
  });

  it('shows "No Playable Files" message when torrent has no video files', () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentNoVideo}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    expect(screen.getByText('No Playable Files')).toBeInTheDocument();
    expect(screen.getByText('No playable video files were found in this torrent.')).toBeInTheDocument();
  });

  it('displays video player when a file is selected from the list', async () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    const firstVideoButton = screen.getByText('video1.mp4');
    fireEvent.click(firstVideoButton);

    await waitFor(() => {
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    });
  });

  it('returns to file selection dialog when closing video player with multiple files', async () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    const firstVideoButton = screen.getByText('video1.mp4');
    fireEvent.click(firstVideoButton);

    await waitFor(() => {
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    });

    const closeButton = document.querySelector('.lucide-x')?.closest('button');
    if (closeButton) {
      fireEvent.click(closeButton);
    }

    await waitFor(() => {
      expect(screen.getByText('Select a video to play')).toBeInTheDocument();
    });

    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it('allows selecting different video files after returning to selection dialog', async () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    const firstVideoButton = screen.getByText('video1.mp4');
    fireEvent.click(firstVideoButton);

    await waitFor(() => {
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    });

    const closeButton = document.querySelector('.lucide-x')?.closest('button');
    if (closeButton) {
      fireEvent.click(closeButton);
    }

    await waitFor(() => {
      expect(screen.getByText('Select a video to play')).toBeInTheDocument();
    });

    const secondVideoButton = screen.getByText('video2.mp4');
    fireEvent.click(secondVideoButton);

    await waitFor(() => {
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    });
  });

  it('closes entire dialog when closing video player with single video file', async () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentSingleVideo}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    const closeButton = document.querySelector('.lucide-x')?.closest('button');
    if (closeButton) {
      fireEvent.click(closeButton);
    }

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
  });

  it('resets state when dialog is closed and reopened', async () => {
    const onOpenChange = vi.fn();
    const { rerender } = render(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    const firstVideoButton = screen.getByText('video1.mp4');
    fireEvent.click(firstVideoButton);

    await waitFor(() => {
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    });

    rerender(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={false}
        onOpenChange={onOpenChange}
      />,
    );

    await waitFor(() => {
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    });

    rerender(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText('Select a video to play')).toBeInTheDocument();
    });
  });

  it('closes dialog when pressing Escape on file selection dialog', async () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    fireEvent.keyDown(document, { key: 'Escape' });

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
  });

  it('closes dialog when pressing Escape on video player with single file', async () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentSingleVideo}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    fireEvent.keyDown(document, { key: 'Escape' });

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
  });

  it('returns to file selection when pressing Escape on video player with multiple files', async () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    const firstVideoButton = screen.getByText('video1.mp4');
    fireEvent.click(firstVideoButton);

    await waitFor(() => {
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    });

    fireEvent.keyDown(document, { key: 'Escape' });

    await waitFor(() => {
      expect(screen.getByText('Select a video to play')).toBeInTheDocument();
    });

    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it('does not exit video player immediately after selecting a file', async () => {
    const onOpenChange = vi.fn();
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={true}
        onOpenChange={onOpenChange}
      />,
    );

    const firstVideoButton = screen.getByText('video1.mp4');
    fireEvent.click(firstVideoButton);

    await waitFor(() => {
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    });

    // Wait a moment to ensure onExit is not called immediately
    await waitFor(() => {
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    }, { timeout: 100 });

    // Verify onOpenChange was not called (meaning player didn't exit)
    expect(onOpenChange).not.toHaveBeenCalled();
  });

});
