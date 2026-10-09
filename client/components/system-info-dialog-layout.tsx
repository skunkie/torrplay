// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { Calendar, Check, Clock, Cpu, GitCommit, Info, Loader2, Monitor, Server } from 'lucide-react';
import type { ReactNode } from 'react';

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
  onViewUpdate?: () => void,
  additionalContent?: ReactNode
}

const operatingSystemLabels: Record<SystemInfo['os'], string> = {
  macos: 'macOS',
  windows: 'Windows',
  linux: 'Linux',
  android: 'Android',
  ios: 'iOS',
  unknown: 'Unknown',
};

export function SystemInfoDialogLayout({
  open,
  onOpenChange,
  systemInfo,
  updateStatus,
  latestVersion,
  onCheckForUpdates,
  onViewUpdate,
  additionalContent,
}: SystemInfoDialogLayoutProps) {
  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[90vh] overflow-hidden flex flex-col'>
        <DialogHeader className='flex-shrink-0'>
          <DialogTitle>System Information</DialogTitle>
          <DialogDescription>Version, deployment, platform, build, and uptime details</DialogDescription>
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
                  <div className='flex min-h-5 items-center justify-between gap-1'>
                    <p className='shrink-0 text-xs text-muted-foreground'>Version</p>
                    {updateStatus === 'up-to-date' ? (
                      <span className='inline-flex items-center gap-1 text-xs text-muted-foreground'>
                        <Check className='h-3 w-3 text-green-500' />
                        Up to date
                      </span>
                    ) : updateStatus === 'checking' ? (
                      <span className='inline-flex items-center gap-1 text-xs text-muted-foreground'>
                        <Loader2 className='h-3 w-3 animate-spin text-primary' />
                        Checking...
                      </span>
                    ) : updateStatus === 'error' ? (
                      <span className='text-xs text-destructive'>Failed to check</span>
                    ) : null}
                  </div>
                  <div className='flex items-center justify-between gap-2 min-w-0'>
                    <p className='text-lg font-semibold text-foreground truncate min-w-0'>
                      {systemInfo?.version}
                    </p>
                    {onCheckForUpdates && updateStatus !== 'checking' && (
                      updateStatus === 'available' ? (
                        <Button
                          size='sm'
                          variant='default'
                          onClick={onViewUpdate}
                          className='h-6 text-[11px] px-1.5 flex-shrink-0 ml-auto'
                        >
                          {latestVersion
                            ? `v${latestVersion.replace(/^v/i, '')} available`
                            : 'Update available'}
                        </Button>
                      ) : updateStatus === 'up-to-date' ? (
                        <Button
                          size='sm'
                          variant='ghost'
                          onClick={onCheckForUpdates}
                          className='h-6 text-[11px] px-1.5 text-muted-foreground hover:text-foreground flex-shrink-0 ml-auto'
                        >
                          Check again
                        </Button>
                      ) : updateStatus === 'error' ? (
                        <Button
                          size='sm'
                          variant='ghost'
                          onClick={onCheckForUpdates}
                          className='h-6 text-[11px] px-1.5 text-muted-foreground hover:text-foreground flex-shrink-0 ml-auto'
                        >
                          Retry
                        </Button>
                      ) : (
                        <Button
                          size='sm'
                          variant='ghost'
                          onClick={onCheckForUpdates}
                          className='h-6 text-[11px] px-1.5 text-muted-foreground hover:text-foreground flex-shrink-0 ml-auto'
                        >
                          Check for updates
                        </Button>
                      )
                    )}
                  </div>
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

            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-chart-1/10 flex-shrink-0'>
                  <Monitor className='h-5 w-5 text-chart-1' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Operating System</p>
                  <p className='text-lg font-semibold text-foreground'>
                    {systemInfo ? operatingSystemLabels[systemInfo.os] : 'Unknown'}
                  </p>
                </div>
              </div>
            </Card>

            <Card className='p-4'>
              <div className='flex items-center gap-3'>
                <div className='p-2 rounded-lg bg-chart-2/10 flex-shrink-0'>
                  <Cpu className='h-5 w-5 text-chart-2' />
                </div>
                <div className='flex-1 min-w-0'>
                  <p className='text-xs text-muted-foreground'>Architecture</p>
                  <p className='text-lg font-semibold text-foreground'>
                    {systemInfo?.architecture || 'Unknown'}
                  </p>
                </div>
              </div>
            </Card>

            <Card className='p-4 sm:col-span-2'>
              <div className='flex items-start gap-3'>
                <div className='p-2 rounded-lg bg-chart-5/10 flex-shrink-0'>
                  <Server className='h-5 w-5 text-chart-5' />
                </div>
                <div className='grid flex-1 min-w-0 grid-cols-1 gap-3 sm:grid-cols-[minmax(7rem,1fr)_minmax(0,2fr)]'>
                  <div className='min-w-0'>
                    <p className='text-xs text-muted-foreground'>Deployment</p>
                    <p className='text-lg font-semibold text-foreground'>
                      {systemInfo?.deployment === 'container' ? 'Container' : 'Native'}
                    </p>
                  </div>
                  <div className='min-w-0'>
                    <p className='text-xs text-muted-foreground'>Addresses</p>
                    {systemInfo?.addresses && systemInfo.addresses.length > 0 ? (
                      <ul
                        aria-label='Server addresses'
                        tabIndex={0}
                        className='list-none max-h-[4.5rem] overflow-auto overscroll-contain pr-1 font-mono text-xs leading-6'
                      >
                        {systemInfo.addresses.map((addr, index) => (
                          <li key={index}
                            className='w-max min-w-full whitespace-nowrap'>{addr}</li>
                        ))}
                      </ul>
                    ) : (
                      <p className='text-sm text-muted-foreground'>None reported</p>
                    )}
                  </div>
                </div>
              </div>
            </Card>
          </div>
          {additionalContent}
        </div>
      </DialogContent>
    </Dialog>
  );
}
