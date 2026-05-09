// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useEffect, useState } from 'react';

import DemoVideoPlayer from '@/components/demo-video-player';
import type { Torrent, TorrentFile } from '@/lib/types/api';
import { getInitialVideoFile, getVideoType } from '@/lib/video-utils';

const videoExtensions = [
  '.mp4',
  '.mkv',
  '.avi',
  '.mov',
  '.wmv',
  '.webm',
  '.flv',
  '.m4v',
];

interface DemoTorrentPlayerDialogProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void
}

export function DemoTorrentPlayerDialog({ torrent, open, onOpenChange }: DemoTorrentPlayerDialogProps) {
  const [selectedFile, setSelectedFile] = useState<TorrentFile | null>(null);

  useEffect(() => {
    if (open && torrent) {
      const files = torrent.files.filter(f => videoExtensions.some(ext => f.name.toLowerCase().endsWith(ext)));
      setSelectedFile(getInitialVideoFile(files));
    } else {
      setSelectedFile(null);
    }
  }, [open, torrent]);

  const handleExit = () => {
    onOpenChange(false);
    setSelectedFile(null);
  };

  const videoPlayerOptions = selectedFile && torrent
    ? {
      src: {
        src: 'data:video/mp4;base64,AAAAIGZ0eXBpcG1wAAACAG1pcHJwAAAAUGlwcm9wAAAA7XfSZXZ0AAAAiXVzZSAKbW92dm0gd2lkZQAAAAAAANB3bWhkAAAAAAAAAAAAAAAAAAAAAA==',
        type: getVideoType(selectedFile.name),
      },
      title: selectedFile.name,
      autoPlay: true,
    }
    : null;

  const isPlayerVisible = !!videoPlayerOptions;

  if (!open || !torrent) return null;

  if (!isPlayerVisible) return null;

  return (
    <div className='fixed inset-0 z-50 flex items-center justify-center bg-black/90 p-4'>
      <div className='relative w-full max-w-4xl aspect-video'>
        <DemoVideoPlayer
          options={videoPlayerOptions}
          onExit={handleExit}
        />
      </div>
    </div>
  );
}
