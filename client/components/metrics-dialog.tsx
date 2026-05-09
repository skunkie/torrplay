// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { VisuallyHidden } from '@radix-ui/react-visually-hidden';
import { Loader2 } from 'lucide-react';
import useSWR from 'swr';

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { getMemoryStats } from '@/lib/api/stats';

import { MetricsDialogLayout } from './metrics-dialog-layout';

interface MetricsDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void
}

export function MetricsDialog({ open, onOpenChange }: MetricsDialogProps) {
  const { data: memoryStats } = useSWR(open ? '/api/stats/memory' : null, () => getMemoryStats(), {
    refreshInterval: 1000,
  });

  if (!open) return null;

  if (!memoryStats) {
    return (
      <Dialog open={open}
        onOpenChange={onOpenChange}>
        <DialogContent>
          <VisuallyHidden>
            <DialogTitle>Loading...</DialogTitle>
          </VisuallyHidden>
          <div className='flex items-center justify-center py-8'>
            <Loader2 className='h-8 w-8 animate-spin text-muted-foreground' />
          </div>
        </DialogContent>
      </Dialog>
    );
  }

  return <MetricsDialogLayout open={open}
    onOpenChange={onOpenChange}
    memoryStats={memoryStats} />;
}
