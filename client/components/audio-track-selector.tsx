// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { Check, Music } from 'lucide-react';
import React, { useEffect, useState } from 'react';

import { type AudioTrackInfo } from '@/lib/mkv-audio';

interface AudioTrackSelectorProps {
  tracks: AudioTrackInfo[],
  selectedTrackIndex: number,
  onSelectTrack: (index: number) => void
}

export const AudioTrackSelector: React.FC<AudioTrackSelectorProps> = ({
  tracks,
  selectedTrackIndex,
  onSelectTrack,
}) => {
  const [isOpen, setIsOpen] = useState(false);

  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        setIsOpen(false);
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [isOpen]);

  if (!tracks || tracks.length === 0) return null;

  return (
    <div className='relative inline-block'>
      <button
        type='button'
        onClick={e => {
          e.stopPropagation();
          setIsOpen(!isOpen);
        }}
        aria-label='Select audio track'
        title='Select audio track'
        aria-haspopup='menu'
        aria-expanded={isOpen}
        className='flex h-10 w-10 items-center justify-center rounded-full text-white ring-white/50 transition-all hover:bg-white/10 focus:ring-4 outline-none'
      >
        <Music className='w-5 h-5' />
        <span className='sr-only'>Select audio track</span>
      </button>

      {isOpen && (
        <>
          <div
            className='fixed inset-0 z-40'
            onClick={e => {
              e.stopPropagation();
              setIsOpen(false);
            }}
            aria-hidden='true'
          />
          <div
            role='menu'
            aria-label='Audio Tracks'
            className='absolute right-0 bottom-10 sm:bottom-12 z-50 w-[min(220px,calc(100vw-1rem))] rounded-lg border border-white/20 bg-black/90 p-1.5 text-xs text-white shadow-2xl backdrop-blur-md animate-in fade-in-0 zoom-in-95'
            onClick={e => e.stopPropagation()}
          >
            <div className='px-2.5 py-1.5 font-semibold text-white/70 uppercase tracking-wider text-[10px] border-b border-white/10 mb-1'>
              Audio Tracks ({tracks.length})
            </div>
            <div className='flex flex-col gap-0.5 max-h-48 overflow-y-auto'>
              {tracks.map(track => {
                const isSelected = track.index === selectedTrackIndex;
                return (
                  <button
                    key={track.id}
                    type='button'
                    role='menuitemradio'
                    aria-checked={isSelected}
                    onClick={e => {
                      e.stopPropagation();
                      onSelectTrack(track.index);
                      setIsOpen(false);
                    }}
                    className={`flex w-full items-center justify-between gap-2 rounded px-2.5 py-2 text-left transition-colors hover:bg-white/20 ${
                      isSelected ? 'bg-white/15 text-primary-foreground font-medium' : 'text-white/90'
                    }`}
                  >
                    <div className='flex flex-col truncate'>
                      <span className='truncate'>{track.label}</span>
                      {!track.isNativelySupported && (
                        <span className='text-[10px] text-amber-400 font-normal'>
                          WASM Decoded ({track.codec.toUpperCase()})
                        </span>
                      )}
                    </div>
                    {isSelected && <Check className='h-4 w-4 shrink-0 text-white' />}
                  </button>
                );
              })}
            </div>
          </div>
        </>
      )}
    </div>
  );
};

export default AudioTrackSelector;
