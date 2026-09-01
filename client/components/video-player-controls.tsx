// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import '@vidstack/react/player/styles/base.css';
import '@vidstack/react/player/styles/default/captions.css';
import './video-captions.css';

import {
  Captions,
  Controls,
  type MediaPlayerInstance,
  MuteButton,
  PlayButton,
  Spinner,
  Time,
  TimeSlider,
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
import { type RefObject, useCallback, useEffect, useRef, useState } from 'react';

import { type AudioTrackInfo } from '@/lib/mkv-audio';
import { type SubtitleTrackInfo } from '@/lib/video-utils';

import { AudioTrackSelector } from './audio-track-selector';
import { SubtitleTrackSelector } from './subtitle-track-selector';

interface UseVideoPlayerControlsOptions {
  player: RefObject<MediaPlayerInstance | null>,
  subtitleTracks: SubtitleTrackInfo[],
  selectedSubtitleTrack: string | null,
  onSelectSubtitleTrack: (trackId: string | null) => void,
  enabled?: boolean
}

export function useVideoPlayerControls({
  player,
  subtitleTracks,
  selectedSubtitleTrack,
  onSelectSubtitleTrack,
  enabled = true,
}: UseVideoPlayerControlsOptions) {
  const [isFullscreen, setIsFullscreen] = useState(false);
  const selectedSubtitleTrackRef = useRef(selectedSubtitleTrack);
  selectedSubtitleTrackRef.current = selectedSubtitleTrack;

  const seek = useCallback((seconds: number) => {
    if (player.current) player.current.currentTime += seconds;
  }, [player]);

  const toggleFullscreen = useCallback(() => {
    if (!player.current) return;
    try {
      const operation = isFullscreen
        ? player.current.exitFullscreen()
        : player.current.enterFullscreen();
      void Promise.resolve(operation).catch(error => console.error('Fullscreen error:', error));
    } catch (error) {
      console.error('Fullscreen error:', error);
    }
  }, [isFullscreen, player]);

  useEffect(() => {
    if (!enabled) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      const activeElement = document.activeElement as HTMLElement | null;
      const isTextInput = activeElement?.matches('input, textarea, select, [contenteditable="true"]');
      if (isTextInput) return;
      const isButton = activeElement?.matches('button');

      if (event.key === ' ' || event.key === 'k' || event.key === 'K') {
        if (isButton) return;
        event.preventDefault();
        if (player.current?.paused) void player.current.play();
        else player.current?.pause();
      } else if (event.key === 'ArrowLeft') {
        if (!isButton && activeElement?.getAttribute('role') !== 'slider') {
          event.preventDefault();
          seek(-10);
        }
      } else if (event.key === 'ArrowRight') {
        if (!isButton && activeElement?.getAttribute('role') !== 'slider') {
          event.preventDefault();
          seek(10);
        }
      } else if (event.key === 'f' || event.key === 'F') {
        event.preventDefault();
        toggleFullscreen();
      } else if (event.key === 'm' || event.key === 'M') {
        event.preventDefault();
        if (player.current) player.current.muted = !player.current.muted;
      } else if (event.key === 'c' || event.key === 'C') {
        const availableTracks = subtitleTracks.filter(track => !track.unavailableReason);
        if (availableTracks.length === 0) return;
        event.preventDefault();
        onSelectSubtitleTrack(selectedSubtitleTrackRef.current === null ? availableTracks[0].id : null);
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [enabled, onSelectSubtitleTrack, player, seek, subtitleTracks, toggleFullscreen]);

  return { isFullscreen, setIsFullscreen, seek, toggleFullscreen };
}

interface VideoPlayerCaptionsProps {
  tracks: SubtitleTrackInfo[],
  selectedTrackId: string | null
}

export function VideoPlayerCaptions({ tracks, selectedTrackId }: VideoPlayerCaptionsProps) {
  return (
    <Captions
      className='vds-captions'
      data-embedded={tracks.some(track => track.id === selectedTrackId && track.embeddedTrackNumber !== undefined) || undefined}
    />
  );
}

interface VideoPlayerControlsProps {
  title?: string,
  onExit?: () => void,
  onSeek: (seconds: number) => void,
  isFullscreen: boolean,
  onToggleFullscreen: () => void,
  audioTracks: AudioTrackInfo[],
  selectedAudioTrack: number,
  onSelectAudioTrack: (trackIndex: number) => void,
  subtitleTracks: SubtitleTrackInfo[],
  selectedSubtitleTrack: string | null,
  onSelectSubtitleTrack: (trackId: string | null) => void
}

export function VideoPlayerControls({
  title,
  onExit,
  onSeek,
  isFullscreen,
  onToggleFullscreen,
  audioTracks,
  selectedAudioTrack,
  onSelectAudioTrack,
  subtitleTracks,
  selectedSubtitleTrack,
  onSelectSubtitleTrack,
}: VideoPlayerControlsProps) {
  return (
    <>
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
      <div className='absolute inset-0 z-10 w-full opacity-0 group-data-[controls]:opacity-100 transition-opacity'>
        {title && (
          <div className='absolute top-2 left-0 right-0 text-center px-4 py-2 bg-black/50 backdrop-blur-sm text-white font-medium truncate'>
            {title}
          </div>
        )}
        {onExit && (
          <button
            type='button'
            onClick={onExit}
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
            onClick={() => onSeek(-10)}
            aria-label='Seek backward 10 seconds'
            className='flex h-16 w-16 group-data-[fullscreen]:h-32 group-data-[fullscreen]:w-32 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus-visible:ring-4 outline-none'
          >
            <SeekBackward10Icon className='h-10 w-10 group-data-[fullscreen]:h-20 group-data-[fullscreen]:w-20' />
            <span className='sr-only'>Seek backward 10 seconds</span>
          </button>
          <PlayButton
            aria-label='Play or pause'
            className='flex h-20 w-20 group-data-[fullscreen]:h-36 group-data-[fullscreen]:w-36 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus-visible:ring-4 outline-none'
          >
            <PlayIcon className='h-12 w-12 group-data-[fullscreen]:h-24 group-data-[fullscreen]:w-24 hidden group-data-[paused]:block' />
            <PauseIcon className='h-12 w-12 group-data-[fullscreen]:h-24 group-data-[fullscreen]:w-24 hidden group-data-[playing]:block' />
          </PlayButton>
          <button
            type='button'
            onClick={() => onSeek(10)}
            aria-label='Seek forward 10 seconds'
            className='flex h-16 w-16 group-data-[fullscreen]:h-32 group-data-[fullscreen]:w-32 items-center justify-center rounded-full bg-white/50 text-white ring-white/50 transition-all hover:bg-primary/70 focus-visible:ring-4 outline-none'
          >
            <SeekForward10Icon className='h-10 w-10 group-data-[fullscreen]:h-20 group-data-[fullscreen]:w-20' />
            <span className='sr-only'>Seek forward 10 seconds</span>
          </button>
        </div>
        <div className='absolute inset-x-0 bottom-0 w-full h-2/5 bg-gradient-to-t from-black/50 to-transparent pointer-events-none' />
        <Controls.Group className='absolute bottom-3 left-0 right-0 flex flex-col items-center px-2 py-4'>
          <TimeSlider.Root
            aria-label='Seek'
            className='mx-2 media-slider group relative inline-flex h-10 w-full cursor-pointer select-none items-center outline-none'
          >
            <TimeSlider.Track className='relative ring-sky-400 z-0 h-2.5 w-full rounded-sm bg-white/20 group-data-[focus]:ring-[3px]'>
              <TimeSlider.TrackFill className='bg-white/70 absolute h-full w-[var(--slider-fill)] rounded-sm will-change-[width]' />
              <TimeSlider.Progress className='absolute z-10 h-full w-[var(--slider-progress)] rounded-sm bg-white/30 will-change-[width]' />
            </TimeSlider.Track>
            <TimeSlider.Thumb className='absolute left-[var(--slider-fill)] z-20 h-5 w-5 -translate-x-1/2 rounded-full border border-primary bg-white shadow-sm ring-white/40 will-change-[left] group-data-[active]:ring-4' />
          </TimeSlider.Root>
          <div className='w-full flex justify-between text-sm px-2 items-center'>
            <Time type='current' />
            <div className='flex items-center gap-x-2'>
              <MuteButton
                aria-label='Mute or unmute'
                className='flex h-10 w-10 items-center justify-center rounded-full text-white ring-white/50 transition-all hover:bg-white/10 focus:ring-4'
              >
                <MuteIcon className='w-7 h-7 hidden group-data-[muted]:block' />
                <VolumeLowIcon className='w-7 h-7 hidden group-data-[volume-low]:block' />
                <VolumeHighIcon className='w-7 h-7 group-data-[muted]:hidden group-data-[volume-low]:hidden' />
              </MuteButton>
              <VolumeSlider.Root
                aria-label='Volume'
                className='group relative mx-2 inline-flex h-10 w-24 max-w-[80px] cursor-pointer select-none items-center outline-none'
              >
                <VolumeSlider.Track className='relative ring-sky-400 z-0 h-2.5 w-full rounded-sm bg-white/20 group-data-[focus]:ring-[3px]'>
                  <VolumeSlider.TrackFill className='bg-white/70 absolute h-full w-[var(--slider-fill)] rounded-sm will-change-[width]' />
                </VolumeSlider.Track>
                <VolumeSlider.Thumb className='absolute left-[var(--slider-fill)] z-20 h-5 w-5 -translate-x-1/2 rounded-full border border-primary bg-white shadow-sm ring-white/40 will-change-[left] group-data-[active]:ring-4' />
              </VolumeSlider.Root>
              <Time type='duration' />
              <AudioTrackSelector
                tracks={audioTracks}
                selectedTrackIndex={selectedAudioTrack}
                onSelectTrack={onSelectAudioTrack}
              />
              <SubtitleTrackSelector
                tracks={subtitleTracks}
                selectedTrackId={selectedSubtitleTrack}
                onSelectTrack={onSelectSubtitleTrack}
              />
              <button
                type='button'
                onClick={onToggleFullscreen}
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
    </>
  );
}
