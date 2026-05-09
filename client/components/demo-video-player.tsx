// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import '@vidstack/react/player/styles/base.css';

import {
  Controls,
  MediaPlayer,
  type MediaPlayerInstance,
  MediaProvider,
  PlayButton,
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
import { useRef, useState } from 'react';

interface DemoVideoPlayerProps {
  options: {
    src?: VideoSrc,
    title?: string,
    autoPlay?: boolean
  },
  onExit?: () => void
}

const DemoVideoPlayer: React.FC<DemoVideoPlayerProps> = ({ options, onExit }) => {
  const player = useRef<MediaPlayerInstance>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);

  const seek = (seconds: number) => {
    if (player.current) {
      player.current.currentTime += seconds;
    }
  };

  const toggleFullscreen = () => {
    if (!player.current) return;

    try {
      if (isFullscreen) {
        player.current.exitFullscreen();
      } else {
        player.current.enterFullscreen();
      }
    } catch (error) {
      console.error('Fullscreen error:', error);
    }
  };

  return (
    <MediaPlayer
      ref={player}
      className='group bg-black text-white font-sans rounded-lg aspect-video w-full'
      title={options.title}
      src={options.src}
      autoPlay={options.autoPlay}
      onFullscreenChange={setIsFullscreen}
      playsInline
    >
      <MediaProvider />
      <div className='absolute inset-0 z-10 w-full opacity-100 transition-opacity'>
        {onExit && (
          <button onClick={() => onExit()}
            className='absolute z-10 top-2 right-2 flex h-10 w-10 items-center justify-center rounded-full bg-black/50 text-white ring-white/50 transition-all hover:bg-white/20 focus:ring-4'>
            <X className='h-6 w-6' />
          </button>
        )}
        <div className='absolute inset-0 flex w-full items-center justify-center gap-x-4 media-fullscreen:gap-x-12'>
          <div onClick={() => seek(-10)}
            className='flex h-16 w-16 media-fullscreen:h-32 media-fullscreen:w-32 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus:ring-4'>
            <SeekBackward10Icon className='h-10 w-10 media-fullscreen:h-20 media-fullscreen:w-20' />
          </div>
          <PlayButton className='flex h-20 w-20 media-fullscreen:h-36 media-fullscreen:w-36 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus-visible:ring-4 outline-none'>
            <PlayIcon className='h-12 w-12 media-fullscreen:h-24 media-fullscreen:w-24 hidden media-paused:block' />
            <PauseIcon className='h-12 w-12 media-fullscreen:h-24 media-fullscreen:w-24 hidden media-playing:block' />
          </PlayButton>
          <div onClick={() => seek(10)}
            className='flex h-16 w-16 media-fullscreen:h-32 media-fullscreen:w-32 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus:ring-4'>
            <SeekForward10Icon className='h-10 w-10 media-fullscreen:h-20 media-fullscreen:w-20' />
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
              <Controls.Group className='flex items-center'>
                <button
                  onClick={toggleFullscreen}
                  className='flex h-10 w-10 items-center justify-center rounded-full text-white ring-white/50 transition-all hover:bg-white/10 focus:ring-4'
                >
                  <FullscreenExitIcon className='w-7 h-7 hidden media-fullscreen:block' />
                  <FullscreenIcon className='w-7 h-7 media-fullscreen:hidden' />
                </button>
              </Controls.Group>
            </div>
          </div>
        </Controls.Group>
      </div>
    </MediaPlayer>
  );
};

export default DemoVideoPlayer;
