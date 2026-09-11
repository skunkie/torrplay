// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, renderHook } from '@testing-library/react';
import type { MediaPlayerInstance } from '@vidstack/react';
import { describe, expect, it } from 'vitest';

import { type SubtitleTrackInfo } from '@/lib/video-utils';

import { useSubtitleTracks } from '../use-subtitle-tracks';

function makePlayerRef() {
  return { current: null } as React.RefObject<MediaPlayerInstance | null>;
}

const tracks: SubtitleTrackInfo[] = [
  { id: 'sub-1', src: 'sub1.vtt', label: 'English' },
  { id: 'sub-2', src: 'sub2.vtt', label: 'Spanish' },
];

describe('useSubtitleTracks', () => {
  it('ignores selectTrack calls while disabled, so the UI never shows a selection that was never applied', () => {
    const player = makePlayerRef();

    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) => useSubtitleTracks({ player, sourceKey: 'video.mp4', tracks, enabled }),
      { initialProps: { enabled: false } },
    );

    expect(result.current.selectedTrackId).toBeNull();

    // A click on the (still visible) subtitle selector, or the 'c' keyboard shortcut,
    // can reach selectTrack while the hook - and the player it depends on - is disabled
    // (e.g. during preloading, before any TextTrack has been registered).
    act(() => {
      result.current.selectTrack('sub-2');
    });

    expect(result.current.selectedTrackId).toBeNull();

    // Once actually enabled, the same call is honored normally.
    rerender({ enabled: true });

    act(() => {
      result.current.selectTrack('sub-2');
    });

    expect(result.current.selectedTrackId).toBe('sub-2');
  });
});
