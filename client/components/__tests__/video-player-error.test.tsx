// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import type { MediaErrorDetail } from '@vidstack/react';
import { expect, it, vi } from 'vitest';

import VideoPlayer from '@/components/video-player';

vi.mock('@vidstack/react', async () => {
  const React = await import('react');
  const MockMediaPlayer = React.forwardRef<unknown, {
    children: React.ReactNode,
    onError?: (detail: MediaErrorDetail) => void
  }>(({ children, onError }, ref) => {
    React.useImperativeHandle(ref, () => null);
    return (
      <div data-testid='media-player'>
        <button type='button'
          onClick={() => onError?.({ message: 'Failed to load resource.', code: 4 })}>
          Fail playback (unsupported format)
        </button>
        <button type='button'
          onClick={() => onError?.({ message: 'network error', code: 2 })}>
          Fail playback (network)
        </button>
        {children}
      </div>
    );
  });
  MockMediaPlayer.displayName = 'MockMediaPlayer';
  return {
    MediaPlayer: MockMediaPlayer,
    MediaProvider: () => null,
  };
});

vi.mock('@/components/video-player-controls', () => ({
  useVideoPlayerControls: () => ({
    isFullscreen: false,
    setIsFullscreen: vi.fn(),
    seek: vi.fn(),
    toggleFullscreen: vi.fn(),
  }),
  VideoPlayerCaptions: () => null,
  VideoPlayerControls: () => null,
}));

vi.mock('@/hooks/use-subtitle-tracks', () => ({
  useSubtitleTracks: () => ({
    tracks: [],
    selectedTrackId: null,
    selectTrack: vi.fn(),
  }),
}));

vi.mock('@/lib/mkv-audio', async importOriginal => ({
  ...await importOriginal<typeof import('@/lib/mkv-audio')>(),
  isAudioDecodingSupported: () => false,
}));

it('shows a codec-specific error when the source is unsupported', () => {
  render(<VideoPlayer options={{
    src: { src: 'http://test-server/movie.mp4', type: 'video/mp4' },
    title: 'Movie',
  }} />);

  fireEvent.click(screen.getByRole('button', { name: 'Fail playback (unsupported format)' }));

  expect(screen.getByRole('alert')).toHaveTextContent(
    'This video container or codec is not supported by the internal player.',
  );
});

it('shows a network-specific error instead of a misleading codec message', () => {
  render(<VideoPlayer options={{
    src: { src: 'http://test-server/movie.mp4', type: 'video/mp4' },
    title: 'Movie',
  }} />);

  fireEvent.click(screen.getByRole('button', { name: 'Fail playback (network)' }));

  expect(screen.getByRole('alert')).toHaveTextContent(
    'A network error interrupted playback. Check your connection and try again.',
  );
});
