// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useState } from 'react';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';

interface HandleTorrentDialogProps {
  open: boolean,
  type: 'magnet' | 'file',
  onOpenChange: (open: boolean) => void,
  onPlay: (remember: boolean) => void,
  onAddAndPlay: (remember: boolean) => void
}

export function HandleTorrentDialog({
  open,
  type,
  onOpenChange,
  onPlay,
  onAddAndPlay,
}: HandleTorrentDialogProps) {
  const [rememberChoice, setRememberChoice] = useState(false);

  const handlePlay = () => {
    onPlay(rememberChoice);
  };

  const handleAddAndPlay = () => {
    onAddAndPlay(rememberChoice);
  };

  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Torrent Detected</DialogTitle>
          <DialogDescription>
            How would you like to handle this {type === 'magnet' ? 'magnet link' : 'torrent file'}?
          </DialogDescription>
        </DialogHeader>

        <div className='flex items-center space-x-2 py-4'>
          <Switch
            id='remember-choice'
            checked={rememberChoice}
            onCheckedChange={setRememberChoice}
          />
          <Label htmlFor='remember-choice'>Remember my choice</Label>
        </div>

        <DialogFooter className='grid grid-cols-1 gap-2'>
          <Button className='w-full'
            onClick={handleAddAndPlay}>Add & Play</Button>
          <Button className='w-full'
            variant='outline'
            onClick={handlePlay}>
            Play Only
          </Button>
          <Button className='w-full'
            variant='ghost'
            onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
