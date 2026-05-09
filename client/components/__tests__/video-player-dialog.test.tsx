// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { VideoPlayerDialog } from '@/components/video-player-dialog';

describe('VideoPlayerDialog', () => {
  it('renders nothing when dialog is closed', () => {
    const onOpenChange = vi.fn();
    const onExit = vi.fn();
    const mockVideoOptions = {
      src: {
        src: 'http://test-video-url.com/video.mp4',
        type: 'video/mp4' as const,
      },
      title: 'Test Video',
      autoPlay: false,
    };

    render(
      <VideoPlayerDialog
        open={false}
        onOpenChange={onOpenChange}
        options={mockVideoOptions}
        onExit={onExit}
      />
    );

    expect(screen.queryByText('Test Video')).not.toBeInTheDocument();
  });

  it('renders video player when dialog is open', () => {
    const onOpenChange = vi.fn();
    const onExit = vi.fn();
    const mockVideoOptions = {
      src: {
        src: 'http://test-video-url.com/video.mp4',
        type: 'video/mp4' as const,
      },
      title: 'Test Video',
      autoPlay: false,
    };

    render(
      <VideoPlayerDialog
        open={true}
        onOpenChange={onOpenChange}
        options={mockVideoOptions}
        onExit={onExit}
      />
    );

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);
  });

  it('does not make network requests', () => {
    const onOpenChange = vi.fn();
    const onExit = vi.fn();
    const mockFetch = vi.fn();
    const originalFetch = global.fetch;
    global.fetch = mockFetch;

    const mockVideoOptions = {
      src: {
        src: 'http://test-video-url.com/video.mp4',
        type: 'video/mp4' as const,
      },
      title: 'Test Video',
      autoPlay: false,
    };

    render(
      <VideoPlayerDialog
        open={true}
        onOpenChange={onOpenChange}
        options={mockVideoOptions}
        onExit={onExit}
      />
    );

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);
    expect(mockFetch).not.toHaveBeenCalled();
    global.fetch = originalFetch;
  });
});
