// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { Loader2, UploadCloud } from 'lucide-react';
import Image from 'next/image';
import React, { RefObject } from 'react';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import type { Torrent } from '@/lib/types/api';

interface EditTorrentDialogLayoutProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void,
  loading: boolean,
  title: string,
  setTitle: (value: string) => void,
  poster: string,
  setPoster: (value: string) => void,
  category: string,
  setCategory: (value: string) => void,
  useFileStorage: boolean,
  setUseFileStorage: (value: boolean) => void,
  dragOver: boolean,
  showSuggestions: boolean,
  setShowSuggestions: (value: boolean) => void,
  categoryInputRef: RefObject<HTMLDivElement | null>,
  filteredCategories: string[],
  handleSubmit: () => void,
  handleDragOver: (e: React.DragEvent<HTMLDivElement>) => void,
  handleDragEnter: (e: React.DragEvent<HTMLDivElement>) => void,
  handleDragLeave: (e: React.DragEvent<HTMLDivElement>) => void,
  handleDrop: (e: React.DragEvent<HTMLDivElement>) => void
}

export function EditTorrentDialogLayout({
  torrent,
  open,
  onOpenChange,
  loading,
  title,
  setTitle,
  poster,
  setPoster,
  category,
  setCategory,
  useFileStorage,
  setUseFileStorage,
  dragOver,
  showSuggestions,
  setShowSuggestions,
  categoryInputRef,
  filteredCategories,
  handleSubmit,
  handleDragOver,
  handleDragEnter,
  handleDragLeave,
  handleDrop,
}: EditTorrentDialogLayoutProps) {
  if (!torrent) return null;

  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit Torrent Metadata</DialogTitle>
          <DialogDescription className='break-all'>
            {torrent.title || 'Untitled'}
          </DialogDescription>
        </DialogHeader>

        <div className='space-y-4 py-4'>
          <div className='space-y-2'>
            <Label htmlFor='edit-title'>Title</Label>
            <Input
              id='edit-title'
              placeholder='Enter title'
              value={title}
              onChange={e => setTitle(e.target.value)}
            />
          </div>

          <div className='space-y-2 relative'
            ref={categoryInputRef}>
            <Label htmlFor='edit-category'>Category</Label>
            <Input
              id='edit-category'
              placeholder='Movies, Series, Cartoons...'
              value={category}
              onChange={e => setCategory(e.target.value)}
              onFocus={() => setShowSuggestions(true)}
            />
            {showSuggestions && filteredCategories.length > 0 && (
              <div className='absolute z-10 w-full bg-secondary rounded-md shadow-lg mt-1'>
                <ul className='max-h-60 overflow-auto rounded-md py-1 text-base ring-1 ring-black ring-opacity-5 focus:outline-none sm:text-sm'>
                  {filteredCategories.map(cat => (
                    <li
                      key={cat}
                      className='cursor-pointer select-none relative py-2 pl-3 pr-9 text-secondary-foreground hover:bg-primary hover:text-primary-foreground'
                      onClick={() => {
                        setCategory(cat);
                        setShowSuggestions(false);
                      }}
                    >
                      {cat}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>

          <div className='space-y-2'>
            <Label htmlFor='edit-poster'>Poster URL</Label>
            <Input
              id='edit-poster'
              placeholder='https://... or drop an image on the preview'
              value={poster}
              onChange={e => setPoster(e.target.value)}
            />
          </div>

          <div className='flex items-center space-x-2'>
            <Switch
              id='use-file-storage-edit'
              checked={useFileStorage}
              onCheckedChange={setUseFileStorage}
            />
            <Label htmlFor='use-file-storage-edit'>Use File Storage</Label>
          </div>

          <div
            className={`rounded-lg border-2 border-dashed p-2 text-center transition-colors ${
              dragOver ? 'border-primary bg-muted/50' : 'border-border'
            }`}
            onDragEnter={handleDragEnter}
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
          >
            <p className='text-xs text-muted-foreground mb-2'>Preview (drop image here):</p>
            {poster ? (
              <Image
                src={poster}
                alt='Poster preview'
                width={128}
                height={192}
                className='w-32 h-48 object-cover rounded-md bg-muted mx-auto'
              />
            ) : (
              <div className='w-32 h-48 flex flex-col items-center justify-center bg-muted rounded-md mx-auto'>
                <UploadCloud className='w-12 h-12 text-muted-foreground' />
                <p className='text-sm text-muted-foreground mt-2'>Drop image here</p>
              </div>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button variant='outline'
            onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={handleSubmit}
            disabled={loading}>
            {loading ? (
              <>
                <Loader2 className='h-4 w-4 mr-2 animate-spin' />
                Saving...
              </>
            ) : (
              'Save Changes'
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
