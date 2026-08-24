// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

// Import base CSS for custom layouts
import '@vidstack/react/player/styles/base.css';

import { Capacitor } from '@capacitor/core';
import { ActivityAction, IntentLauncher, type IntentLauncherParams } from '@capgo/capacitor-intent-launcher';
import { isTauri } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';
import {
  Controls,
  MediaPlayer,
  type MediaPlayerInstance,
  MediaProvider,
  MuteButton,
  PlayButton,
  Spinner,
  Time,
  TimeSlider,
  type VideoSrc,
  VolumeSlider,
} from '@vidstack/react';
import {
  FullscreenExitIcon,
  FullscreenIcon,
  MuteIcon,
  PauseIcon,
  PlayIcon,
  SeekBackward10Icon,
  SeekForward10Icon,
  VolumeHighIcon,
  VolumeLowIcon
} from '@vidstack/react/icons';
import { X } from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';

import {
  type AudioTrackInfo,
  getDefaultAudioTrackIndex,
  isAudioDecodingSupported,
  MkvAudioSyncEngine,
  probeAudioTracks,
} from '@/lib/mkv-audio';
import { getVidstackVideoElement } from '@/lib/vidstack-media';

import { AudioTrackSelector } from './audio-track-selector';

interface VideoPlayerProps {
  options: {
    src?: VideoSrc,
    title?: string,
    autoPlay?: boolean
  },
  onExit?: () => void
}

const IS_NATIVE = Capacitor.isNativePlatform();
const IS_TAURI = isTauri();

