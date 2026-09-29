// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import type { MediaErrorDetail } from '@vidstack/react';
import { expect, it, vi } from 'vitest';

import VideoPlayer from '@/components/video-player';

const media = vi.hoisted(() => ({ src: undefined as unknown }));

vi.mock('@vidstack/react', async () => {
  const React = await import('react');
  const MockMediaPlayer = React.forwardRef<unknown, {
    children: React.ReactNode,
    onError?: (detail: MediaErrorDetail) => void,
    src?: unknown
  }>(({ children, onError, src }, ref) => {
    React.useImperativeHandle(ref, () => null);
    media.src = src;
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

it('shows the MKV message without loading the file where the video element reports no Matroska support', () => {
  // Safari may load an MKV file indefinitely without reporting an error.
  const canPlayType = vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('');
  render(<VideoPlayer options={{
    src: { src: 'http://test-server/api/v1/stream/hash?path=Show%2FMovie.mkv&token=t', type: 'video/mp4' },
    title: 'Movie',
  }} />);

  expect(screen.getByRole('alert')).toHaveTextContent(
    'MKV files cannot be played here. Open the file in an external player.',
  );
  expect(media.src).toBeUndefined();
  canPlayType.mockRestore();
});

it('loads an MKV file and keeps the standard errors in a browser that plays Matroska', () => {
  const canPlayType = vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('maybe');
  const src = { src: 'http://test-server/api/v1/stream/hash?path=movie.mkv', type: 'video/mp4' as const };
  render(<VideoPlayer options={{ src, title: 'Movie' }} />);

  expect(media.src).toBe(src);
  expect(screen.queryByRole('alert')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Fail playback (unsupported format)' }));

  expect(screen.getByRole('alert')).toHaveTextContent(
    'This video container or codec is not supported by the internal player.',
  );
  canPlayType.mockRestore();
});
