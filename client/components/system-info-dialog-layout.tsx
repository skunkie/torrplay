// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { Calendar, Clock, GitCommit, Info, Server } from 'lucide-react';

import { Card } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { formatUptime } from '@/lib/format-utils';
import { SystemInfo } from '@/lib/types/api';

interface SystemInfoDialogLayoutProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  systemInfo: SystemInfo | null
}

export function SystemInfoDialogLayout({ open, onOpenChange, systemInfo }: SystemInfoDialogLayoutProps) {
  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[90vh] overflow-hidden flex flex-col'>
        <DialogHeader className='flex-shrink-0'>
          <DialogTitle>System Information</DialogTitle>
          <DialogDescription>Version, build date, commit, and uptime details</DialogDescription>
        </DialogHeader>

        <div tabIndex={0}
          className='space-y-4 py-4 overflow-y-auto flex-1 focus:outline-none focus-visible:ring-1 focus-visible:ring-ring rounded-md'>
          <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-primary/10 flex-shrink-0'>
                  <Info className='h-5 w-5 text-primary' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Version</p>
                  <p className='text-lg font-semibold text-foreground truncate'>
                    {systemInfo?.version}
                  </p>
                </div>
              </div>
            </Card>

            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-accent/10 flex-shrink-0'>
                  <Calendar className='h-5 w-5 text-accent' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Build Date</p>
                  <p className='text-lg font-semibold text-foreground'>
                    {systemInfo?.buildDate}
                  </p>
                </div>
              </div>
            </Card>

            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-chart-3/10 flex-shrink-0'>
                  <GitCommit className='h-5 w-5 text-chart-3' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Commit</p>
                  <p className='text-lg font-semibold text-foreground'>
                    {systemInfo?.commit}
                  </p>
                </div>
              </div>
            </Card>

            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-chart-4/10 flex-shrink-0'>
                  <Clock className='h-5 w-5 text-chart-4' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Uptime</p>
                  <p className='text-lg font-semibold text-foreground'>
                    {systemInfo?.uptime ? formatUptime(systemInfo.uptime) : 'N/A'}
                  </p>
                </div>
              </div>
            </Card>

            {systemInfo?.addresses && systemInfo.addresses.length > 0 && (
              <Card className='p-4 sm:col-span-2'>
                <div className='flex items-center gap-3'>
                  <div className='p-2 rounded-lg bg-chart-5/10 flex-shrink-0'>
                    <Server className='h-5 w-5 text-chart-5' />
                  </div>
                  <div className='flex-1 min-w-0'>
                    <p className='text-xs text-muted-foreground'>Addresses</p>
                    <ul className='list-none list-inside'>
                      {systemInfo.addresses.map((addr, index) => <li key={index}>{addr}</li>)}
                    </ul>
                  </div>
                </div>
              </Card>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
