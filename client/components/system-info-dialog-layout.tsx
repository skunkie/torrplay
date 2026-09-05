// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { Calendar, Check, Clock, GitCommit, Info, Loader2, Server } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { UpdateStatus } from '@/lib/app-update-context';
import { formatUptime } from '@/lib/format-utils';
import { SystemInfo } from '@/lib/types/api';

export interface SystemInfoDialogLayoutProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  systemInfo: SystemInfo | null,
  updateStatus?: UpdateStatus,
  latestVersion?: string | null,
  onCheckForUpdates?: () => void,
  onViewUpdate?: () => void
}

export function SystemInfoDialogLayout({
  open,
  onOpenChange,
  systemInfo,
  updateStatus,
  latestVersion,
  onCheckForUpdates,
  onViewUpdate,
}: SystemInfoDialogLayoutProps) {
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
                  <div className='flex items-center justify-between gap-1'>
                    <p className='text-xs text-muted-foreground'>Version</p>
                    {updateStatus === 'available' && (
                      <span className='inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-primary/15 text-primary'>
                        Update available
                      </span>
                    )}
                  </div>
                  <p className='text-lg font-semibold text-foreground truncate'>
                    {systemInfo?.version}
                  </p>
                  {onCheckForUpdates && (
                    <div className='mt-2 flex items-center gap-2'>
                      {updateStatus === 'checking' ? (
                        <span className='text-xs text-muted-foreground flex items-center gap-1.5'>
                          <Loader2 className='h-3.5 w-3.5 animate-spin text-primary' />
                          Checking...
                        </span>
                      ) : updateStatus === 'available' ? (
                        <Button
                          size='sm'
                          variant='default'
                          onClick={onViewUpdate}
                          className='h-6 text-xs px-2'
                        >
                          View Update ({latestVersion ? `v${latestVersion.replace(/^v/i, '')}` : 'New'})
                        </Button>
                      ) : updateStatus === 'up-to-date' ? (
                        <div className='flex items-center gap-2'>
                          <span className='text-xs text-muted-foreground flex items-center gap-1'>
                            <Check className='h-3 w-3 text-green-500' />
                            Up to date
                          </span>
                          <Button
                            size='sm'
                            variant='ghost'
                            onClick={onCheckForUpdates}
                            className='h-6 text-[11px] px-1.5 text-muted-foreground hover:text-foreground'
                          >
                            Check again
                          </Button>
                        </div>
                      ) : updateStatus === 'error' ? (
                        <div className='flex items-center gap-1.5'>
                          <span className='text-xs text-destructive'>Failed to check</span>
                          <Button
                            size='sm'
                            variant='ghost'
                            onClick={onCheckForUpdates}
                            className='h-6 text-[11px] px-1.5 text-muted-foreground hover:text-foreground'
                          >
                            Retry
                          </Button>
                        </div>
                      ) : (
                        <Button
                          size='sm'
                          variant='outline'
                          onClick={onCheckForUpdates}
                          className='h-6 text-xs px-2'
                        >
                          Check for updates
                        </Button>
                      )}
                    </div>
                  )}
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
