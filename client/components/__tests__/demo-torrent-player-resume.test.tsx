// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { expect, it, vi } from 'vitest';

import { DemoTorrentPlayerDialog } from '@/app/demo/demo-torrent-player-dialog';
import type { TorrentPlayerDialogLayout } from '@/components/torrent-player-dialog-layout';
import type { Torrent } from '@/lib/types/api';

type LayoutProps = ComponentProps<typeof TorrentPlayerDialogLayout>;

const observedResumeKeys = vi.hoisted(() => [] as LayoutProps['resumeKey'][]);

vi.mock('@/components/torrent-player-dialog-layout', () => ({
  TorrentPlayerDialogLayout: (props: LayoutProps) => {
    observedResumeKeys.push(props.resumeKey);
    return null;
  },
}));

const torrent: Torrent = {
  hash: 'demosingle123',
  title: 'Demo Single File Torrent',
  name: 'demo-single',
  magnet: 'magnet:?xt=urn:btih:demosingle123',
  files: [{ name: 'single_video.mp4', path: '/single_video.mp4', length: 1000 }],
  storage: 'file',
  pieceCount: 1,
  pieceSize: 1,
  totalSize: 1000,
};

it('resumes demo playback from the saved position of the selected file', () => {
  render(<DemoTorrentPlayerDialog torrent={torrent}
    open={true}
    onOpenChange={vi.fn()} />);

  expect(observedResumeKeys[observedResumeKeys.length - 1]).toEqual({ hash: 'demosingle123', filePath: '/single_video.mp4' });
});
