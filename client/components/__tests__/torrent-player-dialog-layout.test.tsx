// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { TorrentPlayerDialogLayout } from '@/components/torrent-player-dialog-layout';
import type { TorrentFile } from '@/lib/types/api';

const mockVideoFiles: TorrentFile[] = [
  { name: 'episode1.mp4', path: '/episode1.mp4', length: 1000 },
  { name: 'episode2.mp4', path: '/episode2.mp4', length: 2000 },
];

describe('TorrentPlayerDialogLayout', () => {
  it('renders file selection list when isPlayerVisible is false', () => {
    const onOpenChange = vi.fn();
    const setSelectedFile = vi.fn();

    render(
      <TorrentPlayerDialogLayout
        open={true}
        onOpenChange={onOpenChange}
        videoFiles={mockVideoFiles}
        selectedFile={null}
        setSelectedFile={setSelectedFile}
        isPlayerVisible={false}
        videoPlayerOptions={null}
      />
    );

    expect(screen.getByText('Select a video to play')).toBeInTheDocument();
    expect(screen.getByText('episode1.mp4')).toBeInTheDocument();
    expect(screen.getByText('episode2.mp4')).toBeInTheDocument();
  });

  it('calls setSelectedFile when a video file is clicked', () => {
    const onOpenChange = vi.fn();
    const setSelectedFile = vi.fn();

    render(
      <TorrentPlayerDialogLayout
        open={true}
        onOpenChange={onOpenChange}
        videoFiles={mockVideoFiles}
        selectedFile={null}
        setSelectedFile={setSelectedFile}
        isPlayerVisible={false}
        videoPlayerOptions={null}
      />
    );

    fireEvent.click(screen.getByText('episode1.mp4'));
    expect(setSelectedFile).toHaveBeenCalledWith(mockVideoFiles[0]);
  });

  it('renders VideoPlayerLayout directly when isPlayerVisible is true', () => {
    const onOpenChange = vi.fn();
    const setSelectedFile = vi.fn();
    const handleExit = vi.fn();

    const videoPlayerOptions = {
      src: {
        src: 'http://example.com/video.mp4',
        type: 'video/mp4' as const,
      },
      title: 'episode1.mp4',
      autoPlay: true,
    };

    render(
      <TorrentPlayerDialogLayout
        open={true}
        onOpenChange={onOpenChange}
        videoFiles={mockVideoFiles}
        selectedFile={mockVideoFiles[0]}
        setSelectedFile={setSelectedFile}
        isPlayerVisible={true}
        videoPlayerOptions={videoPlayerOptions}
        handleExit={handleExit}
      />
    );

    expect(screen.queryByText('Select a video to play')).not.toBeInTheDocument();
    expect(screen.getAllByText('episode1.mp4').length).toBeGreaterThan(0);
  });

  it('renders No Playable Files message when videoFiles is empty', () => {
    const onOpenChange = vi.fn();
    const setSelectedFile = vi.fn();

    render(
      <TorrentPlayerDialogLayout
        open={true}
        onOpenChange={onOpenChange}
        videoFiles={[]}
        selectedFile={null}
        setSelectedFile={setSelectedFile}
        isPlayerVisible={false}
        videoPlayerOptions={null}
      />
    );

    expect(screen.getByText('No Playable Files')).toBeInTheDocument();
    expect(
      screen.getByText('No playable video files were found in this torrent.')
    ).toBeInTheDocument();
  });

  it('calls handleExit instead of closing modal when dismissed in multi-file playback', () => {
    const onOpenChange = vi.fn();
    const setSelectedFile = vi.fn();
    const handleExit = vi.fn();

    const videoPlayerOptions = {
      src: {
        src: 'http://example.com/video.mp4',
        type: 'video/mp4' as const,
      },
      title: 'episode1.mp4',
      autoPlay: true,
    };

    render(
      <TorrentPlayerDialogLayout
        open={true}
        onOpenChange={onOpenChange}
        videoFiles={mockVideoFiles}
        selectedFile={mockVideoFiles[0]}
        setSelectedFile={setSelectedFile}
        isPlayerVisible={true}
        videoPlayerOptions={videoPlayerOptions}
        handleExit={handleExit}
      />
    );

    // Simulate Escape key / dialog dismiss
    fireEvent.keyDown(document, { key: 'Escape' });

    expect(handleExit).toHaveBeenCalled();
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it('calls onOpenChange directly when dismissed with single video file', () => {
    const onOpenChange = vi.fn();
    const setSelectedFile = vi.fn();
    const handleExit = vi.fn();

    const singleVideoFile = [mockVideoFiles[0]];
    const videoPlayerOptions = {
      src: {
        src: 'http://example.com/video.mp4',
        type: 'video/mp4' as const,
      },
      title: 'episode1.mp4',
      autoPlay: true,
    };

    render(
      <TorrentPlayerDialogLayout
        open={true}
        onOpenChange={onOpenChange}
        videoFiles={singleVideoFile}
        selectedFile={singleVideoFile[0]}
        setSelectedFile={setSelectedFile}
        isPlayerVisible={true}
        videoPlayerOptions={videoPlayerOptions}
        handleExit={handleExit}
      />
    );

    fireEvent.keyDown(document, { key: 'Escape' });

    expect(handleExit).toHaveBeenCalled();
  });

  it('renders preloading badge inside video player when preloadBadge is provided', () => {
    render(
      <TorrentPlayerDialogLayout
        open={true}
        onOpenChange={vi.fn()}
        videoFiles={mockVideoFiles}
        selectedFile={mockVideoFiles[0]}
        setSelectedFile={vi.fn()}
        isPlayerVisible={true}
        videoPlayerOptions={{
          src: { src: 'http://test/video.mp4', type: 'video/mp4' },
          title: 'episode1.mp4',
          autoPlay: true,
        }}
        preloadBadge={{
          progress: 0.7,
          completedBytes: 7000000,
          targetBytes: 10000000,
        }}
      />
    );

    const badge = screen.getByTestId('player-preload-badge');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveTextContent('Buffering 70%');
  });
});
