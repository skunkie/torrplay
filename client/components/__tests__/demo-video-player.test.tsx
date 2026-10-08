// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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
    render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    expect(screen.getByRole('button', { name: 'Seek backward 10 seconds' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Seek forward 10 seconds' })).toBeInTheDocument();
  });

  it('shows time slider controls', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const timeSlider = container.querySelector('[class*="time-slider"], [class*="media-slider"]');
    expect(timeSlider).toBeInTheDocument();
  });

  it('shows fullscreen control button', () => {
    render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    expect(screen.getByRole('button', { name: 'Enter fullscreen' })).toBeInTheDocument();
  });

  it('shows close button when onExit is provided', () => {
    render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    expect(screen.getByRole('button', { name: 'Close player' })).toBeInTheDocument();
  });

  it('does not show close button when onExit is not provided', () => {
    render(<DemoVideoPlayer options={demoVideoOptions} />);

    expect(screen.queryByRole('button', { name: 'Close player' })).not.toBeInTheDocument();
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
    render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={onExit} />);

    await userEvent.click(screen.getByRole('button', { name: 'Close player' }));
    expect(onExit).toHaveBeenCalledTimes(1);
  });

  it('renders with proper text styling', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const textWhiteElements = container.querySelectorAll('.text-white');
    expect(textWhiteElements.length).toBeGreaterThan(0);
  });

  it('does not fabricate audio tracks before media probing finds them', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    const audioTrackBtn = container.querySelector('button[aria-label="Select audio track"]');
    expect(audioTrackBtn).not.toBeInTheDocument();
  });

  it('always uses the internal player in demo mode', () => {
    localStorage.setItem('external_player', 'vlc');
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions} />);

    expect(container.querySelector('[data-media-player]')).toBeInTheDocument();
    localStorage.removeItem('external_player');
  });

  it('shares the live player keyboard shortcut behavior', async () => {
    render(<DemoVideoPlayer options={{
      ...demoVideoOptions,
      tracks: [{
        id: 'english',
        src: 'data:text/vtt,WEBVTT',
        label: 'English',
        type: 'vtt',
        kind: 'subtitles',
      }],
    }} />);

    fireEvent.keyDown(window, { key: 'c' });
    await userEvent.click(screen.getByRole('button', { name: 'Select subtitle track' }));
    expect(screen.getByRole('menuitemradio', { name: /English/ })).toHaveAttribute('aria-checked', 'true');
  });

  it('uses the same accessible controls and visibility behavior as the live player', () => {
    const { container } = render(<DemoVideoPlayer options={demoVideoOptions}
      onExit={vi.fn()} />);

    expect(screen.getByRole('button', { name: 'Play or pause' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Mute or unmute' })).toBeInTheDocument();
    expect(screen.getByRole('slider', { name: 'Seek' })).toBeInTheDocument();
    expect(Array.from(container.querySelectorAll('div'))
      .some(element => element.className.includes('group-data-[controls]:opacity-100'))).toBe(true);
  });
});
