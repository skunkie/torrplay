// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, render, renderHook, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MediaPlayer, type MediaPlayerInstance, MediaProvider } from '@vidstack/react';
import { describe, expect, it, vi } from 'vitest';

import { type AudioTrackInfo } from '@/lib/mkv-audio';
import { type SubtitleTrackInfo } from '@/lib/video-utils';

import { useVideoPlayerControls, VideoPlayerControls } from '../video-player-controls';

const audioTracks: AudioTrackInfo[] = [
  {
    id: 1,
    index: 0,
    name: 'Main',
    language: 'eng',
    codec: 'ac3',
    channels: 6,
    sampleRate: 48000,
    isDefault: true,
    isNativelySupported: false,
    label: 'Main - English (AC3, 5.1)',
  },
];

const subtitleTracks: SubtitleTrackInfo[] = [
  { id: 'subs/english.srt', src: 'http://localhost/subs/english.srt', label: 'English (SRT)' },
];

function renderControls(props: Partial<React.ComponentProps<typeof VideoPlayerControls>> = {}) {
  return render(
    <MediaPlayer src='http://test-server/movie.mp4'>
      <MediaProvider />
      <VideoPlayerControls
        {...props}
        onSeek={vi.fn()}
        isFullscreen={false}
        onToggleFullscreen={vi.fn()}
        audioTracks={audioTracks}
        selectedAudioTrack={0}
        onSelectAudioTrack={vi.fn()}
        subtitleTracks={subtitleTracks}
        selectedSubtitleTrack={null}
        onSelectSubtitleTrack={vi.fn()}
      />
    </MediaPlayer>,
  );
}

describe('VideoPlayerControls', () => {
  it('closes the audio menu when the subtitle menu is opened, instead of stacking both', async () => {
    renderControls();

    await userEvent.click(screen.getByRole('button', { name: /select audio track/i }));
    expect(screen.getByText(/Audio Tracks \(1\)/i)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));
    expect(screen.getByText(/Subtitles \(1\)/i)).toBeInTheDocument();
    expect(screen.queryByText(/Audio Tracks \(1\)/i)).not.toBeInTheDocument();
  });

  it('closes the subtitle menu when the audio menu is opened, instead of stacking both', async () => {
    renderControls();

    await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));
    expect(screen.getByText(/Subtitles \(1\)/i)).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /select audio track/i }));
    expect(screen.getByText(/Audio Tracks \(1\)/i)).toBeInTheDocument();
    expect(screen.queryByText(/Subtitles \(1\)/i)).not.toBeInTheDocument();
  });
});

describe('VideoPlayerControls visibility', () => {
  // controlsLayer returns the layer holding every control, whose opacity
  // shows or hides them.
  const controlsLayer = () => screen.getByRole('button', { name: 'Close player' }).parentElement!;

  it('offers closing, playlist navigation, and fullscreen at all times without a source', () => {
    renderControls({ mediaUnavailable: true, onExit: vi.fn(), playlistNavigation: { onNext: vi.fn() } });

    expect(controlsLayer()).toHaveClass('opacity-100');
    for (const name of ['Close player', 'Previous video', 'Next video', 'Enter fullscreen']) {
      expect(screen.getByRole('button', { name })).toBeInTheDocument();
    }
    for (const name of [/play or pause/i, /seek backward/i, /seek forward/i, /mute or unmute/i, /select audio track/i]) {
      expect(screen.queryByRole('button', { name })).not.toBeInTheDocument();
    }
    expect(screen.queryByRole('slider', { name: 'Seek' })).not.toBeInTheDocument();
  });

  it('keeps every control shown when asked, as for a source that failed to load', () => {
    renderControls({ keepVisible: true, onExit: vi.fn() });

    expect(controlsLayer()).toHaveClass('opacity-100');
    expect(screen.getByRole('button', { name: /play or pause/i })).toBeInTheDocument();
  });

  it('leaves showing the controls to Vidstack otherwise', () => {
    renderControls({ onExit: vi.fn() });

    expect(controlsLayer()).toHaveClass('opacity-0');
    expect(controlsLayer()).not.toHaveClass('opacity-100');
  });
});

describe('useVideoPlayerControls', () => {
  it('toggleFullscreen decides from the live player state, not a stale isFullscreen mirror', async () => {
    const enterFullscreen = vi.fn().mockResolvedValue(undefined);
    const exitFullscreen = vi.fn().mockResolvedValue(undefined);
    const rawPlayer = {
      state: { fullscreen: false },
      enterFullscreen,
      exitFullscreen,
    };
    const player = { current: rawPlayer as unknown as MediaPlayerInstance };

    const { result } = renderHook(() => useVideoPlayerControls({
      player,
      subtitleTracks: [],
      selectedSubtitleTrack: null,
      onSelectSubtitleTrack: vi.fn(),
    }));

    // The player is actually fullscreen (e.g. a source change forced the browser out
    // of fullscreen and back in on a fresh player instance without the mirrored
    // isFullscreen React state - which is still its initial `false` here - ever
    // catching up). toggleFullscreen must still exit, not try to enter again.
    rawPlayer.state.fullscreen = true;

    await act(async () => {
      result.current.toggleFullscreen();
    });

    expect(exitFullscreen).toHaveBeenCalledTimes(1);
    expect(enterFullscreen).not.toHaveBeenCalled();
  });
});
