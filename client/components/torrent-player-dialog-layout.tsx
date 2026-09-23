// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { type PlayerSrc } from '@vidstack/react';
import React from 'react';

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { type TorrentFile } from '@/lib/types/api';
import { type PreloadBadgeInfo, type SubtitleTrackInfo } from '@/lib/video-utils';

import { Button } from './ui/button';
import { VideoPlayerLayout } from './video-player-layout';

interface TorrentPlayerDialogLayoutProps {
  handleExit?: () => void,
  initialPlaybackPositionSeconds?: number,
  isDemo?: boolean,
  isPlayerVisible: boolean,
  onOpenChange: (open: boolean) => void,
  onPlaybackPositionChange?: (positionSeconds: number) => void,
  open: boolean,
  playlistNavigation?: {
    onNext?: () => void,
    onPrevious?: () => void
  },
  preloadBadge?: PreloadBadgeInfo | null,
  setSelectedFile: (file: TorrentFile) => void,
  videoFiles: TorrentFile[],
  videoPlayerOptions: {
    autoPlay?: boolean,
    src: PlayerSrc,
    title?: string,
    tracks?: SubtitleTrackInfo[]
  } | null
}

export const TorrentPlayerDialogLayout = ({
  open,
  onOpenChange,
  videoFiles,
  setSelectedFile,
  isPlayerVisible,
  videoPlayerOptions,
  handleExit,
  initialPlaybackPositionSeconds,
  onPlaybackPositionChange,
  playlistNavigation,
  preloadBadge,
  isDemo = false,
}: TorrentPlayerDialogLayoutProps) => {
  if (isPlayerVisible && videoPlayerOptions) {
    return (
      <VideoPlayerLayout
        open={open}
        onOpenChange={shouldOpen => {
          if (!shouldOpen && handleExit) {
            handleExit();
          } else {
            onOpenChange(shouldOpen);
          }
        }}
        options={videoPlayerOptions}
        onExit={handleExit}
        initialPlaybackPositionSeconds={initialPlaybackPositionSeconds}
        onPlaybackPositionChange={onPlaybackPositionChange}
        playlistNavigation={playlistNavigation}
        preloadBadge={preloadBadge}
        isDemo={isDemo}
      />
    );
  }

  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogTitle className='sr-only'>Torrent Player</DialogTitle>
        <DialogDescription className='sr-only'>Media player for torrent videos</DialogDescription>
        {videoFiles.length === 0 ? (
          <DialogHeader>
            <DialogTitle>No Playable Files</DialogTitle>
            <DialogDescription>No playable video files were found in this torrent.</DialogDescription>
          </DialogHeader>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>Select a video to play</DialogTitle>
              <DialogDescription>Choose a video file from the torrent to play</DialogDescription>
            </DialogHeader>
            <div className='flex flex-col gap-2 max-h-[60vh] overflow-y-auto py-4'>
              {videoFiles.map(file => (
                <Button
                  key={file.path}
                  onClick={() => setSelectedFile(file)}
                  variant='outline'
                  className='whitespace-normal h-auto text-left break-all'
                >
                  {file.name}
                </Button>
              ))}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
};
