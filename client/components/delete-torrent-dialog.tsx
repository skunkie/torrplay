// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useState } from 'react';
import { toast } from 'sonner';

import { deleteTorrent } from '@/lib/api/torrents';

import { DeleteTorrentDialogLayout } from './delete-torrent-dialog-layout';

export interface DeleteTorrentDialogProps {
  torrent: { hash: string, title?: string, name?: string } | null,
  open: boolean,
  onOpenChange: (open: boolean) => void,
  onSuccess: () => void
}

export function DeleteTorrentDialog({
  torrent,
  open,
  onOpenChange,
  onSuccess,
}: DeleteTorrentDialogProps) {
  const [isDeleting, setIsDeleting] = useState(false);

  const torrentName = torrent?.title || torrent?.name;

  const handleDelete = async () => {
    if (!torrent) return;

    setIsDeleting(true);
    try {
      await deleteTorrent(torrent.hash);
      toast.success('Torrent deleted', {
        description: `Successfully deleted ${torrentName}`,
      });
      onSuccess();
      onOpenChange(false);
    } catch (error) {
      toast.error('Delete failed', {
        description: error instanceof Error ? error.message : 'Failed to delete torrent',
      });
    } finally {
      setIsDeleting(false);
    }
  };

  return (
    <DeleteTorrentDialogLayout
      open={open}
      onOpenChange={onOpenChange}
      torrent={torrent}
      isDeleting={isDeleting}
      onSubmit={handleDelete}
    />
  );
}
