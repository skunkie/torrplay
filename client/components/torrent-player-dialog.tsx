// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { cancelPreload, getPreload, getTorrentStreamUrl, startPreload } from '@/lib/api/torrents';
import { type Torrent, type TorrentFile } from '@/lib/types/api';
import { getInitialVideoFile, getSubtitleTracksForVideo, getVideoFiles, getVideoType } from '@/lib/video-utils';

import { TorrentPlayerDialogLayout } from './torrent-player-dialog-layout';

interface TorrentPlayerDialogProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void,
  enablePreload?: boolean
}

function computeVideoFiles(torrent: Torrent | null): { videoFiles: TorrentFile[], selectedFile: TorrentFile | null } {
  if (!torrent) return { videoFiles: [], selectedFile: null };
  const files = getVideoFiles(torrent.files);
  return { videoFiles: files, selectedFile: getInitialVideoFile(files) };
}

export const TorrentPlayerDialog = ({
  torrent,
  open,
  onOpenChange,
  enablePreload = false,
}: TorrentPlayerDialogProps) => {
  const [userSelectedFile, setUserSelectedFile] = useState<TorrentFile | null>(null);
  const [isPreloading, setIsPreloading] = useState(false);
  const [preloadProgress, setPreloadProgress] = useState(0);
  const [completedBytes, setCompletedBytes] = useState(0);
  const [targetBytes, setTargetBytes] = useState(0);

  const prevOpenRef = useRef(open);
  const preloadedFileRef = useRef<string | null>(null);
  const activePreloadHashRef = useRef<string | null>(null);
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const timeoutTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const stopPreloadPolling = useCallback(() => {
    if (pollTimerRef.current) {
      clearInterval(pollTimerRef.current);
      pollTimerRef.current = null;
    }
    if (timeoutTimerRef.current) {
      clearTimeout(timeoutTimerRef.current);
      timeoutTimerRef.current = null;
    }
  }, []);

  const cancelActivePreload = useCallback(() => {
    stopPreloadPolling();
    const hash = activePreloadHashRef.current;
    activePreloadHashRef.current = null;
    if (hash) {
      cancelPreload(hash).catch(() => {});
    }
  }, [stopPreloadPolling]);

  if (open && !prevOpenRef.current) {
    setUserSelectedFile(null);
    preloadedFileRef.current = null;
    setIsPreloading(false);
    setPreloadProgress(0);
    setCompletedBytes(0);
    setTargetBytes(0);
  }
  prevOpenRef.current = open;

  const computed = computeVideoFiles(torrent);

  const videoFiles = open ? computed.videoFiles : [];
  const selectedFile = open ? (userSelectedFile ?? computed.selectedFile) : null;

  const handleExit = useCallback(() => {
    cancelActivePreload();
    setIsPreloading(false);
    if (videoFiles.length > 1) {
      setUserSelectedFile(null);
      preloadedFileRef.current = null;
    } else {
      onOpenChange(false);
      setUserSelectedFile(null);
      preloadedFileRef.current = null;
    }
  }, [cancelActivePreload, onOpenChange, videoFiles.length]);

  // Implicit preload trigger when a playable file is selected
  useEffect(() => {
    if (!open || !torrent || !selectedFile || !enablePreload) {
      cancelActivePreload();
      return;
    }

    if (preloadedFileRef.current === selectedFile.path) {
      return;
    }

    let isMounted = true;

    if (activePreloadHashRef.current && activePreloadHashRef.current !== torrent.hash) {
      cancelPreload(activePreloadHashRef.current).catch(() => {});
    }
    activePreloadHashRef.current = torrent.hash;

    setIsPreloading(true);
    setPreloadProgress(0);
    setCompletedBytes(0);
    setTargetBytes(0);

    startPreload(torrent.hash, { filePath: selectedFile.path })
      .then(resp => {
        if (!isMounted) return;
        setPreloadProgress(current => Math.max(current, resp.progress || 0));
        setCompletedBytes(current => Math.max(current, resp.completedBytes || 0));
        setTargetBytes(current => Math.max(current, resp.targetBytes || 0));

        if (resp.status === 'idle') {
          cancelActivePreload();
          preloadedFileRef.current = selectedFile.path;
          setIsPreloading(false);
          return;
        }

        if (resp.status === 'ready' || (resp.progress && resp.progress >= 1)) {
          setPreloadProgress(1.0);
          setCompletedBytes(resp.targetBytes || resp.completedBytes || 0);
          setTargetBytes(resp.targetBytes || 0);
          setTimeout(() => {
            if (!isMounted) return;
            preloadedFileRef.current = selectedFile.path;
            setIsPreloading(false);
          }, 300);
          return;
        }

        // Start polling for preload progress
        pollTimerRef.current = setInterval(async () => {
          try {
            const statusResp = await getPreload(torrent.hash);
            if (!isMounted) return;
            const progress = statusResp.progress || 0;
            setPreloadProgress(current => Math.max(current, progress));
            setCompletedBytes(current => Math.max(current, statusResp.completedBytes || 0));
            setTargetBytes(current => Math.max(current, statusResp.targetBytes || 0));

            if (statusResp.status === 'idle') {
              cancelActivePreload();
              preloadedFileRef.current = selectedFile.path;
              setIsPreloading(false);
              return;
            }

            if (statusResp.status === 'ready' || progress >= 1) {
              stopPreloadPolling();
              setPreloadProgress(1.0);
              setTimeout(() => {
                if (!isMounted) return;
                preloadedFileRef.current = selectedFile.path;
                setIsPreloading(false);
              }, 300);
            }
          } catch {
            // Polling error: stop preloading indicator
            cancelActivePreload();
            preloadedFileRef.current = selectedFile.path;
            setIsPreloading(false);
          }
        }, 400);

        // Stop badge after timeout if still active
        timeoutTimerRef.current = setTimeout(() => {
          if (!isMounted) return;
          cancelActivePreload();
          preloadedFileRef.current = selectedFile.path;
          setIsPreloading(false);
        }, 15000);
      })
      .catch(() => {
        if (!isMounted) return;
        // Network/API error: stop preloading indicator
        cancelActivePreload();
        preloadedFileRef.current = selectedFile.path;
        setIsPreloading(false);
      });

    return () => {
      isMounted = false;
      stopPreloadPolling();
    };
  }, [cancelActivePreload, open, torrent, selectedFile, enablePreload, stopPreloadPolling]);

  // Clean up when dialog closes
  useEffect(() => {
    if (!open) {
      cancelActivePreload();
      setIsPreloading(false);
    }
  }, [cancelActivePreload, open]);

  useEffect(() => () => cancelActivePreload(), [cancelActivePreload]);

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
        ? () => {
          cancelActivePreload();
          preloadedFileRef.current = null;
          setUserSelectedFile(videoFiles[selectedFileIndex - 1]);
        }
        : undefined,
      onNext: selectedFileIndex < videoFiles.length - 1
        ? () => {
          cancelActivePreload();
          preloadedFileRef.current = null;
          setUserSelectedFile(videoFiles[selectedFileIndex + 1]);
        }
        : undefined,
    }
    : undefined;

  const preloadBadge = isPreloading
    ? {
      progress: preloadProgress,
      completedBytes,
      targetBytes,
    }
    : null;

  return (
    <TorrentPlayerDialogLayout
      open={open}
      onOpenChange={onOpenChange}
      videoFiles={videoFiles}
      selectedFile={selectedFile}
      setSelectedFile={file => {
        cancelActivePreload();
        preloadedFileRef.current = null;
        setUserSelectedFile(file);
      }}
      isPlayerVisible={isPlayerVisible}
      videoPlayerOptions={videoPlayerOptions}
      handleExit={handleExit}
      playlistNavigation={playlistNavigation}
      preloadBadge={preloadBadge}
    />
  );
};

export default TorrentPlayerDialog;
