// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { Capacitor } from '@capacitor/core';
import { ActivityAction, IntentLauncher, type IntentLauncherParams } from '@capgo/capacitor-intent-launcher';
import { isTauri } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';
import {
  type MediaErrorDetail,
  MediaPlayer,
  type MediaPlayerInstance,
  MediaProvider,
  type PlayerSrc,
} from '@vidstack/react';
import { useCallback, useEffect, useRef, useState } from 'react';

import { useSubtitleTracks } from '@/hooks/use-subtitle-tracks';
import {
  type AudioTrackInfo,
  getDefaultAudioTrackIndex,
  isAudioDecodingSupported,
  MkvAudioSyncEngine,
  probeAudioTracks,
} from '@/lib/mkv-audio';
import { isMkvOrWebmStream } from '@/lib/mkv-subtitles';
import { type PreloadBadgeInfo, type SubtitleTrackInfo } from '@/lib/video-utils';
import { getVidstackVideoElement } from '@/lib/vidstack-media';

import { useVideoPlayerControls, VideoPlayerCaptions, VideoPlayerControls } from './video-player-controls';

// Standard HTMLMediaElement error codes (MEDIA_ERR_*), which Vidstack forwards as-is
// on `detail.code` for both native decode errors and its own source-resolution failures.
const PLAYBACK_ERROR_MESSAGES: Record<number, string> = {
  1: 'Playback was aborted.',
  2: 'A network error interrupted playback. Check your connection and try again.',
  3: 'This video could not be decoded by the internal player.',
  4: 'This video container or codec is not supported by the internal player.',
};

const PLAYBACK_POSITION_SAVE_INTERVAL_MS = 5000;

function getPlaybackErrorMessage(detail: MediaErrorDetail): string {
  if (detail.code && PLAYBACK_ERROR_MESSAGES[detail.code]) {
    return PLAYBACK_ERROR_MESSAGES[detail.code];
  }
  return detail.message || 'Playback failed in the internal player.';
}

export interface VideoPlayerProps {
  initialPlaybackPositionSeconds?: number,
  internalOnly?: boolean,
  onExit?: () => void,
  onPlaybackPositionChange?: (positionSeconds: number) => void,
  options: {
    autoPlay?: boolean,
    src?: PlayerSrc,
    title?: string,
    tracks?: SubtitleTrackInfo[]
  },
  playlistNavigation?: {
    onNext?: () => void,
    onPrevious?: () => void
  },
  preloadBadge?: PreloadBadgeInfo | null
}

const IS_NATIVE = Capacitor.isNativePlatform();
const IS_TAURI = isTauri();

