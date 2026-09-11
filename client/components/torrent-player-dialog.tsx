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
  const [downloadRate, setDownloadRate] = useState(0);
  const [activePeers, setActivePeers] = useState(0);
  const [totalPeers, setTotalPeers] = useState(0);

  const prevOpenRef = useRef(open);
  const preloadedFileRef = useRef<string | null>(null);
  const activePreloadHashRef = useRef<string | null>(null);
  const pollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const timeoutTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // The torrent/file the caller hands us gets a brand-new object identity on every
  // poll refresh upstream (e.g. SWR revalidation), even when nothing relevant changed.
  // The preload-trigger effect below must not restart on that churn, so it keys off
  // the stable torrent.hash/selectedFile.path below and reads the latest objects via
  // these refs instead of depending on the objects themselves.
  const torrentRef = useRef(torrent);
  torrentRef.current = torrent;
  const selectedFileForEffectRef = useRef<TorrentFile | null>(null);

  const stopPreloadPolling = useCallback(() => {
    if (pollTimerRef.current) {
      clearTimeout(pollTimerRef.current);
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
    setDownloadRate(0);
    setActivePeers(0);
    setTotalPeers(0);
  }
  prevOpenRef.current = open;

  const computed = computeVideoFiles(torrent);

  const videoFiles = open ? computed.videoFiles : [];
  const selectedFile = open ? (userSelectedFile ?? computed.selectedFile) : null;
  selectedFileForEffectRef.current = selectedFile;

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

  // Implicit preload trigger when a playable file is selected.
  // Keyed on torrent.hash/selectedFile.path (stable strings) rather than the torrent/
  // selectedFile objects themselves: the caller's torrent prop gets a brand-new object
  // identity on every upstream poll refresh even when nothing relevant changed, and
  // depending on the objects directly would cancel and restart this preload on every
  // such refresh - potentially forever, if a preload takes longer than the poll interval.
  const torrentHash = torrent?.hash ?? null;
  const selectedFilePath = selectedFile?.path ?? null;

  useEffect(() => {
    const currentTorrent = torrentRef.current;
    const currentSelectedFile = selectedFileForEffectRef.current;

    if (!open || !currentTorrent || !currentSelectedFile || !enablePreload) {
      cancelActivePreload();
      return;
    }

    if (preloadedFileRef.current === currentSelectedFile.path) {
      return;
    }

    let isMounted = true;

    if (activePreloadHashRef.current && activePreloadHashRef.current !== currentTorrent.hash) {
      cancelPreload(activePreloadHashRef.current).catch(() => {});
    }
    activePreloadHashRef.current = currentTorrent.hash;

    setIsPreloading(true);
    setPreloadProgress(0);
    setCompletedBytes(0);
    setTargetBytes(0);
    setDownloadRate(0);
    setActivePeers(0);
    setTotalPeers(0);

    startPreload(currentTorrent.hash, { filePath: currentSelectedFile.path })
      .then(resp => {
        if (!isMounted) return;
        setPreloadProgress(current => Math.max(current, resp.progress || 0));
        setCompletedBytes(current => Math.max(current, resp.completedBytes || 0));
        setTargetBytes(current => Math.max(current, resp.targetBytes || 0));
        setDownloadRate(resp.downloadRate || 0);
        setActivePeers(resp.activePeers || 0);
        setTotalPeers(resp.totalPeers || 0);

        if (resp.status === 'idle') {
          cancelActivePreload();
          preloadedFileRef.current = currentSelectedFile.path;
          setIsPreloading(false);
          return;
        }

        if (resp.status === 'ready' || (resp.progress && resp.progress >= 1)) {
          setPreloadProgress(1.0);
          setCompletedBytes(resp.targetBytes || resp.completedBytes || 0);
          setTargetBytes(resp.targetBytes || 0);
          setTimeout(() => {
            if (!isMounted) return;
            preloadedFileRef.current = currentSelectedFile.path;
            setIsPreloading(false);
          }, 300);
          return;
        }

        // Start polling for preload progress. Each poll is scheduled only after the
        // previous one settles (rather than a fixed-cadence setInterval), so at most one
        // getPreload request is ever in flight - a slow response can't land after and
        // overwrite a newer, faster one.
        const poll = async () => {
          try {
            const statusResp = await getPreload(currentTorrent.hash);
            if (!isMounted) return;
            const progress = statusResp.progress || 0;
            setPreloadProgress(current => Math.max(current, progress));
            setCompletedBytes(current => Math.max(current, statusResp.completedBytes || 0));
            setTargetBytes(current => Math.max(current, statusResp.targetBytes || 0));
            setDownloadRate(statusResp.downloadRate || 0);
            setActivePeers(statusResp.activePeers || 0);
            setTotalPeers(statusResp.totalPeers || 0);

            if (statusResp.status === 'idle') {
              cancelActivePreload();
              preloadedFileRef.current = currentSelectedFile.path;
              setIsPreloading(false);
              return;
            }

            if (statusResp.status === 'ready' || progress >= 1) {
              stopPreloadPolling();
              setPreloadProgress(1.0);
              setTimeout(() => {
                if (!isMounted) return;
                preloadedFileRef.current = currentSelectedFile.path;
                setIsPreloading(false);
              }, 300);
              return;
            }

            if (!isMounted) return;
            pollTimerRef.current = setTimeout(poll, 400);
          } catch {
            // Polling error: stop preloading indicator
            cancelActivePreload();
            preloadedFileRef.current = currentSelectedFile.path;
            setIsPreloading(false);
          }
        };
        pollTimerRef.current = setTimeout(poll, 400);

        // Stop badge after timeout if still active
        timeoutTimerRef.current = setTimeout(() => {
          if (!isMounted) return;
          cancelActivePreload();
          preloadedFileRef.current = currentSelectedFile.path;
          setIsPreloading(false);
        }, 15000);
      })
      .catch(() => {
        if (!isMounted) return;
        // Network/API error: stop preloading indicator
        cancelActivePreload();
        preloadedFileRef.current = currentSelectedFile.path;
        setIsPreloading(false);
      });

    return () => {
      isMounted = false;
      stopPreloadPolling();
    };
  }, [cancelActivePreload, open, torrentHash, selectedFilePath, enablePreload, stopPreloadPolling]);

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
      const streamUrl = getTorrentStreamUrl(torrent.hash, selectedFile.path);
      const videoType = getVideoType(selectedFile.name);
      return {
        // Leave containers that Vidstack cannot describe untyped so the browser
        // can determine the format instead of being told they are MP4.
        src: videoType ? { src: streamUrl, type: videoType } : streamUrl,
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

  // Block the media source on the very first render for a newly selected file.
  // Waiting for the preload effect would allow the browser to issue a stream
  // request before preloading has even started.
  const shouldBlockForPreload = enablePreload && open && !!torrent && !!selectedFile &&
    (isPreloading || preloadedFileRef.current !== selectedFile.path);
  const preloadBadge = shouldBlockForPreload
    ? {
      progress: preloadProgress,
      completedBytes,
      targetBytes,
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
