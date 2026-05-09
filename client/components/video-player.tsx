// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

// Import base CSS for custom layouts
import '@vidstack/react/player/styles/base.css';

import { Capacitor } from '@capacitor/core';
import { ActivityAction, IntentLauncher, IntentLauncherParams } from '@capgo/capacitor-intent-launcher';
import { isTauri } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';
import {
  Controls,
  MediaPlayer,
  type MediaPlayerInstance,
  MediaProvider,
  PlayButton,
  Spinner,
  Time,
  TimeSlider,
  type VideoSrc,
} from '@vidstack/react';
import {
  FullscreenExitIcon,
  FullscreenIcon,
  PauseIcon,
  PlayIcon,
  SeekBackward10Icon,
  SeekForward10Icon
} from '@vidstack/react/icons';
import { X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

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

  useEffect(() => {
    const setPlayerPreference = () => {
      const externalPlayer = localStorage.getItem('external_player');
      setUseExternalPlayer(IS_NATIVE || !!externalPlayer);
      setPreferenceLoaded(true);
    };

    setPlayerPreference();
  }, []);

  useEffect(() => {
    if (onExit) {
      handleEndedRef.current = onExit;
    }
  }, [onExit]);

  const handleEnded = () => {
    if (handleEndedRef.current && hasPlayedRef.current) {
      handleEndedRef.current();
    }
  };

  const handlePlay = () => {
    hasPlayedRef.current = true;
  };

  useEffect(() => {
    const handleVideoPlayback = async () => {
      if (!preferenceLoaded || !useExternalPlayer || intentLaunched.current) return;

      let url: string | undefined;
      if (options.src) {
        if (typeof options.src === 'string') {
          url = options.src;
        } else if ('src' in options.src && typeof options.src.src === 'string') {
          url = options.src.src;
        }
      }

      if (!url) {
        console.error('Video source is not a valid URL for an external player.');
        if (onExit) onExit();
        return;
      }

      if (IS_TAURI) {
        try {
          const externalPlayer = localStorage.getItem('external_player');
          await openUrl(url, externalPlayer || undefined);
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
            data: url,
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
  }, [options, onExit, useExternalPlayer, preferenceLoaded]);

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

  const toggleFullscreen = () => {
    if (player.current) {
      if (isFullscreen) {
        player.current.exitFullscreen();
      } else {
        player.current.enterFullscreen();
      }
    }
  };

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
          <button onClick={() => onExit()}
            className='absolute z-10 top-2 right-2 flex h-10 w-10 items-center justify-center rounded-full bg-black/50 text-white ring-white/50 transition-all hover:bg-white/20 focus:ring-4'>
            <X className='h-6 w-6' />
          </button>
        )}
        <div className='absolute inset-0 flex w-full items-center justify-center gap-x-4 group-data-[fullscreen]:gap-x-12'>
          <div onClick={() => seek(-10)}
            className='flex h-16 w-16 group-data-[fullscreen]:h-32 group-data-[fullscreen]:w-32 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus:ring-4'>
            <SeekBackward10Icon className='h-10 w-10 group-data-[fullscreen]:h-20 group-data-[fullscreen]:w-20' />
          </div>
          <PlayButton className='flex h-20 w-20 group-data-[fullscreen]:h-36 group-data-[fullscreen]:w-36 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus-visible:ring-4 outline-none'>
            <PlayIcon className='h-12 w-12 group-data-[fullscreen]:h-24 group-data-[fullscreen]:w-24 hidden group-data-[paused]:block' />
            <PauseIcon className='h-12 w-12 group-data-[fullscreen]:h-24 group-data-[fullscreen]:w-24 hidden group-data-[playing]:block' />
          </PlayButton>
          <div onClick={() => seek(10)}
            className='flex h-16 w-16 group-data-[fullscreen]:h-32 group-data-[fullscreen]:w-32 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus:ring-4'>
            <SeekForward10Icon className='h-10 w-10 group-data-[fullscreen]:h-20 group-data-[fullscreen]:w-20' />
          </div>
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
              <Time type='duration' />
              <button onClick={toggleFullscreen}
                className='flex h-10 w-10 items-center justify-center rounded-full text-white ring-white/50 transition-all hover:bg-white/10 focus:ring-4'>
                {isFullscreen ? (
                  <FullscreenExitIcon className='w-7 h-7' />
                ) : (
                  <FullscreenIcon className='w-7 h-7' />
                )}
              </button>
            </div>
          </div>
        </Controls.Group>
      </div>
    </MediaPlayer>
  );
};

export default VideoPlayer;
