// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useCallback, useMemo, useRef, useState } from 'react';

import { TorrentPlayerDialogLayout } from '@/components/torrent-player-dialog-layout';
import type { Torrent, TorrentFile } from '@/lib/types/api';
import { getInitialVideoFile, getVideoFiles, getVideoType } from '@/lib/video-utils';

interface DemoTorrentPlayerDialogProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void
}

export function DemoTorrentPlayerDialog({ torrent, open, onOpenChange }: DemoTorrentPlayerDialogProps) {
  const [userSelectedFile, setUserSelectedFile] = useState<TorrentFile | null>(null);
  const prevOpenRef = useRef(open);

  if (open && !prevOpenRef.current) {
    setUserSelectedFile(null);
  }
  prevOpenRef.current = open;

  const videoFiles = open && torrent ? getVideoFiles(torrent.files) : [];
  const initialFile = open && torrent ? getInitialVideoFile(videoFiles) : null;
  const selectedFile = userSelectedFile ?? initialFile;

  const handleExit = useCallback(() => {
    if (videoFiles.length > 1) {
      setUserSelectedFile(null);
    } else {
      onOpenChange(false);
      setUserSelectedFile(null);
    }
  }, [onOpenChange, videoFiles.length]);

  const videoPlayerOptions = useMemo(() => {
    if (selectedFile && torrent) {
      return {
        src: {
          src: 'data:video/mp4;base64,AAAAIGZ0eXBpcG1wAAACAG1pcHJwAAAAUGlwcm9wAAAA7XfSZXZ0AAAAiXVzZSAKbW92dm0gd2lkZQAAAAAAANB3bWhkAAAAAAAAAAAAAAAAAAAAAA==',
          type: getVideoType(selectedFile.name),
        },
        title: selectedFile.name,
        autoPlay: true,
      };
    }
    return null;
  }, [selectedFile, torrent]);

  const isPlayerVisible = !!videoPlayerOptions;

  return (
    <TorrentPlayerDialogLayout
      open={open}
      onOpenChange={onOpenChange}
      videoFiles={videoFiles}
      selectedFile={selectedFile}
      setSelectedFile={setUserSelectedFile}
      isPlayerVisible={isPlayerVisible}
      videoPlayerOptions={videoPlayerOptions}
      handleExit={handleExit}
      isDemo={true}
    />
  );
}
