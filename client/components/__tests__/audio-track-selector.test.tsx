// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { AudioTrackSelector } from '@/components/audio-track-selector';
import { type AudioTrackInfo } from '@/lib/mkv-audio';

const mockTracks: AudioTrackInfo[] = [
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
  {
    id: 2,
    index: 1,
    name: 'Commentary',
    language: 'rus',
    codec: 'aac',
    channels: 2,
    sampleRate: 44100,
    isDefault: false,
    isNativelySupported: true,
    label: 'Commentary - Russian (AAC, Stereo)',
  },
];

describe('AudioTrackSelector', () => {
  it('renders nothing when track list is empty', () => {
    const { container } = render(
      <AudioTrackSelector tracks={[]}
        selectedTrackIndex={0}
        onSelectTrack={vi.fn()} />
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('renders audio track button and toggles menu open and closed', async () => {
    render(
      <AudioTrackSelector
        tracks={mockTracks}
        selectedTrackIndex={0}
        onSelectTrack={vi.fn()}
      />
    );

    const button = screen.getByRole('button', { name: /select audio track/i });
    expect(button).toBeInTheDocument();

    // Initially menu is not open
    expect(screen.queryByText(/Audio Tracks \(2\)/i)).not.toBeInTheDocument();

    // Click to open
    await userEvent.click(button);
    expect(screen.getByText(/Audio Tracks \(2\)/i)).toBeInTheDocument();

    // Click button again to close
    await userEvent.click(button);
    expect(screen.queryByText(/Audio Tracks \(2\)/i)).not.toBeInTheDocument();
  });

  it('closes menu when backdrop overlay is clicked', async () => {
    const { container } = render(
      <AudioTrackSelector
        tracks={mockTracks}
        selectedTrackIndex={0}
        onSelectTrack={vi.fn()}
      />
    );

    const button = screen.getByRole('button', { name: /select audio track/i });
    await userEvent.click(button);
    expect(screen.getByText(/Audio Tracks \(2\)/i)).toBeInTheDocument();

    // Find backdrop element
    const backdrop = container.querySelector('.fixed.inset-0');
    expect(backdrop).toBeInTheDocument();

    if (backdrop) {
      await userEvent.click(backdrop);
    }
    expect(screen.queryByText(/Audio Tracks \(2\)/i)).not.toBeInTheDocument();
  });

  it('renders audio tracks with WASM Decoded badge for non-native codecs', async () => {
    render(
      <AudioTrackSelector
        tracks={mockTracks}
        selectedTrackIndex={0}
        onSelectTrack={vi.fn()}
      />
    );

    await userEvent.click(screen.getByRole('button', { name: /select audio track/i }));

    expect(screen.getByText('Main - English (AC3, 5.1)')).toBeInTheDocument();
    expect(screen.getByText('WASM Decoded (AC3)')).toBeInTheDocument();

    expect(screen.getByText('Commentary - Russian (AAC, Stereo)')).toBeInTheDocument();
    // Track 2 is AAC (natively supported), so no WASM Decoded badge for it
    expect(screen.queryByText('WASM Decoded (AAC)')).not.toBeInTheDocument();
  });

  it('closes menu when Escape key is pressed', async () => {
    render(
      <AudioTrackSelector
        tracks={mockTracks}
        selectedTrackIndex={0}
        onSelectTrack={vi.fn()}
      />
    );

    const button = screen.getByRole('button', { name: /select audio track/i });
    await userEvent.click(button);
    expect(screen.getByText(/Audio Tracks \(2\)/i)).toBeInTheDocument();

    await userEvent.keyboard('{Escape}');
    expect(screen.queryByText(/Audio Tracks \(2\)/i)).not.toBeInTheDocument();
  });

  it('sets proper ARIA attributes when closed and open', async () => {
    render(
      <AudioTrackSelector
        tracks={mockTracks}
        selectedTrackIndex={0}
        onSelectTrack={vi.fn()}
      />
    );

    const button = screen.getByRole('button', { name: /select audio track/i });
    expect(button).toHaveAttribute('aria-haspopup', 'menu');
    expect(button).toHaveAttribute('aria-expanded', 'false');

    await userEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('menu', { name: /audio tracks/i })).toBeInTheDocument();

    const menuItems = screen.getAllByRole('menuitemradio');
    expect(menuItems).toHaveLength(2);
    expect(menuItems[0]).toHaveAttribute('aria-checked', 'true');
    expect(menuItems[1]).toHaveAttribute('aria-checked', 'false');
  });

  it('calls onSelectTrack with track index when an option is selected', async () => {
    const onSelectTrack = vi.fn();
    render(
      <AudioTrackSelector
        tracks={mockTracks}
        selectedTrackIndex={0}
        onSelectTrack={onSelectTrack}
      />
    );

    await userEvent.click(screen.getByRole('button', { name: /select audio track/i }));

    const secondTrackBtn = screen.getByText('Commentary - Russian (AAC, Stereo)');
    await userEvent.click(secondTrackBtn);

    expect(onSelectTrack).toHaveBeenCalledWith(1);
    expect(screen.queryByText(/Audio Tracks \(2\)/i)).not.toBeInTheDocument();
  });
});
