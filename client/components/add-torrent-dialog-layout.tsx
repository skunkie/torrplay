// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { RefObject } from 'react';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';

interface AddTorrentDialogLayoutProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  activeTab: string,
  setActiveTab: (value: string) => void,
  magnet: string,
  setMagnet: (value: string) => void,
  hash: string,
  setHash: (value: string) => void,
  file: File | null,
  setFile: (value: File | null) => void,
  title: string,
  setTitle: (value: string) => void,
  poster: string,
  setPoster: (value: string) => void,
  category: string,
  setCategory: (value: string) => void,
  useFileStorage: boolean,
  setUseFileStorage: (value: boolean) => void,
  showSuggestions: boolean,
  setShowSuggestions: (value: boolean) => void,
  categoryInputRef: RefObject<HTMLDivElement | null>,
  filteredCategories: string[],
  handleMagnetSubmit: () => void,
  handleHashSubmit: () => void,
  handleFileSubmit: () => void,
  loading: boolean
}

export function AddTorrentDialogLayout({
  open,
  onOpenChange,
  activeTab,
  setActiveTab,
  magnet,
  setMagnet,
  hash,
  setHash,
  file,
  setFile,
  title,
  setTitle,
  poster,
  setPoster,
  category,
  setCategory,
  useFileStorage,
  setUseFileStorage,
  showSuggestions,
  setShowSuggestions,
  categoryInputRef,
  filteredCategories,
  handleMagnetSubmit,
  handleHashSubmit,
  handleFileSubmit,
  loading,
}: AddTorrentDialogLayoutProps) {
  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add New Torrent</DialogTitle>
          <DialogDescription>
            Add a torrent using a magnet, info hash, or torrent file
          </DialogDescription>
        </DialogHeader>

        <Tabs value={activeTab}
          onValueChange={setActiveTab}
          className='w-full'>
          <TabsList className='grid w-full grid-cols-3'>
            <TabsTrigger value='magnet'>Magnet</TabsTrigger>
            <TabsTrigger value='hash'>Info Hash</TabsTrigger>
            <TabsTrigger value='file'>File</TabsTrigger>
          </TabsList>

          <TabsContent value='magnet'
            className='space-y-4 pt-4'>
            <div className='space-y-2'>
              <Label htmlFor='magnet'>Magnet</Label>
              <Input
                id='magnet'
                placeholder='magnet:?xt=urn:btih:...'
                value={magnet}
                onChange={e => setMagnet(e.target.value)}
              />
            </div>

            <div className='space-y-2'>
              <Label htmlFor='title-magnet'>Title (optional)</Label>
              <Input
                id='title-magnet'
                placeholder='My Torrent'
                value={title}
                onChange={e => setTitle(e.target.value)}
              />
            </div>

            <div className='space-y-2 relative'
              ref={categoryInputRef}>
              <Label htmlFor='category-magnet'>Category (optional)</Label>
              <Input
                id='category-magnet'
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
              <Label htmlFor='poster-magnet'>Poster URL (optional)</Label>
              <Input
                id='poster-magnet'
                placeholder='https://...'
                value={poster}
                onChange={e => setPoster(e.target.value)}
              />
            </div>

            <div className='flex items-center space-x-2'>
              <Switch
                id='use-file-storage-magnet'
                checked={useFileStorage}
                onCheckedChange={setUseFileStorage}
              />
              <Label htmlFor='use-file-storage-magnet'>Use File Storage</Label>
            </div>

            <Button
              onClick={handleMagnetSubmit}
              disabled={!magnet || loading}
              className='w-full'
            >
              {loading ? 'Adding...' : 'Add Torrent'}
            </Button>
          </TabsContent>

          <TabsContent value='hash'
            className='space-y-4 pt-4'>
            <div className='space-y-2'>
              <Label htmlFor='hash'>Info Hash</Label>
              <Input
                id='hash'
                placeholder='1234567890abcdef...'
                value={hash}
                onChange={e => setHash(e.target.value)}
              />
            </div>

            <div className='space-y-2'>
              <Label htmlFor='title-hash'>Title (optional)</Label>
              <Input
                id='title-hash'
                placeholder='My Torrent'
                value={title}
                onChange={e => setTitle(e.target.value)}
              />
            </div>

            <div className='space-y-2 relative'
              ref={categoryInputRef}>
              <Label htmlFor='category-hash'>Category (optional)</Label>
              <Input
                id='category-hash'
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
              <Label htmlFor='poster-hash'>Poster URL (optional)</Label>
              <Input
                id='poster-hash'
                placeholder='https://...'
                value={poster}
                onChange={e => setPoster(e.target.value)}
              />
            </div>

            <div className='flex items-center space-x-2'>
              <Switch
                id='use-file-storage-hash'
                checked={useFileStorage}
                onCheckedChange={setUseFileStorage}
              />
              <Label htmlFor='use-file-storage-hash'>Use File Storage</Label>
            </div>

            <Button
              onClick={handleHashSubmit}
              disabled={!hash || loading}
              className='w-full'
            >
              {loading ? 'Adding...' : 'Add Torrent'}
            </Button>
          </TabsContent>

          <TabsContent value='file'
            className='space-y-4 pt-4'>
            <div className='space-y-2'>
              <Label htmlFor='file'>Torrent File</Label>
              <Input
                id='file'
                type='file'
                onChange={e =>
                  setFile(e.target.files ? e.target.files[0] : null)
                }
                accept='.torrent'
              />
            </div>

            <div className='space-y-2'>
              <Label htmlFor='title-file'>Title (optional)</Label>
              <Input
                id='title-file'
                placeholder='My Torrent'
                value={title}
                onChange={e => setTitle(e.target.value)}
              />
            </div>

            <div className='space-y-2'>
              <Label htmlFor='poster-file'>Poster URL (optional)</Label>
              <Input
                id='poster-file'
                placeholder='https://...'
                value={poster}
                onChange={e => setPoster(e.target.value)}
              />
            </div>

            <div className='flex items-center space-x-2'>
              <Switch
                id='use-file-storage-file'
                checked={useFileStorage}
                onCheckedChange={setUseFileStorage}
              />
              <Label htmlFor='use-file-storage-file'>Use File Storage</Label>
            </div>

            <Button
              onClick={handleFileSubmit}
              disabled={!file || loading}
              className='w-full'
            >
              {loading ? 'Adding...' : 'Add Torrent'}
            </Button>
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}
