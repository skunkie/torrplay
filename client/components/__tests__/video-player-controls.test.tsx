// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MediaPlayer, MediaProvider } from '@vidstack/react';
import { describe, expect, it, vi } from 'vitest';

import { type AudioTrackInfo } from '@/lib/mkv-audio';
import { type SubtitleTrackInfo } from '@/lib/video-utils';

import { VideoPlayerControls } from '../video-player-controls';

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

function renderControls() {
  return render(
    <MediaPlayer src='http://test-server/movie.mp4'>
      <MediaProvider />
      <VideoPlayerControls
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
