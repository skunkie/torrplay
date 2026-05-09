// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { BarChart3, Edit, ImageOff, Play, Plus, Trash2 } from 'lucide-react';
import { forwardRef, KeyboardEvent, useEffect, useRef, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import SafeImage from '@/components/ui/safe-image';
import { VIDEO_EXTENSIONS } from '@/lib/constants';
import { formatRelativeTime } from '@/lib/format-utils';
import type { Torrent } from '@/lib/types/api';

interface TorrentCardProps extends React.HTMLAttributes<HTMLDivElement> {
  torrent: Torrent,
  onEdit: (torrent: Torrent) => void,
  onViewStats: (torrent: Torrent) => void,
  onDelete: (torrent: Torrent) => void,
  onPlayTorrent: (torrent: Torrent) => void,
  onAddToDatabase: (torrent: Torrent) => void
}

export const TorrentCard = forwardRef<HTMLDivElement, TorrentCardProps>(
  ({ torrent, onEdit, onViewStats, onDelete, onPlayTorrent, onAddToDatabase, ...props }, ref) => {
    const displayDate = torrent.updatedAt || torrent.createdAt;
    const hasVideoFiles = torrent.files.some(file =>
      VIDEO_EXTENSIONS.some(ext => file.name.toLowerCase().endsWith(ext)),
    );

    const [isNavigating, setIsNavigating] = useState(false);
    const justOpenedDialog = useRef(false);
    const lastFocusedButtonRef = useRef<HTMLButtonElement | null>(null);
    const cardRef = useRef<HTMLDivElement>(null);
    const posterRef = useRef<HTMLDivElement>(null);
    const statsButtonRef = useRef<HTMLButtonElement>(null);
    const editOrAddButtonRef = useRef<HTMLButtonElement>(null);
    const deleteButtonRef = useRef<HTMLButtonElement>(null);

    const isNavigatingRef = useRef(isNavigating);
    isNavigatingRef.current = isNavigating;

    useEffect(() => {
      if (typeof ref === 'function') {
        ref(cardRef.current);
      } else if (ref) {
        ref.current = cardRef.current;
      }
    }, [ref]);

    useEffect(() => {
      if (isNavigating) {
        posterRef.current?.focus();
      }
    }, [isNavigating]);

    useEffect(() => {
      const handleGlobalKeyDown = (e: globalThis.KeyboardEvent) => {
        if (!isNavigatingRef.current || e.key !== 'Escape') {
          return;
        }

        e.preventDefault();
        e.stopPropagation();

        if (justOpenedDialog.current) {
          justOpenedDialog.current = false;
          lastFocusedButtonRef.current?.focus();
        } else {
          setIsNavigating(false);
          cardRef.current?.focus();
        }
      };

      document.addEventListener('keydown', handleGlobalKeyDown, true);
      return () => {
        document.removeEventListener('keydown', handleGlobalKeyDown, true);
      };
    }, []);

    const handleKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
      if (e.key === 'Enter' && !isNavigating) {
        e.preventDefault();
        setIsNavigating(true);
      }

      if (!isNavigating || e.key === 'Escape') {
        return;
      }

      const currentElement = e.target as HTMLElement;
      if (e.key === 'ArrowDown' && currentElement === posterRef.current) {
        e.preventDefault();
        statsButtonRef.current?.focus();
      } else if (e.key === 'ArrowUp') {
        const isButton = [statsButtonRef, editOrAddButtonRef, deleteButtonRef].some(
          buttonRef => buttonRef.current === currentElement,
        );
        if (isButton) {
          e.preventDefault();
          posterRef.current?.focus();
        }
      } else if (e.key === 'ArrowRight') {
        if (currentElement === statsButtonRef.current) {
          e.preventDefault();
          editOrAddButtonRef.current?.focus();
        } else if (currentElement === editOrAddButtonRef.current) {
          e.preventDefault();
          deleteButtonRef.current?.focus();
        }
      } else if (e.key === 'ArrowLeft') {
        if (currentElement === deleteButtonRef.current) {
          e.preventDefault();
          editOrAddButtonRef.current?.focus();
        } else if (currentElement === editOrAddButtonRef.current) {
          e.preventDefault();
          statsButtonRef.current?.focus();
        }
      }
    };

    return (
      <Card
        {...props}
        ref={cardRef}
        onKeyDown={handleKeyDown}
        data-nav-inside={isNavigating}
        className='group hover:border-primary/50 transition-colors flex flex-col focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2'
        tabIndex={isNavigating ? -1 : 0}
      >
        <div
          ref={posterRef}
          role='button'
          aria-label={`Play torrent ${torrent.title || torrent.name}`}
          className='relative w-full pt-[143%] bg-muted cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 rounded-t-lg'
          onClick={() => {
            if (hasVideoFiles) {
              onPlayTorrent(torrent);
            }
          }}
          onKeyDown={e => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault();
              if (hasVideoFiles) {
                onPlayTorrent(torrent);
              }
            }
          }}
          tabIndex={isNavigating ? 0 : -1}
        >
          <div className='absolute inset-0 overflow-hidden rounded-t-lg'>
            {torrent.poster ? (
              <SafeImage fill
                src={torrent.poster}
                alt={torrent.title || 'Torrent'}
                className='object-cover'
                priority />
            ) : (
              <div className='w-full h-full flex items-center justify-center'
                data-testid='no-poster-placeholder'>
                <ImageOff className='w-16 h-16 text-muted-foreground' />
              </div>
            )}
            {hasVideoFiles && (
              <div
                data-testid='play-icon-overlay'
                className='absolute inset-0 bg-black/20 flex items-center justify-center opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 transition-opacity'
              >
                <Play className='h-8 w-8 text-white' />
              </div>
            )}
          </div>
        </div>

        <div className='p-2 flex flex-col flex-grow'>
          <h3
            className='font-medium line-clamp-2 leading-snug flex-grow break-words text-xs 4xl:text-sm'
            title={torrent.title || torrent.name}
          >
            {torrent.title || torrent.name}
          </h3>

          <div className='flex items-center justify-between text-xs text-muted-foreground'>
            <div>{torrent.category && <span>{torrent.category}</span>}</div>
            <div>
              {displayDate && <span className='hidden md:inline'>{formatRelativeTime(displayDate, false)}</span>}
            </div>
          </div>

          <div className='mt-auto pt-3 grid grid-cols-3 gap-3'>
            <Button
              ref={statsButtonRef}
              variant='secondary'
              size='sm'
              onClick={e => {
                lastFocusedButtonRef.current = e.currentTarget;
                onViewStats(torrent);
                justOpenedDialog.current = true;
              }}
              tabIndex={isNavigating ? 0 : -1}
              className='group hover:bg-blue-500 hover:text-white dark:hover:bg-blue-600 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2'
            >
              <BarChart3 className='h-4 w-4 group-hover:text-white' />
              <span className='hidden 5xl:inline'>Stats</span>
            </Button>
            {torrent.createdAt ? (
              <Button
                ref={editOrAddButtonRef}
                variant='secondary'
                size='sm'
                onClick={e => {
                  lastFocusedButtonRef.current = e.currentTarget;
                  onEdit(torrent);
                  justOpenedDialog.current = true;
                }}
                tabIndex={isNavigating ? 0 : -1}
                className='group hover:bg-blue-500 hover:text-white dark:hover:bg-blue-600 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2'
              >
                <Edit className='h-4 w-4 group-hover:text-white' />
                <span className='hidden 5xl:inline'>Edit</span>
              </Button>
            ) : (
              <Button
                ref={editOrAddButtonRef}
                variant='secondary'
                size='sm'
                onClick={() => onAddToDatabase(torrent)}
                tabIndex={isNavigating ? 0 : -1}
                className='group hover:bg-green-500 hover:text-white dark:hover:bg-green-600 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2'
              >
                <Plus className='h-4 w-4 group-hover:text-white' />
                <span className='hidden 5xl:inline'>Add</span>
              </Button>
            )}
            <Button
              ref={deleteButtonRef}
              variant='secondary'
              size='sm'
              onClick={e => {
                lastFocusedButtonRef.current = e.currentTarget;
                onDelete(torrent);
                justOpenedDialog.current = true;
              }}
              tabIndex={isNavigating ? 0 : -1}
              className='group hover:bg-destructive hover:text-destructive-foreground transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2'
            >
              <Trash2 className='h-4 w-4 group-hover:text-destructive-foreground' />
              <span className='hidden 5xl:inline'>Delete</span>
            </Button>
          </div>
        </div>
      </Card>
    );
  },
);

TorrentCard.displayName = 'TorrentCard';
