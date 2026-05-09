// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { Plus } from 'lucide-react';
import { useRef } from 'react';

import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import type { Torrent } from '@/lib/types/api';

interface TorrentControlsProps {
  torrentsData?: { torrents: Torrent[] },
  torrents: string[],
  filteredAndSortedTorrents: Torrent[],
  usePagination?: boolean,
  torrentsPerPage: number,
  onTorrentsPerPageChange?: (value: string) => void,
  categoryFilter: string,
  onCategoryFilterChange: (value: string) => void,
  sortBy: string,
  onSortByChange: (value: string) => void,
  onAddTorrent: () => void
}

export function TorrentControls({
  torrentsData,
  torrents,
  filteredAndSortedTorrents,
  usePagination = false,
  torrentsPerPage,
  onTorrentsPerPageChange,
  categoryFilter,
  onCategoryFilterChange,
  sortBy,
  onSortByChange,
  onAddTorrent,
}: TorrentControlsProps) {
  const mobileControlsRef = useRef<HTMLDivElement>(null);
  const topControlsRef = useRef<HTMLDivElement>(null);

  return (
    <div className='mb-3'>
      {/* Layout for large screens (md and up). */}
      <div ref={topControlsRef}
        className='hidden md:flex flex-wrap items-center justify-between gap-4'>
        <div className='flex flex-wrap items-center gap-4'>
          <Button onClick={onAddTorrent}
            className='gap-2'>
            <Plus className='h-4 w-4' />
            Add Torrent
          </Button>
          <Select value={categoryFilter}
            onValueChange={onCategoryFilterChange}>
            <SelectTrigger id='category-select'
              className='w-full sm:w-[240px] xl:w-[300px] 3xl:w-[360px]'>
              <SelectValue placeholder='Filter by category...' />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>All Categories</SelectItem>
              {torrents.map(category => (
                <SelectItem key={category}
                  value={category}>
                  {category}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={sortBy}
            onValueChange={onSortByChange}>
            <SelectTrigger id='sort-select'
              className='w-full sm:w-[180px] xl:w-[240px] 3xl:w-[300px]'>
              <SelectValue placeholder='Sort by...' />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='date'>Date Added</SelectItem>
              <SelectItem value='updated'>Date Updated</SelectItem>
              <SelectItem value='name'>Name</SelectItem>
              <SelectItem value='size'>Size</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className='flex items-center gap-2'>
          {torrentsData && (
            <span className='text-sm text-muted-foreground'>
              {filteredAndSortedTorrents.length}{' '}
              {filteredAndSortedTorrents.length === 1 ? 'torrent' : 'torrents'}
            </span>
          )}
          {usePagination && (
            <Select value={String(torrentsPerPage)}
              onValueChange={onTorrentsPerPageChange}>
              <SelectTrigger className='w-[120px]'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='0'>All</SelectItem>
                <SelectItem value='12'>12 / page</SelectItem>
                <SelectItem value='24'>24 / page</SelectItem>
                <SelectItem value='48'>48 / page</SelectItem>
                <SelectItem value='96'>96 / page</SelectItem>
              </SelectContent>
            </Select>
          )}
        </div>
      </div>

      {/* Layout for small screens (less than md). */}
      <div ref={mobileControlsRef}
        className='md:hidden flex flex-col gap-4'>
        <div className='flex items-center justify-between'>
          <Button onClick={onAddTorrent}
            className='gap-2'>
            <Plus className='h-4 w-4' />
            Add Torrent
          </Button>
          {torrentsData && (
            <span className='text-sm text-muted-foreground'>
              {filteredAndSortedTorrents.length}{' '}
              {filteredAndSortedTorrents.length === 1 ? 'torrent' : 'torrents'}
            </span>
          )}
        </div>
        <div className='flex flex-wrap items-center gap-4'>
          <Select value={categoryFilter}
            onValueChange={onCategoryFilterChange}>
            <SelectTrigger id='category-select-mobile'
              className='w-full sm:w-[240px] xl:w-[300px] 3xl:w-[360px]'>
              <SelectValue placeholder='Filter by category...' />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='all'>All Categories</SelectItem>
              {torrents.map(category => (
                <SelectItem key={category}
                  value={category}>
                  {category}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={sortBy}
            onValueChange={onSortByChange}>
            <SelectTrigger id='sort-select-mobile'
              className='w-full sm:w-[180px] xl:w-[240px] 3xl:w-[300px]'>
              <SelectValue placeholder='Sort by...' />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value='date'>Date Added</SelectItem>
              <SelectItem value='updated'>Date Updated</SelectItem>
              <SelectItem value='name'>Name</SelectItem>
              <SelectItem value='size'>Size</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
    </div>
  );
}
