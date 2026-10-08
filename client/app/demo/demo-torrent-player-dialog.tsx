// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { TorrentPlayerDialogLayout } from '@/components/torrent-player-dialog-layout';
import { getDemoVideoSource } from '@/lib/demo-media';
import { getDemoSubtitleTracks } from '@/lib/demo-subtitles';
import type { Torrent, TorrentFile } from '@/lib/types/api';
import { getInitialVideoFile, getVideoFiles } from '@/lib/video-utils';

const DEMO_TARGET_BYTES = 30 * 1024 * 1024; // 30 MB simulated buffer

interface DemoTorrentPlayerDialogProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void,
  enablePreload?: boolean
}

export function DemoTorrentPlayerDialog({
  torrent,
  open,
  onOpenChange,
  enablePreload = false,
}: DemoTorrentPlayerDialogProps) {
  const [userSelectedFile, setUserSelectedFile] = useState<TorrentFile | null>(null);
  const [isPreloading, setIsPreloading] = useState(false);
  const [preloadProgress, setPreloadProgress] = useState(0);
  const [downloadRate, setDownloadRate] = useState(0);
  const [activePeers, setActivePeers] = useState(0);
  const [totalPeers, setTotalPeers] = useState(0);

  const prevOpenRef = useRef(open);
  const preloadedFileRef = useRef<string | null>(null);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const stopSimulation = useCallback(() => {
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  if (open && !prevOpenRef.current) {
    setUserSelectedFile(null);
    preloadedFileRef.current = null;
    setIsPreloading(false);
    setPreloadProgress(0);
    setDownloadRate(0);
    setActivePeers(0);
    setTotalPeers(0);
  }
  prevOpenRef.current = open;

  const videoFiles = open && torrent ? getVideoFiles(torrent.files) : [];
  const initialFile = open && torrent ? getInitialVideoFile(videoFiles) : null;
  const selectedFile = userSelectedFile ?? initialFile;

  const handleExit = useCallback(() => {
    stopSimulation();
    setIsPreloading(false);
    if (videoFiles.length > 1) {
      setUserSelectedFile(null);
      preloadedFileRef.current = null;
    } else {
      onOpenChange(false);
      setUserSelectedFile(null);
      preloadedFileRef.current = null;
    }
  }, [onOpenChange, stopSimulation, videoFiles.length]);

  // Demo preload simulation
  useEffect(() => {
    if (!open || !torrent || !selectedFile || !enablePreload) {
      return;
    }

    if (preloadedFileRef.current === selectedFile.path) {
      return;
    }

    setIsPreloading(true);
    setPreloadProgress(0.15);
    // Simulated swarm: a handful of connected peers, a subset of them actively sending data.
    setActivePeers(4);
    setTotalPeers(9);
    setDownloadRate(Math.round(0.35 * DEMO_TARGET_BYTES / 0.2));

    let current = 0.15;
    timerRef.current = setInterval(() => {
      current += 0.35;
      if (current >= 1.0) {
        stopSimulation();
        setPreloadProgress(1.0);
        setDownloadRate(0);
        setTimeout(() => {
          preloadedFileRef.current = selectedFile.path;
          setIsPreloading(false);
        }, 300);
      } else {
        setPreloadProgress(current);
        // Vary the simulated rate and peer count slightly each tick so the badge feels live.
        setActivePeers(3 + Math.round(Math.random() * 3));
        setDownloadRate(Math.round((0.3 + Math.random() * 0.1) * DEMO_TARGET_BYTES / 0.2));
      }
    }, 200);

    return () => {
      stopSimulation();
    };
  }, [open, torrent, selectedFile, enablePreload, stopSimulation]);

  const videoPlayerOptions = useMemo(() => {
    if (selectedFile && torrent) {
      return {
        src: getDemoVideoSource(),
        title: selectedFile.name,
        autoPlay: true,
        tracks: getDemoSubtitleTracks(selectedFile, torrent.files),
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
          preloadedFileRef.current = null;
          setUserSelectedFile(videoFiles[selectedFileIndex - 1]);
        }
        : undefined,
      onNext: selectedFileIndex < videoFiles.length - 1
        ? () => {
          preloadedFileRef.current = null;
          setUserSelectedFile(videoFiles[selectedFileIndex + 1]);
        }
        : undefined,
    }
    : undefined;

  const preloadBadge = isPreloading
    ? {
      progress: preloadProgress,
      completedBytes: Math.round(preloadProgress * DEMO_TARGET_BYTES),
      targetBytes: DEMO_TARGET_BYTES,
      downloadRate,
      activePeers,
      totalPeers,
    }
    : null;

  return (
    <TorrentPlayerDialogLayout
      open={open}
      onOpenChange={onOpenChange}
      videoFiles={videoFiles}
      setSelectedFile={file => {
        preloadedFileRef.current = null;
        setUserSelectedFile(file);
      }}
      isPlayerVisible={isPlayerVisible}
      videoPlayerOptions={videoPlayerOptions}
      handleExit={handleExit}
      playlistNavigation={playlistNavigation}
      preloadBadge={preloadBadge}
      isDemo={true}
    />
  );
}
