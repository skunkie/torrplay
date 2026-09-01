// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { SubtitleTrackSelector } from '@/components/subtitle-track-selector';
import { type SubtitleTrackInfo } from '@/lib/video-utils';

const mockTracks: SubtitleTrackInfo[] = [
  {
    id: 'subs/english.srt',
    src: 'http://localhost/api/v1/stream/hash?path=subs/english.srt',
    label: 'English (SRT)',
    language: 'en',
    type: 'srt',
    kind: 'subtitles',
  },
  {
    id: 'subs/spanish.vtt',
    src: 'http://localhost/api/v1/stream/hash?path=subs/spanish.vtt',
    label: 'Spanish (VTT)',
    language: 'es',
    type: 'vtt',
    kind: 'subtitles',
  },
];

describe('SubtitleTrackSelector', () => {
  it('renders nothing when track list is empty', () => {
    const { container } = render(
      <SubtitleTrackSelector tracks={[]}
        selectedTrackId={null}
        onSelectTrack={vi.fn()} />
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('renders subtitle track button and toggles menu open and closed', async () => {
    render(
      <SubtitleTrackSelector
        tracks={mockTracks}
        selectedTrackId={null}
        onSelectTrack={vi.fn()}
      />
    );

    const button = screen.getByRole('button', { name: /select subtitle track/i });
    expect(button).toBeInTheDocument();

    // Initially menu is not open
    expect(screen.queryByText(/Subtitles \(2\)/i)).not.toBeInTheDocument();

    // Click to open
    await userEvent.click(button);
    expect(screen.getByText(/Subtitles \(2\)/i)).toBeInTheDocument();

    // Click button again to close
    await userEvent.click(button);
    expect(screen.queryByText(/Subtitles \(2\)/i)).not.toBeInTheDocument();
  });

  it('closes menu when backdrop overlay is clicked', async () => {
    const { container } = render(
      <SubtitleTrackSelector
        tracks={mockTracks}
        selectedTrackId={null}
        onSelectTrack={vi.fn()}
      />
    );

    const button = screen.getByRole('button', { name: /select subtitle track/i });
    await userEvent.click(button);
    expect(screen.getByText(/Subtitles \(2\)/i)).toBeInTheDocument();

    const backdrop = container.querySelector('.fixed.inset-0');
    expect(backdrop).toBeInTheDocument();

    if (backdrop) {
      await userEvent.click(backdrop);
    }
    expect(screen.queryByText(/Subtitles \(2\)/i)).not.toBeInTheDocument();
  });

  it('closes menu when Escape key is pressed', async () => {
    render(
      <SubtitleTrackSelector
        tracks={mockTracks}
        selectedTrackId={null}
        onSelectTrack={vi.fn()}
      />
    );

    const button = screen.getByRole('button', { name: /select subtitle track/i });
    await userEvent.click(button);
    expect(screen.getByText(/Subtitles \(2\)/i)).toBeInTheDocument();

    await userEvent.keyboard('{Escape}');
    expect(screen.queryByText(/Subtitles \(2\)/i)).not.toBeInTheDocument();
  });

  it('sets proper ARIA attributes when closed and open', async () => {
    render(
      <SubtitleTrackSelector
        tracks={mockTracks}
        selectedTrackId='subs/english.srt'
        onSelectTrack={vi.fn()}
      />
    );

    const button = screen.getByRole('button', { name: /select subtitle track/i });
    expect(button).toHaveAttribute('aria-haspopup', 'menu');
    expect(button).toHaveAttribute('aria-expanded', 'false');

    await userEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('menu', { name: /subtitles/i })).toBeInTheDocument();

    const menuItems = screen.getAllByRole('menuitemradio');
    // Off + 2 tracks = 3 items
    expect(menuItems).toHaveLength(3);
    expect(menuItems[0]).toHaveAttribute('aria-checked', 'false'); // Off
    expect(menuItems[1]).toHaveAttribute('aria-checked', 'true'); // English
    expect(menuItems[2]).toHaveAttribute('aria-checked', 'false'); // Spanish
  });

  it('calls onSelectTrack with track id when an option is selected', async () => {
    const onSelectTrack = vi.fn();
    render(
      <SubtitleTrackSelector
        tracks={mockTracks}
        selectedTrackId={null}
        onSelectTrack={onSelectTrack}
      />
    );

    await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));

    const englishBtn = screen.getByText('English (SRT)');
    await userEvent.click(englishBtn);

    expect(onSelectTrack).toHaveBeenCalledWith('subs/english.srt');
    expect(screen.queryByText(/Subtitles \(2\)/i)).not.toBeInTheDocument();
  });

  it('calls onSelectTrack with null when Off option is selected', async () => {
    const onSelectTrack = vi.fn();
    render(
      <SubtitleTrackSelector
        tracks={mockTracks}
        selectedTrackId='subs/english.srt'
        onSelectTrack={onSelectTrack}
      />
    );

    await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));

    const offBtn = screen.getByRole('menuitemradio', { name: /off/i });
    await userEvent.click(offBtn);

    expect(onSelectTrack).toHaveBeenCalledWith(null);
    expect(screen.queryByText(/Subtitles \(2\)/i)).not.toBeInTheDocument();
  });
});
