// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { VisuallyHidden } from '@radix-ui/react-visually-hidden';
import { Loader2 } from 'lucide-react';
import useSWR from 'swr';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { getSystemInfo } from '@/lib/api/system';

import { SystemInfoDialogLayout } from './system-info-dialog-layout';

interface SystemInfoDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void
}

export function SystemInfoDialog({ open, onOpenChange }: SystemInfoDialogProps) {
  const { data: systemInfo } = useSWR(open ? '/api/system/info' : null, () => getSystemInfo());

  if (!open) return null;

  if (!systemInfo) {
    return (
      <Dialog open={open}
        onOpenChange={onOpenChange}>
        <DialogContent>
          <VisuallyHidden>
            <DialogTitle>Loading...</DialogTitle>
            <DialogDescription>Loading system information</DialogDescription>
          </VisuallyHidden>
          <div className='flex items-center justify-center py-8'>
            <Loader2 className='h-8 w-8 animate-spin text-muted-foreground' />
          </div>
        </DialogContent>
      </Dialog>
    );
  }

  return <SystemInfoDialogLayout open={open}
    onOpenChange={onOpenChange}
    systemInfo={systemInfo} />;
}
