// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { type VideoMimeType } from '@vidstack/react';
import React from 'react';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';

import VideoPlayer from './video-player';

interface VideoPlayerDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  options: {
    src: {
      src: string,
      type: VideoMimeType
    },
    title: string,
    autoPlay: boolean
  },
  onExit: () => void
}

export const VideoPlayerDialog = ({ open, onOpenChange, options, onExit }: VideoPlayerDialogProps) => {
  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent variant='video'
        showCloseButton={false}>
        <DialogTitle className='sr-only'>{options.title}</DialogTitle>
        <DialogDescription className='sr-only'>Video player for {options.title}</DialogDescription>
        {open && <VideoPlayer options={options}
          onExit={onExit} />}
      </DialogContent>
    </Dialog>
  );
};
