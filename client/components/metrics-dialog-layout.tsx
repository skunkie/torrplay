// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { Activity, ArrowDown, ArrowUp, Cloud, Database, HardDrive } from 'lucide-react';

import { Card } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { formatBytes } from '@/lib/format-utils';
import { MemoryStats, SystemMetrics } from '@/lib/types/api';

interface MetricsDialogLayoutProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  memoryStats: MemoryStats,
  systemMetrics: SystemMetrics
}

export function MetricsDialogLayout({ open, onOpenChange, memoryStats, systemMetrics }: MetricsDialogLayoutProps) {
  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[90vh] overflow-hidden flex flex-col'>
        <DialogHeader className='flex-shrink-0'>
          <DialogTitle>System Metrics</DialogTitle>
          <DialogDescription>Real-time system and torrent statistics</DialogDescription>
        </DialogHeader>

        <div tabIndex={0}
          className='space-y-4 py-4 overflow-y-auto flex-1 focus:outline-none focus-visible:ring-1 focus-visible:ring-ring rounded-md'>
          <div className='grid gap-3 sm:grid-cols-2'>
            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-primary/10 flex-shrink-0'>
                  <HardDrive className='h-5 w-5 text-primary' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Memory Usage</p>
                  <p className='text-lg font-semibold text-foreground truncate'>
                    {formatBytes(memoryStats.usedMemory || 0)}
                  </p>
                  <p className='text-xs text-muted-foreground truncate'>
                    of {formatBytes(memoryStats.maxMemory || 0)}
                  </p>
                </div>
              </div>
            </Card>

            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-accent/10 flex-shrink-0'>
                  <Database className='h-5 w-5 text-accent' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Streaming Torrents</p>
                  <p className='text-lg font-semibold text-foreground'>
                    {memoryStats.activeTorrents || 0}
                  </p>
                  <p className='text-xs text-muted-foreground'>in memory</p>
                </div>
              </div>
            </Card>

            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-chart-2/10 flex-shrink-0'>
                  <Cloud className='h-5 w-5 text-chart-2' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Loaded Torrents</p>
                  <p className='text-lg font-semibold text-foreground'>
                    {systemMetrics.activeTorrents || 0}
                  </p>
                  <p className='text-xs text-muted-foreground'>in client</p>
                </div>
              </div>
            </Card>

            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-chart-3/10 flex-shrink-0'>
                  <Activity className='h-5 w-5 text-chart-3' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Pieces in Memory</p>
                  <p className='text-lg font-semibold text-foreground'>
                    {(memoryStats.totalPieces || 0).toLocaleString()}
                  </p>
                  <p className='text-xs text-muted-foreground'>cached pieces</p>
                </div>
              </div>
            </Card>

            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-chart-4/10 flex-shrink-0'>
                  <ArrowDown className='h-5 w-5 text-chart-4' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Download Speed</p>
                  <p className='text-lg font-semibold text-foreground'>
                    {formatBytes(systemMetrics.downloadSpeed || 0)}/s
                  </p>
                  <p className='text-xs text-muted-foreground'>from active torrents</p>
                </div>
              </div>
            </Card>

            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-chart-5/10 flex-shrink-0'>
                  <ArrowUp className='h-5 w-5 text-chart-5' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Upload Speed</p>
                  <p className='text-lg font-semibold text-foreground'>
                    {formatBytes(systemMetrics.uploadSpeed || 0)}/s
                  </p>
                  <p className='text-xs text-muted-foreground'>to active torrents</p>
                </div>
              </div>
            </Card>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