const VideoPlayer: React.FC<VideoPlayerProps> = ({ options, onExit }) => {
  const player = useRef<MediaPlayerInstance>(null);
  const intentLaunched = useRef(false);
  const [useExternalPlayer, setUseExternalPlayer] = useState(false);
  const [preferenceLoaded, setPreferenceLoaded] = useState(false);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const hasPlayedRef = useRef(false);
  const handleEndedRef = useRef<(() => void) | null>(null);

  // Audio track management
  const [audioTracks, setAudioTracks] = useState<AudioTrackInfo[]>([]);
  const [selectedAudioTrack, setSelectedAudioTrack] = useState<number>(0);
  const [isWasmAudioActive, setIsWasmAudioActive] = useState<boolean>(false);
  const syncEngineRef = useRef<MkvAudioSyncEngine | null>(null);
  const nativeAudioTrackIndexRef = useRef(0);

  const streamUrl = typeof options.src === 'string'
    ? options.src
    : (options.src && 'src' in options.src && typeof options.src.src === 'string')
      ? options.src.src
      : undefined;

  useEffect(() => {
    const setPlayerPreference = () => {
      const externalPlayer = localStorage.getItem('external_player');
      setUseExternalPlayer(IS_NATIVE || !!externalPlayer);
      setPreferenceLoaded(true);
    };

    setPlayerPreference();
  }, []);

  useEffect(() => {
    if (!streamUrl || useExternalPlayer || !isAudioDecodingSupported()) return;

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
          const defaultTrack = tracks[defaultTrackIndex];
          const requiresWasm = defaultTrack ? !defaultTrack.isNativelySupported : false;

          setSelectedAudioTrack(defaultTrackIndex);
          if (requiresWasm) {
            setIsWasmAudioActive(true);
            engine.setWasmActive(true);
            engine.selectTrack(defaultTrackIndex);

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
  }, [streamUrl, useExternalPlayer]);

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
    setIsWasmAudioActive(requiresWasm);
    engine.setWasmActive(requiresWasm);

    const videoEl = getVidstackVideoElement(player.current);
    if (videoEl) {
      engine.attachMediaElement(videoEl);
    }

    if (requiresWasm) {
      engine.selectTrack(index);
      if (player.current && !player.current.paused) {
        engine.onPlay(player.current.currentTime);
      }
    } else {
      engine.onPause();
    }
  };

  const handleEnded = () => {
    if (isWasmAudioActive && syncEngineRef.current) {
      syncEngineRef.current.onPause();
    }
    if (handleEndedRef.current && hasPlayedRef.current) {
      handleEndedRef.current();
    }
  };

  const handlePlay = () => {
    hasPlayedRef.current = true;
    if (syncEngineRef.current && player.current) {
      const videoEl = getVidstackVideoElement(player.current);
      if (videoEl) {
        syncEngineRef.current.attachMediaElement(videoEl);
      }
      if (isWasmAudioActive) {
        syncEngineRef.current.onPlay(player.current.currentTime);
      }
    }
  };

  const handlePause = () => {
    if (isWasmAudioActive && syncEngineRef.current) {
      syncEngineRef.current.onPause();
    }
  };

  const handleSeeked = () => {
    if (isWasmAudioActive && syncEngineRef.current && player.current) {
      syncEngineRef.current.onSeek(player.current.currentTime);
    }
  };

  const handleTimeUpdate = (detail: { currentTime: number }) => {
    if (isWasmAudioActive && syncEngineRef.current) {
      syncEngineRef.current.onTimeUpdate(detail.currentTime);
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
      if (videoEl) {
        syncEngineRef.current.attachMediaElement(videoEl);
      }
      if (isWasmAudioActive) {
        syncEngineRef.current.onPlaying(player.current.currentTime);
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
          if (onExit) onExit();
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

          IntentLauncher.startActivityAsync(intentPayload);
          if (onExit) onExit();
        } catch (error) {
          console.error('Failed to open URL with IntentLauncher', error);
          if (onExit) onExit();
        }
      }
    };

    handleVideoPlayback();
  }, [streamUrl, options.title, onExit, useExternalPlayer, preferenceLoaded]);

  const BufferingIndicator = () => {
    return (
      <div className='pointer-events-none absolute inset-0 z-50 flex h-full w-full items-center justify-center'>
        <Spinner.Root
          className='text-white opacity-0 transition-opacity duration-200 ease-linear media-buffering:animate-spin media-buffering:opacity-100'
          size={84}
        >
          <Spinner.Track className='opacity-25'
            width={8} />
          <Spinner.TrackFill className='opacity-75'
            width={8} />
        </Spinner.Root>
      </div>
    );
  };

  const seek = (seconds: number) => {
    if (player.current) {
      player.current.currentTime += seconds;
    }
  };

  const toggleFullscreen = useCallback(() => {
    if (player.current) {
      if (isFullscreen) {
        player.current.exitFullscreen();
      } else {
        player.current.enterFullscreen();
      }
    }
  }, [isFullscreen]);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      const activeEl = document.activeElement;
      const isInput = activeEl && (activeEl.tagName === 'INPUT' || activeEl.tagName === 'TEXTAREA' || (activeEl as HTMLElement).isContentEditable);
      if (isInput) return;

      if (e.key === ' ' || e.key === 'k' || e.key === 'K') {
        e.preventDefault();
        if (player.current) {
          if (player.current.paused) {
            player.current.play();
          } else {
            player.current.pause();
          }
        }
      } else if (e.key === 'ArrowLeft') {
        const isSlider = activeEl?.getAttribute('role') === 'slider';
        if (!isSlider) {
          e.preventDefault();
          seek(-10);
        }
      } else if (e.key === 'ArrowRight') {
        const isSlider = activeEl?.getAttribute('role') === 'slider';
        if (!isSlider) {
          e.preventDefault();
          seek(10);
        }
      } else if (e.key === 'f' || e.key === 'F') {
        e.preventDefault();
        toggleFullscreen();
      } else if (e.key === 'm' || e.key === 'M') {
        e.preventDefault();
        if (player.current) {
          player.current.muted = !player.current.muted;
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [toggleFullscreen]);

  useEffect(() => {
    if (onExit) {
      handleEndedRef.current = onExit;
    }
  }, [onExit]);

  if (!preferenceLoaded) {
    return null;
  }

  if (useExternalPlayer) {
    return null;
  }

  return (
    <MediaPlayer
      ref={player}
      className='group bg-black text-white font-sans rounded-lg aspect-video w-full'
      title={options.title}
      src={options.src}
      autoPlay={options.autoPlay}
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
      playsInline
    >
      <MediaProvider />
      <BufferingIndicator />
      <div className='absolute inset-0 z-10 w-full opacity-0 group-data-[controls]:opacity-100 transition-opacity'>
        {options.title && (
          <div className='absolute top-2 left-0 right-0 text-center px-4 py-2 bg-black/50 backdrop-blur-sm text-white font-medium truncate'>
            {options.title}
          </div>
        )}
        {onExit && (
          <button
            onClick={() => onExit()}
            aria-label='Close player'
            className='absolute z-10 top-2 right-2 flex h-10 w-10 items-center justify-center rounded-full bg-black/50 text-white ring-white/50 transition-all hover:bg-white/20 focus:ring-4 outline-none'
          >
            <X className='h-6 w-6' />
            <span className='sr-only'>Close player</span>
          </button>
        )}
        <div className='absolute inset-0 flex w-full items-center justify-center gap-x-4 group-data-[fullscreen]:gap-x-12'>
          <button
            type='button'
            onClick={() => seek(-10)}
            aria-label='Seek backward 10 seconds'
            className='flex h-16 w-16 group-data-[fullscreen]:h-32 group-data-[fullscreen]:w-32 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus-visible:ring-4 outline-none'
          >
            <SeekBackward10Icon className='h-10 w-10 group-data-[fullscreen]:h-20 group-data-[fullscreen]:w-20' />
            <span className='sr-only'>Seek backward 10 seconds</span>
          </button>
          <PlayButton className='flex h-20 w-20 group-data-[fullscreen]:h-36 group-data-[fullscreen]:w-36 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus-visible:ring-4 outline-none'>
            <PlayIcon className='h-12 w-12 group-data-[fullscreen]:h-24 group-data-[fullscreen]:w-24 hidden group-data-[paused]:block' />
            <PauseIcon className='h-12 w-12 group-data-[fullscreen]:h-24 group-data-[fullscreen]:w-24 hidden group-data-[playing]:block' />
          </PlayButton>
          <button
            type='button'
            onClick={() => seek(10)}
            aria-label='Seek forward 10 seconds'
            className='flex h-16 w-16 group-data-[fullscreen]:h-32 group-data-[fullscreen]:w-32 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus-visible:ring-4 outline-none'
          >
            <SeekForward10Icon className='h-10 w-10 group-data-[fullscreen]:h-20 group-data-[fullscreen]:w-20' />
            <span className='sr-only'>Seek forward 10 seconds</span>
          </button>
        </div>
        <div className='absolute inset-x-0 bottom-0 w-full h-2/5 bg-gradient-to-t from-black/50 to-transparent pointer-events-none' />
        <Controls.Group className='absolute bottom-3 left-0 right-0 flex flex-col items-center px-2 py-4'>
          <TimeSlider.Root className='mx-2 media-slider group relative inline-flex h-10 w-full cursor-pointer select-none items-center outline-none'>
            <TimeSlider.Track className='relative ring-sky-400 z-0 h-2.5 w-full rounded-sm bg-white/20 group-data-[focus]:ring-[3px]'>
              <TimeSlider.TrackFill className='bg-white/70 absolute h-full w-[var(--slider-fill)] rounded-sm will-change-[width]' />
              <TimeSlider.Progress className='absolute z-10 h-full w-[var(--slider-progress)] rounded-sm bg-white/30 will-change-[width]' />
            </TimeSlider.Track>
            <TimeSlider.Thumb className='absolute left-[var(--slider-fill)] z-20 h-5 w-5 -translate-x-1/2 rounded-full border border-primary bg-white shadow-sm ring-white/40 will-change-[left] group-data-[active]:ring-4' />
          </TimeSlider.Root>
          <div className='w-full flex justify-between text-sm px-2 items-center'>
            <Time type='current' />
            <div className='flex items-center gap-x-2'>
              <MuteButton className='flex h-10 w-10 items-center justify-center rounded-full text-white ring-white/50 transition-all hover:bg-white/10 focus:ring-4'>
                <MuteIcon className='w-7 h-7 hidden group-data-[muted]:block' />
                <VolumeLowIcon className='w-7 h-7 hidden group-data-[volume-low]:block' />
                <VolumeHighIcon className='w-7 h-7 group-data-[muted]:hidden group-data-[volume-low]:hidden' />
              </MuteButton>
              <VolumeSlider.Root className='group relative mx-2 inline-flex h-10 w-24 max-w-[80px] cursor-pointer select-none items-center outline-none'>
                <VolumeSlider.Track className='relative ring-sky-400 z-0 h-2.5 w-full rounded-sm bg-white/20 group-data-[focus]:ring-[3px]'>
                  <VolumeSlider.TrackFill className='bg-white/70 absolute h-full w-[var(--slider-fill)] rounded-sm will-change-[width]' />
                </VolumeSlider.Track>
                <VolumeSlider.Thumb className='absolute left-[var(--slider-fill)] z-20 h-5 w-5 -translate-x-1/2 rounded-full border border-primary bg-white shadow-sm ring-white/40 will-change-[left] group-data-[active]:ring-4' />
              </VolumeSlider.Root>
              <Time type='duration' />
              <AudioTrackSelector
                tracks={audioTracks}
                selectedTrackIndex={selectedAudioTrack}
                onSelectTrack={handleSelectAudioTrack}
              />
              <button
                type='button'
                onClick={toggleFullscreen}
                aria-label={isFullscreen ? 'Exit fullscreen' : 'Enter fullscreen'}
                className='flex h-10 w-10 items-center justify-center rounded-full text-white ring-white/50 transition-all hover:bg-white/10 focus:ring-4 outline-none'
              >
                {isFullscreen ? (
                  <FullscreenExitIcon className='w-7 h-7' />
                ) : (
                  <FullscreenIcon className='w-7 h-7' />
                )}
                <span className='sr-only'>{isFullscreen ? 'Exit fullscreen' : 'Enter fullscreen'}</span>
              </button>
            </div>
          </div>
        </Controls.Group>
      </div>
    </MediaPlayer>
  );
};

export default VideoPlayer;
