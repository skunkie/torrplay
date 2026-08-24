// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import VideoPlayer from '@/components/video-player';
import * as mkvAudioModule from '@/lib/mkv-audio';

const { mockTracks, mockEngine } = vi.hoisted(() => {
  const mockTracks = [
    {
      id: 1,
      index: 0,
      name: 'Main Track',
      language: 'eng',
      codec: 'ac3',
      channels: 6,
      sampleRate: 48000,
      isDefault: true,
      isNativelySupported: false,
      label: 'Main Track - English (AC3, 5.1)',
    },
    {
      id: 2,
      index: 1,
      name: 'Secondary',
      language: 'spa',
      codec: 'aac',
      channels: 2,
      sampleRate: 44100,
      isDefault: false,
      isNativelySupported: true,
      label: 'Secondary - Spanish (AAC, Stereo)',
    },
  ];

  const mockEngine = {
    selectTrack: vi.fn(),
    setWasmActive: vi.fn(),
    attachMediaElement: vi.fn(),
    setVolume: vi.fn(),
    setMuted: vi.fn(),
    setPlaybackRate: vi.fn(),
    onPlay: vi.fn(),
    onPause: vi.fn(),
    onSeek: vi.fn(),
    destroy: vi.fn(),
  };

  return { mockTracks, mockEngine };
});

vi.mock('@/lib/mkv-audio', async importOriginal => {
  const actual = await importOriginal<typeof import('@/lib/mkv-audio')>();
  return {
    ...actual,
    isAudioDecodingSupported: vi.fn().mockReturnValue(true),
    probeAudioTracks: vi.fn().mockResolvedValue({
      input: { dispose: vi.fn() },
      tracks: mockTracks,
      audioTrackObjects: [{ id: 1 }, { id: 2 }],
    }),
    MkvAudioSyncEngine: vi.fn(function MockEngine() {
      return mockEngine;
    }),
  };
});

