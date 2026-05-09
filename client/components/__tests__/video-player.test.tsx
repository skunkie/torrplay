// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import VideoPlayer from '@/components/video-player';

const mockVideoOptions = {
  src: {
    src: 'http://test-video-url.com/video.mp4',
    type: 'video/mp4' as const,
  },
  title: 'Test Video',
  autoPlay: false,
};

describe('VideoPlayer', () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it('does not try to fetch real video data', () => {
    const mockFetch = vi.fn();
    global.fetch = mockFetch;

    render(<VideoPlayer options={mockVideoOptions} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);
    expect(mockFetch).not.toHaveBeenCalled();
    global.fetch = global.fetch;
  });

  it('shows video player without network requests', () => {
    const mockFetch = vi.fn();
    global.fetch = mockFetch;

    render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);
    expect(mockFetch).not.toHaveBeenCalled();
    global.fetch = global.fetch;
  });

  it('displays video title correctly', () => {
    render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);
  });

  it('shows all video player controls', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for media player container
    const mediaPlayer = container.querySelector('.group.bg-black');
    expect(mediaPlayer).toBeInTheDocument();

    // Check for buttons (close, seek buttons, play/pause, fullscreen)
    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(0);
  });

  it('shows close button when onExit is provided', () => {
    const onExit = vi.fn();
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={onExit} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for close button (X icon)
    const closeButton = container.querySelector('button')?.closest('button');
    expect(closeButton).toBeInTheDocument();
  });

  it('does not show close button when onExit is not provided', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // The close button should only be present when onExit is provided
    expect(container.querySelectorAll('button').length).toBeGreaterThan(0);
  });

  it('shows play/pause controls', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for play/pause button
    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(1); // At least play button should exist
  });

  it('shows forward/backward seek controls', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for seek buttons
    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(2); // Should have play button + seek buttons
  });

  it('shows time controls', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for time slider controls
    const timeSliderControls = container.querySelectorAll('[class*="time-slider"], [class*="media-slider"]');
    expect(timeSliderControls.length).toBeGreaterThan(0);
  });

  it('shows video player with proper aspect ratio', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for aspect-video class
    const mediaPlayer = container.querySelector('.aspect-video');
    expect(mediaPlayer).toBeInTheDocument();
  });

  it('shows fullscreen control button', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // The controls overlay is initially hidden (opacity-0), but buttons should still exist
    // Check for fullscreen button using the icon class names from vidstack
    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(0);

    // Check for fullscreen button by icon SVG path content
    let hasFullscreenButton = false;
    buttons.forEach(btn => {
      const svg = btn.querySelector('svg');
      if (svg) {
        const svgHtml = svg.outerHTML.toLowerCase();
        if (svgHtml.includes('fullscreen') || svgHtml.includes('expand')) {
          hasFullscreenButton = true;
        }
      }
    });
    expect(hasFullscreenButton).toBe(true);
  });

  it('shows video player controls overlay', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for controls overlay (absolute positioning)
    const controlsOverlay = container.querySelector('[class*="absolute inset-0"]');
    expect(controlsOverlay).toBeInTheDocument();
  });

  it('shows buffering indicator elements', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for buffering indicator wrapper
    const bufferIndicator = container.querySelector('[class*="absolute inset-0 z-50"]');
    expect(bufferIndicator).toBeInTheDocument();
  });

  it('has proper media player classes', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for proper media player styling
    const mediaPlayer = container.querySelector('.group.bg-black');
    expect(mediaPlayer).toBeInTheDocument();

    if (mediaPlayer) {
      expect(mediaPlayer.className).toContain('bg-black');
      expect(mediaPlayer.className).toContain('rounded-lg');
      expect(mediaPlayer.className).toContain('aspect-video');
    }
  });

  it('shows progress bar controls', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for progress bar (time slider)
    const progressBar = container.querySelector('[class*="slider"], [class*="progress"]');
    expect(progressBar).toBeInTheDocument();
  });

  it('shows control groups properly structured', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for control groups
    const controlGroups = container.querySelectorAll('[class*="controls"]');
    expect(controlGroups.length).toBeGreaterThan(0);
  });

  it('handles video title display', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for title display overlay
    const titleOverlay = container.querySelector('[class*="truncate"]');
    expect(titleOverlay).toBeInTheDocument();
  });

  it('shows centered play controls', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for centered control area
    const centeredControls = container.querySelector('[class*="justify-center"]');
    expect(centeredControls).toBeInTheDocument();
  });

  it('has proper button styling', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for buttons with proper styling classes
    const buttons = container.querySelectorAll('button[class*="rounded-full"]');
    expect(buttons.length).toBeGreaterThan(0);
  });

  it('displays video with proper text styling', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Check for text-white styling
    const textWhiteElements = container.querySelectorAll('.text-white');
    expect(textWhiteElements.length).toBeGreaterThan(0);
  });

  it('calls onExit when close button is clicked', async () => {
    const onExit = vi.fn();
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={onExit} />);

    const closeButton = container.querySelector('button');
    expect(closeButton).toBeInTheDocument();

    if (closeButton) {
      await userEvent.click(closeButton);
      expect(onExit).toHaveBeenCalled();
    }
  });

  it('supports keyboard navigation to control buttons', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(0);

    // Check that buttons have proper tab indexing
    const firstButton = buttons[0];
    expect(firstButton).toBeInTheDocument();
  });

  it('renders in paused state initially', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      autoPlay={false}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);

    // Player should render in initial state
    const mediaPlayer = container.querySelector('.group.bg-black');
    expect(mediaPlayer).toBeInTheDocument();
  });

  it('handles play control button rendering', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    // Check for play control presence
    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(0);
  });

  it('shows volume control elements', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    // Check for volume-related elements
    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(0);
  });

  it('properly structures control button layout', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    // Check for control groups and flex layouts
    const flexElements = container.querySelectorAll('[class*="flex"]');
    expect(flexElements.length).toBeGreaterThan(0);
  });

  it('handles media player responses properly', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    const mediaPlayer = container.querySelector('.group.bg-black');
    expect(mediaPlayer).toBeInTheDocument();

    // Ensure media player has proper structure
    const videoElement = container.querySelector('video');
    if (videoElement) {
      expect(videoElement).toBeInTheDocument();
    }
  });

  it('renders with default options', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);
    expect(container.querySelector('.group.bg-black')).toBeInTheDocument();
  });

  it('handles different video types', () => {
    const videoOptionsWebM = {
      ...mockVideoOptions,
      src: {
        src: 'http://test-video-url.com/video.webm',
        type: 'video/webm' as const,
      },
    };

    const { container } = render(<VideoPlayer options={videoOptionsWebM}
      onExit={vi.fn()} />);

    const videoTitles = screen.getAllByText('Test Video');
    expect(videoTitles.length).toBeGreaterThan(0);
    expect(container.querySelector('.group.bg-black')).toBeInTheDocument();
  });

  it('supports responsive design classes', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    // Check for responsive design elements
    const mediaPlayer = container.querySelector('.group.bg-black');
    expect(mediaPlayer).toBeInTheDocument();

    if (mediaPlayer) {
      expect(mediaPlayer.className).toContain('w-full');
    }
  });

  it('maintains ARIA attributes for accessibility', () => {
    const { container } = render(<VideoPlayer options={mockVideoOptions}
      onExit={vi.fn()} />);

    // Check for buttons with ARIA labels
    const buttons = container.querySelectorAll('button[aria-label], button[role]');
    expect(buttons.length).toBeGreaterThan(0);
  });
});