const VideoPlayer: React.FC<VideoPlayerProps> = ({
  initialPlaybackPositionSeconds = 0,
  internalOnly = false,
  onExit,
  onPlaybackPositionChange,
  options,
  playlistNavigation,
  preloadBadge,
}) => {
  const isPreloading = !!preloadBadge;
  const player = useRef<MediaPlayerInstance>(null);
  const intentLaunched = useRef(false);
  const [useExternalPlayer, setUseExternalPlayer] = useState(false);
  const [preferenceLoaded, setPreferenceLoaded] = useState(internalOnly);
  const hasPlayedRef = useRef(false);
  const handleEndedRef = useRef<(() => void) | null>(null);
  const hasPositionUpdateRef = useRef(false);
  const lastPlaybackPositionRef = useRef(initialPlaybackPositionSeconds);
  const lastPositionSaveMsRef = useRef(0);
  const restoredSourceRef = useRef<string | undefined>(undefined);

  // Audio track management
  const [audioTracks, setAudioTracks] = useState<AudioTrackInfo[]>([]);
  const [selectedAudioTrack, setSelectedAudioTrack] = useState<number>(0);
  const [isWasmAudioActive, setIsWasmAudioActive] = useState<boolean>(false);
  const [playbackError, setPlaybackError] = useState<{ source?: string, message: string } | null>(null);
  const syncEngineRef = useRef<MkvAudioSyncEngine | null>(null);
  const nativeAudioTrackIndexRef = useRef(0);

  const streamUrl = typeof options.src === 'string'
    ? options.src
    : (options.src && 'src' in options.src && typeof options.src.src === 'string')
      ? options.src.src
      : undefined;

  const isMkv = isMkvOrWebmStream(streamUrl) ||
    (typeof options.src === 'object' && 'type' in options.src && options.src.type === 'video/webm');
  const {
    tracks: allSubtitleTracks,
    selectedTrackId: selectedSubtitleTrack,
    selectTrack: handleSelectSubtitleTrack,
  } = useSubtitleTracks({
    player,
    sourceKey: streamUrl,
    tracks: options.tracks,
    enabled: preferenceLoaded && !useExternalPlayer && !isPreloading,
    embeddedStreamUrl: isMkv ? streamUrl : undefined,
  });
  const {
    isFullscreen,
    setIsFullscreen,
    seek,
    toggleFullscreen,
  } = useVideoPlayerControls({
    player,
    subtitleTracks: allSubtitleTracks,
    selectedSubtitleTrack,
    onSelectSubtitleTrack: handleSelectSubtitleTrack,
    // Keep in sync with useSubtitleTracks' `enabled` above: keyboard shortcuts
    // (including subtitle cycling) shouldn't fire while the player has no source
    // loaded during preloading.
    enabled: preferenceLoaded && !useExternalPlayer && !isPreloading,
  });

  const persistPlaybackPosition = useCallback((positionSeconds: number, force: boolean) => {
    if (!isFinite(positionSeconds) || positionSeconds < 0) return;
    lastPlaybackPositionRef.current = positionSeconds;
    hasPositionUpdateRef.current = true;
    const now = Date.now();
    if (!force && now - lastPositionSaveMsRef.current < PLAYBACK_POSITION_SAVE_INTERVAL_MS) return;
    lastPositionSaveMsRef.current = now;
    onPlaybackPositionChange?.(positionSeconds);
  }, [onPlaybackPositionChange]);

  useEffect(() => {
    hasPositionUpdateRef.current = false;
    lastPlaybackPositionRef.current = initialPlaybackPositionSeconds;
    lastPositionSaveMsRef.current = 0;
    restoredSourceRef.current = undefined;
    return () => {
      if (hasPositionUpdateRef.current) {
        onPlaybackPositionChange?.(lastPlaybackPositionRef.current);
      }
    };
  }, [initialPlaybackPositionSeconds, onPlaybackPositionChange, streamUrl]);

  useEffect(() => {
    const handlePageHide = () => {
      if (hasPositionUpdateRef.current) {
        onPlaybackPositionChange?.(lastPlaybackPositionRef.current);
      }
    };
    window.addEventListener('pagehide', handlePageHide);
    return () => window.removeEventListener('pagehide', handlePageHide);
  }, [onPlaybackPositionChange]);

  useEffect(() => {
    if (internalOnly) {
      setUseExternalPlayer(false);
      setPreferenceLoaded(true);
      return;
    }

    const setPlayerPreference = () => {
      const externalPlayer = localStorage.getItem('external_player');
      setUseExternalPlayer(IS_NATIVE || !!externalPlayer);
      setPreferenceLoaded(true);
    };

    setPlayerPreference();
  }, [internalOnly]);

  useEffect(() => {
    if (!streamUrl || useExternalPlayer || !isAudioDecodingSupported() || isPreloading) return;

    let cancelled = false;
    probeAudioTracks(streamUrl)
      .then(({ input, tracks, audioTrackObjects }) => {
        if (cancelled) {
          input.dispose();
          return;
        }
        setAudioTracks(tracks);
        if (tracks.length > 0) {
          const engine = new MkvAudioSyncEngine(
            input,
            audioTrackObjects,
            err => {
              console.debug('MkvAudioSyncEngine stopped due to decoder error:', err);
              setIsWasmAudioActive(false);
              engine.setWasmActive(false);
            }
          );
          syncEngineRef.current = engine;

          const videoEl = getVidstackVideoElement(player.current);
          if (videoEl) {
            engine.attachMediaElement(videoEl);
          }

          const currentVolume = player.current?.volume ?? 1;
          const currentMuted = player.current?.muted ?? false;
          engine.setVolume(currentVolume);
          engine.setMuted(currentMuted);

          const defaultTrackIndex = getDefaultAudioTrackIndex(tracks);
          nativeAudioTrackIndexRef.current = defaultTrackIndex;
          engine.setNativeTrackIndex(defaultTrackIndex);
          const defaultTrack = tracks[defaultTrackIndex];
          const requiresWasm = defaultTrack ? !defaultTrack.isNativelySupported : false;

          setSelectedAudioTrack(defaultTrackIndex);
          if (requiresWasm) {
            const activated = engine.selectTrack(defaultTrackIndex);
            setIsWasmAudioActive(activated);
            if (!activated) return;

            // If the player is already playing when probing finishes, start audio immediately
            if (player.current && !player.current.paused) {
              engine.onPlay(player.current.currentTime);
            }
          } else {
            setIsWasmAudioActive(false);
            engine.setWasmActive(false);
          }
        }
      })
      .catch(err => {
        console.debug('Failed to probe audio tracks for stream:', err);
      });

    return () => {
      cancelled = true;
      syncEngineRef.current?.destroy();
      syncEngineRef.current = null;
      nativeAudioTrackIndexRef.current = 0;
      setAudioTracks([]);
      setIsWasmAudioActive(false);
    };
  }, [streamUrl, useExternalPlayer, isPreloading]);

  const handleSelectAudioTrack = (index: number) => {
    setSelectedAudioTrack(index);
    const track = audioTracks[index];
    if (!track) return;

    const engine = syncEngineRef.current;
    if (!engine) return;

    if (player.current) {
      engine.setVolume(player.current.volume);
      engine.setMuted(player.current.muted);
    }

    // The browser owns the container's default native track. All other tracks
    // route through the sync engine because HTML media lacks portable switching.
    const requiresWasm = index !== nativeAudioTrackIndexRef.current || !track.isNativelySupported;

    const videoEl = getVidstackVideoElement(player.current);
    if (videoEl) {
      engine.attachMediaElement(videoEl);
    }

    if (requiresWasm) {
      const activated = engine.selectTrack(index);
      setIsWasmAudioActive(activated);
      if (!activated) return;
      if (player.current && !player.current.paused) {
        engine.onPlay(player.current.currentTime);
      }
    } else {
      engine.setNativeTrackIndex(index);
      setIsWasmAudioActive(false);
      engine.setWasmActive(false);
      engine.onPause();
    }
  };

  const handleEnded = () => {
    if (isWasmAudioActive && syncEngineRef.current) {
      syncEngineRef.current.onPause();
    }
    if (hasPlayedRef.current) {
      persistPlaybackPosition(0, true);
      handleEndedRef.current?.();
    }
  };

  const handlePlay = () => {
    hasPlayedRef.current = true;
    if (syncEngineRef.current && player.current) {
      const videoEl = getVidstackVideoElement(player.current);
      if (videoEl && syncEngineRef.current.attachMediaElement(videoEl) && isWasmAudioActive) {
        syncEngineRef.current.onPlay(player.current.currentTime);
      } else if (isWasmAudioActive) {
        syncEngineRef.current.setWasmActive(false);
        setIsWasmAudioActive(false);
      }
    }
  };

  const handlePause = () => {
    if (isWasmAudioActive && syncEngineRef.current) {
      syncEngineRef.current.onPause();
    }
    if (hasPlayedRef.current && player.current) {
      persistPlaybackPosition(player.current.currentTime, true);
    }
  };

  const handleSeeked = () => {
    if (isWasmAudioActive && syncEngineRef.current && player.current) {
      syncEngineRef.current.onSeek(player.current.currentTime);
    }
    if (hasPlayedRef.current && player.current) {
      persistPlaybackPosition(player.current.currentTime, true);
    }
  };

  const handleTimeUpdate = (detail: { currentTime: number }) => {
    if (isWasmAudioActive && syncEngineRef.current) {
      syncEngineRef.current.onTimeUpdate(detail.currentTime);
    }
    if (hasPlayedRef.current) {
      persistPlaybackPosition(detail.currentTime, false);
    }
  };

  const handleWaiting = () => {
    if (isWasmAudioActive && syncEngineRef.current) {
      syncEngineRef.current.onWaiting();
    }
  };

  const handlePlaying = () => {
    if (syncEngineRef.current && player.current) {
      const videoEl = getVidstackVideoElement(player.current);
      if (videoEl && syncEngineRef.current.attachMediaElement(videoEl) && isWasmAudioActive) {
        syncEngineRef.current.onPlaying(player.current.currentTime);
      } else if (isWasmAudioActive) {
        syncEngineRef.current.setWasmActive(false);
        setIsWasmAudioActive(false);
      }
    }
  };

  const handleVolumeChange = (detail: { volume: number, muted: boolean }) => {
    if (isWasmAudioActive && syncEngineRef.current) {
      syncEngineRef.current.setVolume(detail.volume);
      syncEngineRef.current.setMuted(detail.muted);
    }
  };

  const handleRateChange = (rate: number) => {
    if (isWasmAudioActive && syncEngineRef.current) {
      syncEngineRef.current.setPlaybackRate(rate);
    }
  };

  useEffect(() => {
    const handleVideoPlayback = async () => {
      if (!preferenceLoaded || !useExternalPlayer || intentLaunched.current) return;

      if (!streamUrl) {
        console.error('Video source is not a valid URL for an external player.');
        if (onExit) onExit();
        return;
      }

      if (IS_TAURI) {
        try {
          const externalPlayer = localStorage.getItem('external_player');
          await openUrl(streamUrl, externalPlayer || undefined);
          if (onExit) onExit();
        } catch (error) {
          console.error(error);
          setUseExternalPlayer(false);
        }
      } else if (IS_NATIVE) {
        intentLaunched.current = true;
        try {
          const intentPayload: IntentLauncherParams = {
            action: ActivityAction.VIEW,
            data: streamUrl,
            type: 'video/*',
          };

          if (options.title) {
            intentPayload.extra = {
              'android.intent.extra.TITLE': options.title,
              'title': options.title,
            };
          }

          await IntentLauncher.startActivityAsync(intentPayload);
          if (onExit) onExit();
        } catch (error) {
          console.error('Failed to open URL with IntentLauncher', error);
          intentLaunched.current = false;
          setUseExternalPlayer(false);
        }
      }
    };

    handleVideoPlayback();
  }, [streamUrl, options.title, onExit, useExternalPlayer, preferenceLoaded]);

  useEffect(() => {
    if (onExit) {
      handleEndedRef.current = onExit;
    }
  }, [onExit]);

  const prevPreloadingRef = useRef(isPreloading);
  const canPlayRef = useRef(false);

  const tryPlay = useCallback(() => {
    if (!player.current) return;
    try {
      if (player.current.state?.canPlay || canPlayRef.current) {
        const playPromise = player.current.play();
        if (playPromise && typeof playPromise.catch === 'function') {
          playPromise.catch(() => {});
        }
      }
    } catch {
      // Ignored if media not ready or autoplay blocked
    }
  }, []);

  const handleCanPlay = useCallback(() => {
    canPlayRef.current = true;
    setPlaybackError(null);
    if (
      player.current
      && streamUrl
      && restoredSourceRef.current !== streamUrl
      && initialPlaybackPositionSeconds > 0
      && (!player.current.duration || initialPlaybackPositionSeconds < player.current.duration)
    ) {
      player.current.currentTime = initialPlaybackPositionSeconds;
      restoredSourceRef.current = streamUrl;
    }
    if (!isPreloading && options.autoPlay) {
      tryPlay();
    }
  }, [initialPlaybackPositionSeconds, isPreloading, options.autoPlay, streamUrl, tryPlay]);

  useEffect(() => {
    if (prevPreloadingRef.current && !isPreloading && options.autoPlay) {
      tryPlay();
    }
    prevPreloadingRef.current = isPreloading;
  }, [isPreloading, options.autoPlay, tryPlay]);

  if (!preferenceLoaded) {
    return null;
  }

  if (useExternalPlayer) {
    return null;
  }

  const playbackErrorMessage = playbackError && playbackError.source === streamUrl
    ? playbackError.message
    : null;

  return (
    <MediaPlayer
      ref={player}
      className='group bg-black text-white font-sans rounded-lg aspect-video w-full'
      title={options.title}
      src={isPreloading ? undefined : options.src}
      autoPlay={options.autoPlay && !isPreloading}
      onCanPlay={handleCanPlay}
      onFullscreenChange={setIsFullscreen}
      onEnded={handleEnded}
      onPlay={handlePlay}
      onPlaying={handlePlaying}
      onWaiting={handleWaiting}
      onTimeUpdate={handleTimeUpdate}
      onPause={handlePause}
      onSeeked={handleSeeked}
      onVolumeChange={handleVolumeChange}
      onRateChange={handleRateChange}
      onError={detail => setPlaybackError({
        source: streamUrl,
        message: getPlaybackErrorMessage(detail),
      })}
      playsInline
    >
      {/* Keyed on streamUrl (not <MediaPlayer> itself) so switching sources forces a fresh
          native media element - preventing stale/overlapping WASM audio across sources -
          without removing the fullscreen element from the DOM. <MediaPlayer> is the element
          the browser actually fullscreens, so keeping it mounted across playlist navigation
          is what lets fullscreen survive next/previous. */}
      <MediaProvider key={streamUrl ?? 'video-player'} />
      {playbackErrorMessage && !isPreloading && (
        <div
          role='alert'
          className='absolute inset-0 z-[5] flex items-center justify-center bg-black/85 px-6 text-center text-sm text-white sm:text-base'
        >
          {playbackErrorMessage}
        </div>
      )}
      <VideoPlayerCaptions tracks={allSubtitleTracks}
        selectedTrackId={selectedSubtitleTrack} />
      <VideoPlayerControls
        title={options.title}
        onExit={onExit}
        onSeek={seek}
        isFullscreen={isFullscreen}
        onToggleFullscreen={toggleFullscreen}
        audioTracks={audioTracks}
        selectedAudioTrack={selectedAudioTrack}
        onSelectAudioTrack={handleSelectAudioTrack}
        subtitleTracks={allSubtitleTracks}
        selectedSubtitleTrack={selectedSubtitleTrack}
        onSelectSubtitleTrack={handleSelectSubtitleTrack}
        playlistNavigation={playlistNavigation}
        preloadBadge={preloadBadge}
      />
    </MediaPlayer>
  );
};

export default VideoPlayer;