describe('VideoPlayer MKV Audio Integration', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('probes audio tracks when source is MKV and initializes sync engine', async () => {
    render(
      <VideoPlayer
        options={{
          src: {
            src: 'http://test-server/movie.mkv',
            type: 'video/mp4' as const,
          },
          title: 'Test Movie.mkv',
          autoPlay: false,
        }}
      />
    );

    await waitFor(() => {
      expect(mkvAudioModule.probeAudioTracks).toHaveBeenCalledWith('http://test-server/movie.mkv');
      expect(mkvAudioModule.MkvAudioSyncEngine).toHaveBeenCalled();
      expect(mockEngine.setVolume).toHaveBeenCalled();
      expect(mockEngine.setMuted).toHaveBeenCalled();
    });
  });

  it('renders AudioTrackSelector when audio tracks are discovered', async () => {
    render(
      <VideoPlayer
        options={{
          src: {
            src: 'http://test-server/movie.mkv',
            type: 'video/mp4' as const,
          },
          title: 'Test Movie.mkv',
        }}
      />
    );

    const audioSelectorBtn = await screen.findByRole('button', { name: /select audio track/i });
    expect(audioSelectorBtn).toBeInTheDocument();

    await userEvent.click(audioSelectorBtn);
    expect(screen.getByText(/Audio Tracks \(2\)/i)).toBeInTheDocument();
    expect(screen.getByText('Main Track - English (AC3, 5.1)')).toBeInTheDocument();
  });

  it('switches audio track without muting player and updates sync engine', async () => {
    render(
      <VideoPlayer
        options={{
          src: {
            src: 'http://test-server/movie.mkv',
            type: 'video/mp4' as const,
          },
          title: 'Test Movie.mkv',
        }}
      />
    );

    const audioSelectorBtn = await screen.findByRole('button', { name: /select audio track/i });
    await userEvent.click(audioSelectorBtn);

    const secondTrack = await screen.findByText('Secondary - Spanish (AAC, Stereo)');
    await userEvent.click(secondTrack);

    expect(mockEngine.selectTrack).toHaveBeenCalledWith(1);
    expect(mockEngine.setVolume).toHaveBeenCalled();
  });

  it('selects and activates default audio track 0 via WASM engine when track 0 is non-native (e.g. AC-3)', async () => {
    render(
      <VideoPlayer
        options={{
          src: {
            src: 'http://test-server/movie.mkv',
            type: 'video/mp4' as const,
          },
          title: 'Test Movie.mkv',
          autoPlay: false,
        }}
      />
    );

    await waitFor(() => {
      expect(mockEngine.selectTrack).toHaveBeenCalledWith(0);
    });
  });

  it('uses native audio playback and does not activate WASM engine when default track 0 is natively supported (e.g. AAC)', async () => {
    const nativeTracks = [
      {
        id: 1,
        index: 0,
        name: 'Native AAC Track',
        language: 'eng',
        codec: 'aac',
        channels: 2,
        sampleRate: 48000,
        isDefault: true,
        isNativelySupported: true,
        label: 'Native AAC Track - English (AAC, Stereo)',
      },
      {
        id: 2,
        index: 1,
        name: 'Secondary AC3',
        language: 'spa',
        codec: 'ac3',
        channels: 6,
        sampleRate: 48000,
        isDefault: false,
        isNativelySupported: false,
        label: 'Secondary AC3 - Spanish (AC3, 5.1)',
      },
    ];

    vi.mocked(mkvAudioModule.probeAudioTracks).mockResolvedValueOnce({
      input: { dispose: vi.fn() } as unknown as import('mediabunny').Input,
      tracks: nativeTracks,
      audioTrackObjects: [{ id: 1 }, { id: 2 }] as unknown as import('mediabunny').InputAudioTrack[],
    });

    render(
      <VideoPlayer
        options={{
          src: {
            src: 'http://test-server/movie.mp4',
            type: 'video/mp4' as const,
          },
          title: 'Test Movie.mp4',
          autoPlay: false,
        }}
      />
    );

    await waitFor(() => {
      expect(mkvAudioModule.probeAudioTracks).toHaveBeenCalledWith('http://test-server/movie.mp4');
      expect(mkvAudioModule.MkvAudioSyncEngine).toHaveBeenCalled();
    });

    // WASM engine selectTrack should NOT be called on mount because track 0 is natively supported
    expect(mockEngine.selectTrack).not.toHaveBeenCalled();

    // Selecting secondary non-native track activates WASM engine
    const audioSelectorBtn = await screen.findByRole('button', { name: /select audio track/i });
    await userEvent.click(audioSelectorBtn);

    const secondTrack = await screen.findByText('Secondary AC3 - Spanish (AC3, 5.1)');
    await userEvent.click(secondTrack);

    expect(mockEngine.selectTrack).toHaveBeenCalledWith(1);

    // Switching back to native track 0 pauses WASM engine
    await userEvent.click(audioSelectorBtn);
    const firstTrack = await screen.findByText('Native AAC Track - English (AAC, Stereo)');
    await userEvent.click(firstTrack);

    expect(mockEngine.onPause).toHaveBeenCalled();
  });

  it('honors a native default audio track whose container index is not zero', async () => {
    const nonzeroDefaultTracks = [
      {
        id: 1,
        index: 0,
        name: 'Alternate Mix',
        language: 'eng',
        codec: 'opus',
        channels: 2,
        sampleRate: 48000,
        isDefault: false,
        isNativelySupported: true,
        label: 'Alternate Mix - English (OPUS, Stereo)',
      },
      {
        id: 2,
        index: 1,
        name: 'Main Mix',
        language: 'eng',
        codec: 'opus',
        channels: 2,
        sampleRate: 48000,
        isDefault: true,
        isNativelySupported: true,
        label: 'Main Mix - English (OPUS, Stereo)',
      },
    ];

    vi.mocked(mkvAudioModule.probeAudioTracks).mockResolvedValueOnce({
      input: { dispose: vi.fn() } as unknown as import('mediabunny').Input,
      tracks: nonzeroDefaultTracks,
      audioTrackObjects: [{ id: 1 }, { id: 2 }] as unknown as import('mediabunny').InputAudioTrack[],
    });

    render(<VideoPlayer options={{
      src: { src: 'http://test-server/nonzero-default.webm', type: 'video/webm' as const },
      title: 'Non-zero default.webm',
    }} />);

    const audioSelectorBtn = await screen.findByRole('button', { name: /select audio track/i });
    await userEvent.click(audioSelectorBtn);
    const items = screen.getAllByRole('menuitemradio');
    expect(items[0]).toHaveAttribute('aria-checked', 'false');
    expect(items[1]).toHaveAttribute('aria-checked', 'true');
    expect(mockEngine.selectTrack).not.toHaveBeenCalled();

    await userEvent.click(screen.getByText('Alternate Mix - English (OPUS, Stereo)'));
    expect(mockEngine.selectTrack).toHaveBeenCalledWith(0);

    await userEvent.click(audioSelectorBtn);
    await userEvent.click(screen.getByText('Main Mix - English (OPUS, Stereo)'));
    expect(mockEngine.onPause).toHaveBeenCalled();
  });

  it('probes audio tracks for other formats such as MP4', async () => {
    render(
      <VideoPlayer
        options={{
          src: {
            src: 'http://test-server/movie.mp4',
            type: 'video/mp4' as const,
          },
          title: 'Test Movie.mp4',
          autoPlay: false,
        }}
      />
    );

    await waitFor(() => {
      expect(mkvAudioModule.probeAudioTracks).toHaveBeenCalledWith('http://test-server/movie.mp4');
      expect(mkvAudioModule.MkvAudioSyncEngine).toHaveBeenCalled();
    });
  });

  it('does not probe audio tracks when isAudioDecodingSupported is false (e.g. insecure context)', async () => {
    vi.mocked(mkvAudioModule.isAudioDecodingSupported).mockReturnValueOnce(false);

    render(
      <VideoPlayer
        options={{
          src: {
            src: 'http://test-server/movie.mkv',
            type: 'video/mp4' as const,
          },
          title: 'Test Movie.mkv',
          autoPlay: false,
        }}
      />
    );

    expect(mkvAudioModule.probeAudioTracks).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: /select audio track/i })).not.toBeInTheDocument();
  });

  it('handles audio track probe errors gracefully without crashing', async () => {
    const consoleSpy = vi.spyOn(console, 'debug').mockImplementation(() => {});
    vi.mocked(mkvAudioModule.probeAudioTracks).mockRejectedValueOnce(new Error('Corrupt header'));

    render(
      <VideoPlayer
        options={{
          src: {
            src: 'http://test-server/corrupt.mkv',
            type: 'video/mp4' as const,
          },
          title: 'Corrupt.mkv',
        }}
      />
    );

    await waitFor(() => {
      expect(consoleSpy).toHaveBeenCalledWith(
        'Failed to probe audio tracks for stream:',
        expect.any(Error)
      );
    });

    consoleSpy.mockRestore();
  });

  it('destroys sync engine and resets audio tracks when unmounted', async () => {
    const { unmount } = render(
      <VideoPlayer
        options={{
          src: {
            src: 'http://test-server/movie.mkv',
            type: 'video/mp4' as const,
          },
          title: 'Test Movie.mkv',
        }}
      />
    );

    await waitFor(() => {
      expect(mkvAudioModule.MkvAudioSyncEngine).toHaveBeenCalled();
    });

    unmount();
    expect(mockEngine.destroy).toHaveBeenCalled();
  });
});
