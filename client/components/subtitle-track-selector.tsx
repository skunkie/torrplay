// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { Check, Subtitles } from 'lucide-react';
import React, { useEffect, useState } from 'react';

import { type SubtitleTrackInfo } from '@/lib/video-utils';

interface SubtitleTrackSelectorProps {
  tracks: SubtitleTrackInfo[],
  selectedTrackId: string | null,
  onSelectTrack: (trackId: string | null) => void
}

export const SubtitleTrackSelector: React.FC<SubtitleTrackSelectorProps> = ({
  tracks,
  selectedTrackId,
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

  const isOffSelected = selectedTrackId === null;

  return (
    <div className='relative inline-block'>
      <button
        type='button'
        onClick={e => {
          e.stopPropagation();
          setIsOpen(!isOpen);
        }}
        aria-label='Select subtitle track'
        title='Select subtitle track'
        aria-haspopup='menu'
        aria-expanded={isOpen}
        className={`flex h-10 w-10 items-center justify-center rounded-full ring-white/50 transition-all hover:bg-white/10 focus:ring-4 outline-none ${
          !isOffSelected ? 'text-primary-foreground bg-white/20' : 'text-white'
        }`}
      >
        <Subtitles className='w-5 h-5' />
        <span className='sr-only'>Select subtitle track</span>
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
            aria-label='Subtitles'
            className='absolute right-0 bottom-10 sm:bottom-12 z-50 w-[min(220px,calc(100vw-1rem))] rounded-lg border border-white/20 bg-black/90 p-1.5 text-xs text-white shadow-2xl backdrop-blur-md animate-in fade-in-0 zoom-in-95'
            onClick={e => e.stopPropagation()}
          >
            <div className='px-2.5 py-1.5 font-semibold text-white/70 uppercase tracking-wider text-[10px] border-b border-white/10 mb-1'>
              Subtitles ({tracks.length})
            </div>
            <div className='flex flex-col gap-0.5 max-h-48 overflow-y-auto'>
              <button
                type='button'
                role='menuitemradio'
                aria-checked={isOffSelected}
                onClick={e => {
                  e.stopPropagation();
                  onSelectTrack(null);
                  setIsOpen(false);
                }}
                className={`flex w-full items-center justify-between gap-2 rounded px-2.5 py-2 text-left transition-colors hover:bg-white/20 ${
                  isOffSelected ? 'bg-white/15 text-primary-foreground font-medium' : 'text-white/90'
                }`}
              >
                <span>Off</span>
                {isOffSelected && <Check className='h-4 w-4 shrink-0 text-white' />}
              </button>

              {tracks.map(track => {
                const isSelected = track.id === selectedTrackId;
                return (
                  <button
                    key={track.id}
                    type='button'
                    role='menuitemradio'
                    aria-checked={isSelected}
                    disabled={!!track.unavailableReason}
                    title={track.unavailableReason}
                    onClick={e => {
                      e.stopPropagation();
                      onSelectTrack(track.id);
                      setIsOpen(false);
                    }}
                    className={`flex w-full items-center justify-between gap-2 rounded px-2.5 py-2 text-left transition-colors hover:bg-white/20 disabled:cursor-not-allowed disabled:opacity-60 ${
                      isSelected ? 'bg-white/15 text-primary-foreground font-medium' : 'text-white/90'
                    }`}
                  >
                    <div className='flex flex-col truncate'>
                      <span className='truncate'>{track.label}</span>
                      {track.unavailableReason ? (
                        <span className='max-w-64 whitespace-normal text-[10px] text-white/70 font-normal'>
                          {track.unavailableReason}
                        </span>
                      ) : track.type && (
                        <span className='text-[10px] text-white/50 font-normal uppercase'>
                          {track.type}
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

export default SubtitleTrackSelector;
