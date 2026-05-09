// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { VideoPlayerLayout } from '@/components/video-player-layout';

describe('VideoPlayerLayout', () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

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
      <VideoPlayerLayout
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
      <VideoPlayerLayout
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
      <VideoPlayerLayout
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
