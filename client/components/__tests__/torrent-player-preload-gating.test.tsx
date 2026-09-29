// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';

import { TorrentPlayerDialog } from '@/components/torrent-player-dialog';
import type { VideoPlayerProps } from '@/components/video-player';
import * as torrentsApi from '@/lib/api/torrents';
import { savePlaybackPositionSeconds } from '@/lib/playback-position';
import type { PreloadRequest, PreloadResponse, Torrent } from '@/lib/types/api';

const observedPreloadBadges = vi.hoisted(() => [] as VideoPlayerProps['preloadBadge'][]);
const observedPositionKeys = vi.hoisted(() => [] as VideoPlayerProps['positionKey'][]);
const { startPreloadMock } = vi.hoisted(() => ({
  startPreloadMock: vi.fn<(hash: string, request: PreloadRequest) => Promise<never>>(() => new Promise(() => {})),
}));

vi.mock('@/components/video-player', () => ({
  default: (props: VideoPlayerProps) => {
    observedPreloadBadges.push(props.preloadBadge);
    observedPositionKeys.push(props.positionKey);
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

it('preloads from the saved playback position and resumes the player there', () => {
  localStorage.clear();
  savePlaybackPositionSeconds('1234567890', 'movie.mp4', 900);
  observedPositionKeys.length = 0;
  startPreloadMock.mockClear();

  render(
    <TorrentPlayerDialog
      torrent={makeTorrent()}
      open={true}
      onOpenChange={vi.fn()}
      enablePreload={true}
    />,
  );

  expect(startPreloadMock).toHaveBeenCalledWith('1234567890', { filePath: 'movie.mp4', playbackPositionSeconds: 900 });
  expect(observedPositionKeys[observedPositionKeys.length - 1]).toEqual({ hash: '1234567890', filePath: 'movie.mp4' });
  localStorage.clear();
});

it('preloads the head and tail only without a saved playback position', () => {
  localStorage.clear();
  startPreloadMock.mockClear();

  render(
    <TorrentPlayerDialog
      torrent={makeTorrent()}
      open={true}
      onOpenChange={vi.fn()}
      enablePreload={true}
    />,
  );

  expect(startPreloadMock).toHaveBeenCalledWith('1234567890', { filePath: 'movie.mp4' });
});

it('shows the latest preload target when the server shrinks it', async () => {
  // A resume window that cannot be placed leaves the head and tail only.
  const status = (targetBytes: number): PreloadResponse => ({
    fileIndex: 0, targetBytes, completedBytes: 100, progress: 100 / targetBytes,
    status: 'preloading', activePeers: 1, downloadRate: 10, totalPeers: 1,
  });
  startPreloadMock.mockClear();
  startPreloadMock.mockResolvedValueOnce(status(1000) as never);
  const getPreloadSpy = vi.spyOn(torrentsApi, 'getPreload').mockResolvedValue(status(500));
  observedPreloadBadges.length = 0;

  render(
    <TorrentPlayerDialog
      torrent={makeTorrent()}
      open={true}
      onOpenChange={vi.fn()}
      enablePreload={true}
    />,
  );

  await waitFor(() => expect(observedPreloadBadges[observedPreloadBadges.length - 1]?.targetBytes).toBe(500));
  getPreloadSpy.mockRestore();
});
