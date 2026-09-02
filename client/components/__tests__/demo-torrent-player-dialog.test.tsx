// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { DemoTorrentPlayerDialog } from '@/app/demo/demo-torrent-player-dialog';
import type { Torrent } from '@/lib/types/api';

const mockDemoMultiTorrent: Torrent = {
  hash: 'demo123456',
  title: 'Demo Multi File Torrent',
  name: 'demo-multi',
  magnet: 'magnet:?xt=urn:btih:demo123456',
  files: [
    { name: 'demo_video1.mp4', path: '/demo_video1.mp4', length: 1000 },
    { name: 'demo_video2.mp4', path: '/demo_video2.mp4', length: 2000 },
  ],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  poster: 'http://example.com/poster.jpg',
  storage: 'file',
  pieceCount: 1,
  pieceSize: 1,
  totalSize: 3000,
};

const mockDemoSingleTorrent: Torrent = {
  hash: 'demosingle123',
  title: 'Demo Single File Torrent',
  name: 'demo-single',
  magnet: 'magnet:?xt=urn:btih:demosingle123',
  files: [
    { name: 'single_video.mp4', path: '/single_video.mp4', length: 1000 },
  ],
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
  poster: 'http://example.com/poster.jpg',
  storage: 'file',
  pieceCount: 1,
  pieceSize: 1,
  totalSize: 1000,
};

describe('DemoTorrentPlayerDialog', () => {
  it('renders file selection dialog for multi-file torrent', () => {
    const onOpenChange = vi.fn();

    render(
      <DemoTorrentPlayerDialog
        torrent={mockDemoMultiTorrent}
        open={true}
        onOpenChange={onOpenChange}
      />
    );

    expect(screen.getByText('Select a video to play')).toBeInTheDocument();
    expect(screen.getByText('demo_video1.mp4')).toBeInTheDocument();
    expect(screen.getByText('demo_video2.mp4')).toBeInTheDocument();
  });

  it('selects and plays video from multi-file torrent in demo mode', async () => {
    const onOpenChange = vi.fn();

    render(
      <DemoTorrentPlayerDialog
        torrent={mockDemoMultiTorrent}
        open={true}
        onOpenChange={onOpenChange}
      />
    );

    fireEvent.click(screen.getByText('demo_video1.mp4'));

    await waitFor(() => {
      expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    });

    expect(screen.getAllByText('demo_video1.mp4').length).toBeGreaterThan(0);
  });

  it('navigates between videos in demo mode', async () => {
    render(
      <DemoTorrentPlayerDialog
        torrent={mockDemoMultiTorrent}
        open={true}
        onOpenChange={vi.fn()}
      />
    );

    fireEvent.click(screen.getByText('demo_video1.mp4'));
    const nextButton = await screen.findByRole('button', { name: 'Next video' });

    expect(screen.getByRole('button', { name: 'Previous video' })).toBeDisabled();
    fireEvent.click(nextButton);

    await waitFor(() => {
      expect(screen.getAllByText('demo_video2.mp4').length).toBeGreaterThan(0);
    });
    expect(screen.getByRole('button', { name: 'Next video' })).toBeDisabled();
  });

  it('plays single-file torrent directly', () => {
    const onOpenChange = vi.fn();

    render(
      <DemoTorrentPlayerDialog
        torrent={mockDemoSingleTorrent}
        open={true}
        onOpenChange={onOpenChange}
      />
    );

    expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    expect(screen.getAllByText('single_video.mp4').length).toBeGreaterThan(0);
  });

  it('does not render when open is false', () => {
    const onOpenChange = vi.fn();

    render(
      <DemoTorrentPlayerDialog
        torrent={mockDemoMultiTorrent}
        open={false}
        onOpenChange={onOpenChange}
      />
    );

    expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
  });
});
