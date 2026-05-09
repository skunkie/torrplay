// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { AlertTriangle } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import type { Torrent } from '@/lib/types/api';

interface DemoDeleteTorrentDialogProps {
  torrent: Torrent | null,
  open: boolean,
  onOpenChange: (open: boolean) => void,
  isDeleting: boolean,
  onDelete: () => void
}

export function DemoDeleteTorrentDialog({ torrent, open, onOpenChange, isDeleting, onDelete }: DemoDeleteTorrentDialogProps) {
  if (!torrent) return null;

  const handleDelete = () => {
    onDelete();
    onOpenChange(false);
  };

  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader className='text-center'>
          <DialogTitle className='flex items-center justify-center gap-2'>
            <AlertTriangle className='h-5 w-5 text-destructive' />
            Delete Torrent
          </DialogTitle>
          <DialogDescription>
          </DialogDescription>
        </DialogHeader>
        <div className='py-4 bg-muted/50 rounded-md'>
          {torrent && (
            <p className='text-center font-medium break-all px-4'>
              {torrent.title || torrent.name || torrent.hash}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={isDeleting}>
            Cancel
          </Button>
          <Button variant='destructive'
            onClick={handleDelete}
            disabled={isDeleting}>
            {isDeleting ? 'Deleting...' : 'Delete'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
