// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';

import { TorrentPlayerDialog } from '@/components/torrent-player-dialog';
import type { VideoPlayerProps } from '@/components/video-player';
import { getPlaybackPositionSeconds, savePlaybackPositionSeconds } from '@/lib/playback-position';
import type { Torrent } from '@/lib/types/api';

const observedPreloadBadges = vi.hoisted(() => [] as VideoPlayerProps['preloadBadge'][]);
const observedVideoPlayerProps = vi.hoisted(() => [] as VideoPlayerProps[]);
const { startPreloadMock } = vi.hoisted(() => ({ startPreloadMock: vi.fn(() => new Promise(() => {})) }));

vi.mock('@/components/video-player', () => ({
  default: (props: VideoPlayerProps) => {
    observedPreloadBadges.push(props.preloadBadge);
    observedVideoPlayerProps.push(props);
    return <div data-testid='mock-video-player' />;
  },
}));

vi.mock('@/lib/api/torrents', async importOriginal => {
  const actual = await importOriginal<typeof import('@/lib/api/torrents')>();
  return {
    ...actual,
    startPreload: startPreloadMock,
  };
});

function makeTorrent(): Torrent {
  return {
    hash: '1234567890',
    name: 'movie',
    title: 'Movie',
    magnet: 'magnet:?xt=urn:btih:1234567890',
    files: [{ name: 'movie.mp4', path: 'movie.mp4', length: 1024 }],
    storage: 'file',
    pieceCount: 1,
    pieceSize: 1024,
    totalSize: 1024,
  };
}

beforeEach(() => {
  localStorage.clear();
  observedVideoPlayerProps.length = 0;
});

it('blocks the media source on the initial render while preload is pending', () => {
  observedPreloadBadges.length = 0;
  startPreloadMock.mockClear();

  render(
    <TorrentPlayerDialog
      torrent={makeTorrent()}
      open={true}
      onOpenChange={vi.fn()}
      enablePreload={true}
    />,
  );

  expect(observedPreloadBadges[0]).toEqual({
    progress: 0,
    completedBytes: 0,
    targetBytes: 0,
    downloadRate: 0,
    activePeers: 0,
    totalPeers: 0,
  });
});

it('does not restart an in-flight preload when the torrent prop is replaced by a referentially new but logically identical object', () => {
  // Mirrors an upstream poll refresh (e.g. SWR revalidation) that hands down a freshly
  // parsed torrent/file object graph with the same hash/path but new object identities.
  startPreloadMock.mockClear();

  const { rerender } = render(
    <TorrentPlayerDialog
      torrent={makeTorrent()}
      open={true}
      onOpenChange={vi.fn()}
      enablePreload={true}
    />,
  );

  expect(startPreloadMock).toHaveBeenCalledTimes(1);

  rerender(
    <TorrentPlayerDialog
      torrent={makeTorrent()}
      open={true}
      onOpenChange={vi.fn()}
      enablePreload={true}
    />,
  );

  expect(startPreloadMock).toHaveBeenCalledTimes(1);
});

it('passes the locally stored playback position to preload', () => {
  localStorage.clear();
  startPreloadMock.mockClear();
  savePlaybackPositionSeconds('1234567890', 'movie.mp4', 321);

  render(
    <TorrentPlayerDialog
      torrent={makeTorrent()}
      open={true}
      onOpenChange={vi.fn()}
      enablePreload={true}
    />,
  );

  expect(startPreloadMock).toHaveBeenCalledWith('1234567890', {
    filePath: 'movie.mp4',
    playbackPositionSeconds: 321,
  });
  expect(observedVideoPlayerProps[0].initialPlaybackPositionSeconds).toBe(321);

  observedVideoPlayerProps[0].onPlaybackPositionChange?.(654);
  expect(getPlaybackPositionSeconds('1234567890', 'movie.mp4')).toBe(654);
});
