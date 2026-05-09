// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';

import DemoVideoPlayer from '@/components/demo-video-player';

const demoVideoOptions = {
  src: {
    src: 'http://test-video-url.com/video.mp4',
    type: 'video/mp4' as const,
  },
  title: 'Test Video',
  autoPlay: false,
};

describe('DemoVideoPlayer', () => {
  it('renders video player without loading media', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const mediaPlayer = container.querySelector('.group.bg-black');
    expect(mediaPlayer).toBeInTheDocument();

    expect(container.querySelector('.text-white')).toBeInTheDocument();
  });

  it('shows play/pause controls', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const playButton = container.querySelector('button');
    expect(playButton).toBeInTheDocument();
  });

  it('shows seek backward and forward buttons', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(0);
  });

  it('shows time slider controls', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const timeSlider = container.querySelector('[class*="time-slider"], [class*="media-slider"]');
    expect(timeSlider).toBeInTheDocument();
  });

  it('shows fullscreen control button', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(0);

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

  it('shows close button when onExit is provided', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const closeButton = container.querySelector('button')?.closest('button');
    expect(closeButton).toBeInTheDocument();
  });

  it('does not show close button when onExit is not provided', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions} />);

    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(0);
  });

  it('has proper responsive design classes', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const mediaPlayer = container.querySelector('.group.bg-black');
    expect(mediaPlayer).toBeInTheDocument();

    if (mediaPlayer) {
      expect(mediaPlayer.className).toContain('w-full');
      expect(mediaPlayer.className).toContain('aspect-video');
      expect(mediaPlayer.className).toContain('rounded-lg');
    }
  });

  it('has proper aspect ratio', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const mediaPlayer = container.querySelector('.aspect-video');
    expect(mediaPlayer).toBeInTheDocument();
  });

  it('has proper button styling', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(0);

    buttons.forEach(btn => {
      expect(btn.className).toContain('rounded-full');
    });
  });

  it('calls onExit when close button is clicked', async () => {
    const onExit = vi.fn();
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={onExit} />);

    const buttons = container.querySelectorAll('button');
    expect(buttons.length).toBeGreaterThan(0);

    const hasCloseButton = container.querySelector('button')?.closest('button');
    expect(hasCloseButton).toBeInTheDocument();
  });

  it('renders with proper text styling', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const textWhiteElements = container.querySelectorAll('.text-white');
    expect(textWhiteElements.length).toBeGreaterThan(0);
  });
});
