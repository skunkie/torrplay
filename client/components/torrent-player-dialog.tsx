// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useCallback, useMemo, useRef, useState } from 'react';

import { getTorrentStreamUrl } from '@/lib/api/torrents';
import { type Torrent, type TorrentFile } from '@/lib/types/api';
import { getInitialVideoFile, getSubtitleTracksForVideo, getVideoFiles, getVideoType } from '@/lib/video-utils';

import { TorrentPlayerDialogLayout } from './torrent-player-dialog-layout';

interface TorrentPlayerDialogProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void
}

function computeVideoFiles(torrent: Torrent | null): { videoFiles: TorrentFile[], selectedFile: TorrentFile | null } {
  if (!torrent) return { videoFiles: [], selectedFile: null };
  const files = getVideoFiles(torrent.files);
  return { videoFiles: files, selectedFile: getInitialVideoFile(files) };
}

export const TorrentPlayerDialog = ({ torrent, open, onOpenChange }: TorrentPlayerDialogProps) => {
  const [userSelectedFile, setUserSelectedFile] = useState<TorrentFile | null>(null);
  const prevOpenRef = useRef(open);

  if (open && !prevOpenRef.current) {
    setUserSelectedFile(null);
  }
  prevOpenRef.current = open;

  const computed = computeVideoFiles(torrent);

  const videoFiles = open ? computed.videoFiles : [];
  const selectedFile = open ? (userSelectedFile ?? computed.selectedFile) : null;

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
      const subtitleTracks = getSubtitleTracksForVideo(selectedFile, torrent.files, torrent.hash);
      return {
        src: {
          src: getTorrentStreamUrl(torrent.hash, selectedFile.path),
          type: getVideoType(selectedFile.name),
        },
        title: selectedFile.name,
        autoPlay: true,
        tracks: subtitleTracks,
      };
    }
    return null;
  }, [selectedFile, torrent]);

  const isPlayerVisible = !!videoPlayerOptions;
  const selectedFileIndex = selectedFile
    ? videoFiles.findIndex(file => file.path === selectedFile.path)
    : -1;
  const playlistNavigation = videoFiles.length > 1 && selectedFileIndex >= 0
    ? {
      onPrevious: selectedFileIndex > 0
        ? () => setUserSelectedFile(videoFiles[selectedFileIndex - 1])
        : undefined,
      onNext: selectedFileIndex < videoFiles.length - 1
        ? () => setUserSelectedFile(videoFiles[selectedFileIndex + 1])
        : undefined,
    }
    : undefined;

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
      playlistNavigation={playlistNavigation}
    />
  );
};

export default TorrentPlayerDialog;
