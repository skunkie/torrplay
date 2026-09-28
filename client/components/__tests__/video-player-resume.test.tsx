// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import VideoPlayer from '@/components/video-player';
import { getPlaybackPositionSeconds, savePlaybackPositionSeconds } from '@/lib/playback-position';

interface MediaHandlers {
  onCanPlay?: () => void,
  onEnded?: () => void,
  onPause?: () => void,
  onPlay?: () => void,
  onSeeked?: () => void,
  onTimeUpdate?: (detail: { currentTime: number }) => void
}

const media = vi.hoisted(() => ({
  handlers: {} as MediaHandlers,
  instance: { currentTime: 0, duration: 0, state: { canPlay: false, ended: false }, play: () => Promise.resolve() },
}));

vi.mock('@vidstack/react', async () => {
  const React = await import('react');
  const MockMediaPlayer = React.forwardRef<unknown, MediaHandlers & { children: React.ReactNode }>(
    ({ children, ...handlers }, ref) => {
      React.useImperativeHandle(ref, () => media.instance);
      media.handlers = handlers;
      return <div data-testid='media-player'>{children}</div>;
    },
  );
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

const resumeKey = { hash: 'hash-a', filePath: 'movie.mp4' };
const options = { src: { src: 'http://test-server/movie.mp4', type: 'video/mp4' as const }, title: 'Movie' };

// startPlayback reports that the source can play and has started playing.
function startPlayback() {
  act(() => {
    media.handlers.onCanPlay?.();
    media.handlers.onPlay?.();
  });
}

describe('VideoPlayer playback position', () => {
  beforeEach(() => {
    localStorage.clear();
    media.instance.currentTime = 0;
    media.instance.duration = 600;
    media.instance.state.ended = false;
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('resumes from the saved position when the source can play', () => {
    savePlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath, 120);
    render(<VideoPlayer options={options}
      resumeKey={resumeKey}
      internalOnly />);

    act(() => media.handlers.onCanPlay?.());

    expect(media.instance.currentTime).toBe(120);
  });

  it('starts from the beginning when the saved position is past the end', () => {
    savePlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath, 700);
    render(<VideoPlayer options={options}
      resumeKey={resumeKey}
      internalOnly />);

    act(() => media.handlers.onCanPlay?.());

    expect(media.instance.currentTime).toBe(0);
  });

  it('does not overwrite the saved position before playback starts', () => {
    savePlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath, 120);
    render(<VideoPlayer options={options}
      resumeKey={resumeKey}
      internalOnly />);

    act(() => media.handlers.onTimeUpdate?.({ currentTime: 0 }));

    expect(getPlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath)).toBe(120);
  });

  it('saves the position while playing, at most every five seconds', () => {
    vi.useFakeTimers();
    render(<VideoPlayer options={options}
      resumeKey={resumeKey}
      internalOnly />);
    startPlayback();

    act(() => media.handlers.onTimeUpdate?.({ currentTime: 30 }));
    expect(getPlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath)).toBe(30);

    act(() => media.handlers.onTimeUpdate?.({ currentTime: 31 }));
    expect(getPlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath)).toBe(30);

    vi.advanceTimersByTime(5000);
    act(() => media.handlers.onTimeUpdate?.({ currentTime: 36 }));
    expect(getPlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath)).toBe(36);
  });

  it('saves the position at once on pause and seek', () => {
    render(<VideoPlayer options={options}
      resumeKey={resumeKey}
      internalOnly />);
    startPlayback();
    act(() => media.handlers.onTimeUpdate?.({ currentTime: 30 }));

    media.instance.currentTime = 42;
    act(() => media.handlers.onPause?.());
    expect(getPlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath)).toBe(42);

    media.instance.currentTime = 300;
    act(() => media.handlers.onSeeked?.());
    expect(getPlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath)).toBe(300);
  });

  it('saves the latest position when the player closes', () => {
    const { unmount } = render(<VideoPlayer options={options}
      resumeKey={resumeKey}
      internalOnly />);
    startPlayback();
    act(() => {
      media.handlers.onTimeUpdate?.({ currentTime: 30 });
      media.handlers.onTimeUpdate?.({ currentTime: 31 });
    });

    unmount();

    expect(getPlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath)).toBe(31);
  });

  it('forgets the position of a finished file', () => {
    render(<VideoPlayer options={options}
      resumeKey={resumeKey}
      internalOnly />);
    startPlayback();
    act(() => media.handlers.onTimeUpdate?.({ currentTime: 30 }));

    media.instance.state.ended = true;
    act(() => {
      media.handlers.onPause?.();
      media.handlers.onEnded?.();
    });

    expect(getPlaybackPositionSeconds(resumeKey.hash, resumeKey.filePath)).toBe(0);
  });

  it('saves nothing without a resume key', () => {
    render(<VideoPlayer options={options}
      internalOnly />);
    startPlayback();

    act(() => media.handlers.onTimeUpdate?.({ currentTime: 30 }));

    expect(localStorage.getItem('torrplay_playback_positions')).toBeNull();
  });
});
