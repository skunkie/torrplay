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

  it('navigates between videos and disables controls at playlist boundaries', async () => {
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentMultipleVideos}
        open={true}
        onOpenChange={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByText('video2.mp4'));

    const previousButton = await screen.findByRole('button', { name: 'Previous video' });
    const nextButton = screen.getByRole('button', { name: 'Next video' });
    expect(previousButton).toHaveClass('h-10', 'w-10');
    expect(previousButton).toBeEnabled();
    expect(nextButton).toBeEnabled();

    fireEvent.click(nextButton);
    await waitFor(() => expect(screen.getAllByText('video3.mkv')).toHaveLength(2));
    expect(screen.getByRole('button', { name: 'Next video' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: 'Previous video' }));
    await waitFor(() => expect(screen.getAllByText('video2.mp4')).toHaveLength(2));
  });

  it('does not show playlist navigation for a single video file', () => {
    render(
      <TorrentPlayerDialog
        torrent={mockTorrentSingleVideo}
        open={true}
        onOpenChange={vi.fn()}
      />,
    );

    expect(screen.queryByRole('button', { name: 'Previous video' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Next video' })).not.toBeInTheDocument();
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

  describe('single file re-open', () => {
    it('should not flash "No Playable Files" on re-open with single file', async () => {
      const onOpenChange = vi.fn();
      const { rerender } = render(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={true}
          onOpenChange={onOpenChange}
        />,
      );

      // Close
      rerender(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={false}
          onOpenChange={onOpenChange}
        />,
      );

      // Immediately after close, no UI should be visible
      expect(screen.queryByText('No Playable Files')).not.toBeInTheDocument();
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();

      // Re-open
      rerender(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={true}
          onOpenChange={onOpenChange}
        />,
      );

      // "No Playable Files" must NOT flash during re-open
      expect(screen.queryByText('No Playable Files')).not.toBeInTheDocument();

      // File selector must NOT flash during re-open
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    });

    it('should show video player immediately on re-open with single file', async () => {
      const onOpenChange = vi.fn();
      const { rerender } = render(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={true}
          onOpenChange={onOpenChange}
        />,
      );

      // Close
      rerender(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={false}
          onOpenChange={onOpenChange}
        />,
      );

      // Re-open
      rerender(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={true}
          onOpenChange={onOpenChange}
        />,
      );

      // Video player should be visible immediately (not after useEffect)
      expect(screen.getAllByText('video.mp4')).toHaveLength(2);
    });

    it('should not call onOpenChange(false) on re-open with single file', async () => {
      const onOpenChange = vi.fn();
      const { rerender } = render(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={true}
          onOpenChange={onOpenChange}
        />,
      );

      onOpenChange.mockClear();

      // Close
      rerender(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={false}
          onOpenChange={onOpenChange}
        />,
      );

      // Re-open
      rerender(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={true}
          onOpenChange={onOpenChange}
        />,
      );

      // Wait for effects to settle
      await waitFor(() => {
        expect(screen.getAllByText('video.mp4')).toHaveLength(2);
      });

      // onOpenChange(false) must not have been called during re-open
      expect(onOpenChange).not.toHaveBeenCalledWith(false);
    });

    it('should not flash file selector when opening directly with single file from closed state', async () => {
      const onOpenChange = vi.fn();
      const { rerender } = render(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={false}
          onOpenChange={onOpenChange}
        />,
      );

      // Open directly (first time from closed state)
      rerender(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={true}
          onOpenChange={onOpenChange}
        />,
      );

      // Must not flash file selector
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
      // Must not flash "No Playable Files"
      expect(screen.queryByText('No Playable Files')).not.toBeInTheDocument();
      // Video player should be visible
      expect(screen.getAllByText('video.mp4')).toHaveLength(2);
    });

    it('should not flash when closing and re-opening multiple times', async () => {
      const onOpenChange = vi.fn();
      const { rerender } = render(
        <TorrentPlayerDialog
          torrent={mockTorrentSingleVideo}
          open={true}
          onOpenChange={onOpenChange}
        />,
      );

      for (let i = 0; i < 3; i++) {
        onOpenChange.mockClear();

        rerender(
          <TorrentPlayerDialog
            torrent={mockTorrentSingleVideo}
            open={false}
            onOpenChange={onOpenChange}
          />,
        );

        rerender(
          <TorrentPlayerDialog
            torrent={mockTorrentSingleVideo}
            open={true}
            onOpenChange={onOpenChange}
          />,
        );

        // Must not flash on any re-open cycle
        expect(screen.queryByText('No Playable Files')).not.toBeInTheDocument();
        expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
      }
    });
  });

  it('provides subtitle tracks from torrent files to the video player', () => {
    const torrentWithSubs: Torrent = {
      hash: 'subtorrent123',
      title: 'Movie with Subs',
      name: 'movie-subs',
      magnet: 'magnet:?xt=urn:btih:subtorrent123',
      files: [
        { name: 'movie.mp4', path: 'movie.mp4', length: 5000 },
        { name: 'movie.en.srt', path: 'subs/movie.en.srt', length: 50 },
        { name: 'movie.es.vtt', path: 'subs/movie.es.vtt', length: 40 },
      ],
      storage: 'file',
      pieceCount: 1,
      pieceSize: 1,
      totalSize: 5090,
    };

    render(
      <TorrentPlayerDialog
        torrent={torrentWithSubs}
        open={true}
        onOpenChange={vi.fn()}
      />
    );

    const subButton = screen.getByRole('button', { name: /select subtitle track/i });
    expect(subButton).toBeInTheDocument();
  });
});
