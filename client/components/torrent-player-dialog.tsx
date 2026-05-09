// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useCallback, useEffect, useMemo, useState } from 'react';

import { getTorrentStreamUrl } from '@/lib/api/torrents';
import { type Torrent, type TorrentFile } from '@/lib/types/api';
import { getInitialVideoFile, getVideoFiles, getVideoType } from '@/lib/video-utils';

import { TorrentPlayerDialogLayout } from './torrent-player-dialog-layout';

interface TorrentPlayerDialogProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void
}

export const TorrentPlayerDialog = ({ torrent, open, onOpenChange }: TorrentPlayerDialogProps) => {
  const [videoFiles, setVideoFiles] = useState<TorrentFile[]>([]);
  const [selectedFile, setSelectedFile] = useState<TorrentFile | null>(null);

  useEffect(() => {
    if (open && torrent) {
      const files = getVideoFiles(torrent.files);
      setVideoFiles(files);
      setSelectedFile(getInitialVideoFile(files));
    } else {
      setVideoFiles([]);
      setSelectedFile(null);
    }
  }, [open, torrent]);

  const handleExit = useCallback(() => {
    if (videoFiles.length > 1) {
      setSelectedFile(null);
    } else {
      onOpenChange(false);
      setSelectedFile(null);
    }
  }, [onOpenChange, videoFiles.length]);

  const videoPlayerOptions = useMemo(() => {
    if (selectedFile && torrent) {
      return {
        src: {
          src: getTorrentStreamUrl(torrent.hash, selectedFile.path),
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
      setSelectedFile={setSelectedFile}
      isPlayerVisible={isPlayerVisible}
      videoPlayerOptions={videoPlayerOptions}
      handleExit={handleExit}
    />
  );
};

export default TorrentPlayerDialog;
