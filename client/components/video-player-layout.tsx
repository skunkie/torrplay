// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { type PlayerSrc } from '@vidstack/react';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { type PreloadBadgeInfo, type SubtitleTrackInfo } from '@/lib/video-utils';

import DemoVideoPlayer from './demo-video-player';
import VideoPlayer from './video-player';

interface VideoPlayerLayoutProps {
  initialPlaybackPositionSeconds?: number,
  isDemo?: boolean,
  onExit?: () => void,
  onOpenChange: (open: boolean) => void,
  onPlaybackPositionChange?: (positionSeconds: number) => void,
  open: boolean,
  options: {
    autoPlay?: boolean,
    src: PlayerSrc,
    title?: string,
    tracks?: SubtitleTrackInfo[]
  },
  playlistNavigation?: {
    onNext?: () => void,
    onPrevious?: () => void
  },
  preloadBadge?: PreloadBadgeInfo | null
}

export const VideoPlayerLayout = ({
  open,
  onOpenChange,
  options,
  onExit,
  initialPlaybackPositionSeconds,
  onPlaybackPositionChange,
  playlistNavigation,
  preloadBadge,
  isDemo = false,
}: VideoPlayerLayoutProps) => {
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
    >
      <DialogContent
        variant='video'
        showCloseButton={false}
        onPointerDownOutside={e => e.preventDefault()}
      >
        <DialogTitle className='sr-only'>{options.title ?? ''}</DialogTitle>
        <DialogDescription className='sr-only'>Video player for {options.title ?? 'video'}</DialogDescription>
        {open && (
          isDemo ? (
            <DemoVideoPlayer
              options={options}
              onExit={onExit}
              initialPlaybackPositionSeconds={initialPlaybackPositionSeconds}
              onPlaybackPositionChange={onPlaybackPositionChange}
              playlistNavigation={playlistNavigation}
              preloadBadge={preloadBadge}
            />
          ) : (
            <VideoPlayer
              initialPlaybackPositionSeconds={initialPlaybackPositionSeconds}
              onPlaybackPositionChange={onPlaybackPositionChange}
              options={options}
              onExit={onExit}
              playlistNavigation={playlistNavigation}
              preloadBadge={preloadBadge}
            />
          )
        )}
      </DialogContent>
    </Dialog>
  );
};
