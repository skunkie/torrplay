// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { VisuallyHidden } from '@radix-ui/react-visually-hidden';
import { Loader2 } from 'lucide-react';
import useSWR from 'swr';

import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { getMemoryStats } from '@/lib/api/stats';
import { getSystemMetrics } from '@/lib/api/system';

import { MetricsDialogLayout } from './metrics-dialog-layout';

interface MetricsDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void
}

export function MetricsDialog({ open, onOpenChange }: MetricsDialogProps) {
  const { data: memoryStats } = useSWR(open ? '/api/stats/memory' : null, () => getMemoryStats(), {
    refreshInterval: 1000,
  });

  const { data: systemMetrics } = useSWR(open ? '/api/system/metrics' : null, () => getSystemMetrics(), {
    refreshInterval: 1000,
  });

  if (!open) return null;

  if (!memoryStats || !systemMetrics) {
    return (
      <Dialog open={open}
        onOpenChange={onOpenChange}>
        <DialogContent>
          <VisuallyHidden>
            <DialogTitle>Loading...</DialogTitle>
            <DialogDescription>Loading system metrics</DialogDescription>
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
    memoryStats={memoryStats}
    systemMetrics={systemMetrics} />;
}
