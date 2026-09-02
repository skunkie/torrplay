// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { type VideoSrc } from '@vidstack/react';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { type SubtitleTrackInfo } from '@/lib/video-utils';

import DemoVideoPlayer from './demo-video-player';
import VideoPlayer from './video-player';

interface VideoPlayerLayoutProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  options: {
    src: VideoSrc,
    title?: string,
    autoPlay?: boolean,
    tracks?: SubtitleTrackInfo[]
  },
  onExit?: () => void,
  playlistNavigation?: {
    onPrevious?: () => void,
    onNext?: () => void
  },
  isDemo?: boolean
}

export const VideoPlayerLayout = ({
  open,
  onOpenChange,
  options,
  onExit,
  playlistNavigation,
  isDemo = false,
}: VideoPlayerLayoutProps) => {
  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent variant='video'
        showCloseButton={false}>
        <DialogTitle className='sr-only'>{options.title ?? ''}</DialogTitle>
        <DialogDescription className='sr-only'>Video player for {options.title ?? 'video'}</DialogDescription>
        {open && (
          isDemo ? (
            <DemoVideoPlayer options={options}
              onExit={onExit}
              playlistNavigation={playlistNavigation} />
          ) : (
            <VideoPlayer options={options}
              onExit={onExit}
              playlistNavigation={playlistNavigation} />
          )
        )}
      </DialogContent>
    </Dialog>
  );
};
