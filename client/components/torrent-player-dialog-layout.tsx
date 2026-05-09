// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { type VideoSrc } from '@vidstack/react';
import React from 'react';

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { type TorrentFile } from '@/lib/types/api';

import { Button } from './ui/button';
import { VideoPlayerLayout } from './video-player-layout';

interface TorrentPlayerDialogLayoutProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  videoFiles: TorrentFile[],
  selectedFile: TorrentFile | null,
  setSelectedFile: (file: TorrentFile) => void,
  isPlayerVisible: boolean,
  videoPlayerOptions: {
    src: VideoSrc,
    title?: string,
    autoPlay?: boolean
  } | null,
  handleExit?: () => void,
  isDemo?: boolean
}

export const TorrentPlayerDialogLayout = ({
  open,
  onOpenChange,
  videoFiles,
  setSelectedFile,
  isPlayerVisible,
  videoPlayerOptions,
  handleExit,
  isDemo = false,
}: TorrentPlayerDialogLayoutProps) => {
  const renderContent = () => {
    if (!isPlayerVisible) {
      if (videoFiles.length === 0 && open) {
        return (
          <DialogHeader>
            <DialogTitle>No Playable Files</DialogTitle>
            <DialogDescription>No playable video files were found in this torrent.</DialogDescription>
          </DialogHeader>
        );
      }

      if (videoFiles.length > 0) {
        return (
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
        );
      }
    }

    return null;
  };

  return (
    <>
      <Dialog open={open && !isPlayerVisible}
        onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogTitle className='sr-only'>Torrent Player</DialogTitle>
          <DialogDescription className='sr-only'>Media player for torrent videos</DialogDescription>
          {renderContent()}
        </DialogContent>
      </Dialog>
      {isPlayerVisible && videoPlayerOptions && (
        <VideoPlayerLayout
          open={open}
          onOpenChange={shouldOpen => {
            if (!shouldOpen && handleExit) {
              handleExit();
            }
          }}
          options={videoPlayerOptions}
          onExit={handleExit}
          isDemo={isDemo}
        />
      )}
    </>
  );
};
