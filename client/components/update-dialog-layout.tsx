// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import {
  ArrowRight,
  ChevronDown,
  ChevronUp,
  Download,
  ExternalLink,
  Sparkles,
} from 'lucide-react';
import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { ReleaseAssetInfo } from '@/lib/api/releases';
import { formatBytes } from '@/lib/format-utils';

export interface UpdateDialogLayoutProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  currentVersion: string | null,
  latestVersion: string | null,
  releaseTitle?: string | null,
  releaseBody?: string | null,
  releaseUrl?: string | null,
  publishedAt?: string | null,
  primaryAsset: ReleaseAssetInfo | null,
  secondaryAssets?: ReleaseAssetInfo[],
  onDownloadPrimary: () => void,
  onDownloadAsset: (url: string) => void,
  onDismiss: () => void,
  onDismissForever: () => void
}

export function UpdateDialogLayout({
  open,
  onOpenChange,
  currentVersion,
  latestVersion,
  releaseTitle,
  releaseBody,
  releaseUrl,
  publishedAt,
  primaryAsset,
  secondaryAssets = [],
  onDownloadPrimary,
  onDownloadAsset,
  onDismiss,
  onDismissForever,
}: UpdateDialogLayoutProps) {
  const [showOtherFormats, setShowOtherFormats] = useState(false);

  const formattedDate = publishedAt
    ? new Date(publishedAt).toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })
    : null;

  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent className='max-w-md sm:max-w-lg max-h-[90vh] overflow-hidden flex flex-col'>
        <DialogHeader className='flex-shrink-0'>
          <div className='flex items-center gap-2'>
            <div className='p-2 rounded-lg bg-primary/10 text-primary'>
              <Sparkles className='h-5 w-5' />
            </div>
            <div>
              <DialogTitle>Update Available</DialogTitle>
              <DialogDescription>
                A newer version of TorrPlay is available.
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div
          tabIndex={0}
          className='space-y-4 py-3 overflow-y-auto flex-1 focus:outline-none focus-visible:ring-1 focus-visible:ring-ring rounded-md pr-1'
        >
          {/* Version banner */}
          <Card className='p-3 bg-muted/40'>
            <div className='flex items-center justify-between'>
              <div className='flex items-center gap-2 text-sm'>
                <span className='text-muted-foreground font-mono'>
                  {currentVersion ? `v${currentVersion.replace(/^v/i, '')}` : 'Current'}
                </span>
                <ArrowRight className='h-4 w-4 text-muted-foreground' />
                <span className='font-semibold text-primary font-mono text-base'>
                  {latestVersion ? `v${latestVersion.replace(/^v/i, '')}` : 'Latest'}
                </span>
              </div>
              {formattedDate && (
                <span className='text-xs text-muted-foreground'>{formattedDate}</span>
              )}
            </div>
            {releaseTitle && (
              <p className='text-xs text-foreground/80 mt-1 font-medium truncate'>
                {releaseTitle}
              </p>
            )}
          </Card>

          {/* Primary Download Call-to-Action */}
          <div className='space-y-2'>
            {primaryAsset ? (
              <Button
                onClick={onDownloadPrimary}
                className='w-full py-5 text-base flex items-center justify-center gap-2 shadow'
              >
                <Download className='h-5 w-5' />
                <span>{primaryAsset.label}</span>
                {primaryAsset.size && (
                  <span className='text-xs opacity-75'>
                    ({formatBytes(primaryAsset.size)})
                  </span>
                )}
              </Button>
            ) : releaseUrl ? (
              <Button
                onClick={() => onDownloadAsset(releaseUrl)}
                className='w-full py-5 text-base flex items-center justify-center gap-2'
              >
                <ExternalLink className='h-5 w-5' />
                <span>View Release on GitHub</span>
              </Button>
            ) : null}
          </div>

          {/* Alternative formats (e.g. Windows MSI Service, AppImage, etc.) */}
          {secondaryAssets.length > 0 && (
            <div className='space-y-2'>
              <button
                type='button'
                onClick={() => setShowOtherFormats(prev => !prev)}
                className='text-xs font-medium text-muted-foreground hover:text-foreground flex items-center gap-1 transition-colors'
              >
                <span>Other download options ({secondaryAssets.length})</span>
                {showOtherFormats ? (
                  <ChevronUp className='h-3.5 w-3.5' />
                ) : (
                  <ChevronDown className='h-3.5 w-3.5' />
                )}
              </button>

              {showOtherFormats && (
                <div className='space-y-1.5 pt-1'>
                  {secondaryAssets.map(asset => (
                    <div
                      key={asset.name}
                      className='flex items-center justify-between p-2 rounded-md border border-border bg-card/60 text-xs'
                    >
                      <div className='min-w-0 pr-2'>
                        <p className='font-medium text-foreground truncate'>
                          {asset.label}
                        </p>
                        <p className='text-[11px] text-muted-foreground truncate'>
                          {asset.name}
                          {asset.size ? ` • ${formatBytes(asset.size)}` : ''}
                        </p>
                      </div>
                      <Button
                        size='sm'
                        variant='outline'
                        onClick={() => onDownloadAsset(asset.url)}
                        className='h-7 text-xs shrink-0 flex items-center gap-1'
                      >
                        <Download className='h-3.5 w-3.5' />
                        <span>Download</span>
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {/* Release Notes */}
          {releaseBody && (
            <div className='space-y-1.5'>
              <p className='text-xs font-semibold text-muted-foreground uppercase tracking-wider'>
                Release Notes
              </p>
              <div className='max-h-40 overflow-y-auto p-3 rounded-md bg-muted/30 border border-border/50 text-xs text-foreground/90 whitespace-pre-wrap font-sans leading-relaxed'>
                {releaseBody}
              </div>
            </div>
          )}
        </div>

        <DialogFooter className='flex-shrink-0 flex flex-col-reverse sm:flex-row sm:justify-between items-center gap-2 pt-2 border-t border-border/50'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={onDismissForever}
            className='text-xs text-muted-foreground hover:text-foreground w-full sm:w-auto'
          >
            Don&apos;t ask again
          </Button>
          <div className='flex items-center gap-2 w-full sm:w-auto justify-end'>
            {releaseUrl && (
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => onDownloadAsset(releaseUrl)}
                className='text-xs flex items-center gap-1'
              >
                <ExternalLink className='h-3.5 w-3.5' />
                <span>GitHub</span>
              </Button>
            )}
            <Button
              type='button'
              variant='secondary'
              size='sm'
              onClick={onDismiss}
              className='text-xs'
            >
              Remind me later
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
