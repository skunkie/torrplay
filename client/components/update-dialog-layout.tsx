// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import {
  Apple,
  ChevronDown,
  ChevronUp,
  CircleArrowDown,
  Container,
  Copy,
  Download,
  ExternalLink,
  Monitor,
  Package,
} from 'lucide-react';
import { useState } from 'react';

import { ReleaseNotesMarkdown } from '@/components/release-notes-markdown';
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { ReleaseAssetInfo } from '@/lib/api/releases';
import type { Deployment } from '@/lib/app-update-context';
import { formatBytes } from '@/lib/format-utils';

export const DOCKER_UPDATE_GUIDE_URL = 'https://torrplay.github.io';
const COMPOSE_UPDATE_COMMAND = 'docker compose pull\ndocker compose up -d';

function dockerImageTag(version: string | null): string {
  return version ? `v${version.replace(/^v+/i, '')}` : 'latest';
}

function AssetPlatformIcon({ type }: { type: ReleaseAssetInfo['type'] }) {
  if (type === 'macos-dmg') {
    return <Apple className='h-5 w-5'
      aria-hidden='true' />;
  }
  if (type.startsWith('linux-')) {
    return <Package className='h-5 w-5'
      aria-hidden='true' />;
  }
  return <Monitor className='h-5 w-5'
    aria-hidden='true' />;
}

export interface UpdateDialogLayoutProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  deployment: Deployment,
  latestVersion: string | null,
  releaseBody?: string | null,
  releaseUrl?: string | null,
  publishedAt?: string | null,
  primaryAsset: ReleaseAssetInfo | null,
  secondaryAssets?: ReleaseAssetInfo[],
  onDownloadPrimary: () => void,
  onDownloadAsset: (url: string) => void,
  onCopy: (value: string, label: string) => void,
  onDismiss: () => void,
  onDismissForever: () => void
}

export function UpdateDialogLayout({
  open,
  onOpenChange,
  deployment,
  latestVersion,
  releaseBody,
  releaseUrl,
  publishedAt,
  primaryAsset,
  secondaryAssets = [],
  onDownloadPrimary,
  onDownloadAsset,
  onCopy,
  onDismiss,
  onDismissForever,
}: UpdateDialogLayoutProps) {
  const [showOtherFormats, setShowOtherFormats] = useState(false);

  const formattedLatestVersion = latestVersion
    ? `v${latestVersion.replace(/^v+/i, '')}`
    : null;
  const dockerPullCommand = `docker pull ghcr.io/torrplay/torrplay:${dockerImageTag(latestVersion)}`;
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
      <DialogContent className='max-w-md sm:max-w-lg max-h-[90vh] overflow-hidden flex flex-col p-0'>
        <DialogHeader className='flex-shrink-0 px-6 pt-6 pb-5 border-b'>
          <div className='flex items-start gap-3 text-left'>
            <div className='flex h-11 w-11 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground'>
              <CircleArrowDown className='h-6 w-6'
                aria-hidden='true' />
            </div>
            <div className='min-w-0 space-y-1'>
              <DialogTitle className='text-xl leading-tight'>
                {formattedLatestVersion
                  ? `TorrPlay ${formattedLatestVersion} is available`
                  : 'A TorrPlay update is available'}
              </DialogTitle>
              <DialogDescription className={formattedDate ? undefined : 'sr-only'}>
                {formattedDate ? `Released ${formattedDate}` : 'A TorrPlay update is available.'}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div
          tabIndex={0}
          className='space-y-4 overflow-y-auto flex-1 focus:outline-none focus-visible:ring-1 focus-visible:ring-ring px-6 py-5'
        >
          {deployment === 'container' ? (
            <div className='space-y-4'>
              <div className='flex gap-3 rounded-lg border p-4'>
                <Container className='mt-0.5 h-5 w-5 shrink-0 text-muted-foreground'
                  aria-hidden='true' />
                <p className='text-sm leading-relaxed text-muted-foreground'>
                  Your existing configuration and data remain in place while the running image is replaced.
                </p>
              </div>

              <DockerCommand
                title='Docker Compose'
                command={COMPOSE_UPDATE_COMMAND}
                copyLabel='Compose commands'
                recommended
                onCopy={onCopy}
              />
              <DockerCommand
                title='Direct Docker pull'
                command={dockerPullCommand}
                copyLabel='Docker pull command'
                onCopy={onCopy}
              />

              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() => onDownloadAsset(DOCKER_UPDATE_GUIDE_URL)}
                className='w-full gap-2'
              >
                <ExternalLink className='h-4 w-4'
                  aria-hidden='true' />
                Open documentation
              </Button>
            </div>
          ) : (
            <>
              {primaryAsset ? (
                <div className='space-y-4 rounded-lg border p-4'>
                  <div className='flex items-center gap-3'>
                    <div className='flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground'>
                      <AssetPlatformIcon type={primaryAsset.type} />
                    </div>
                    <div className='min-w-0'>
                      <p className='font-medium'>{primaryAsset.label}</p>
                      <p className='truncate text-xs text-muted-foreground'>
                        {primaryAsset.name}
                        {primaryAsset.size ? ` · ${formatBytes(primaryAsset.size)}` : ''}
                      </p>
                    </div>
                  </div>
                  <Button onClick={onDownloadPrimary}
                    className='w-full gap-2'>
                    <Download className='h-4 w-4'
                      aria-hidden='true' />
                    Download update
                  </Button>
                </div>
              ) : releaseUrl ? (
                <Button onClick={() => onDownloadAsset(releaseUrl)}
                  className='w-full gap-2'>
                  <ExternalLink className='h-4 w-4'
                    aria-hidden='true' />
                  View release on GitHub
                </Button>
              ) : null}

              {secondaryAssets.length > 0 && (
                <div className='overflow-hidden rounded-lg border'>
                  <button
                    type='button'
                    aria-expanded={showOtherFormats}
                    onClick={() => setShowOtherFormats(previous => !previous)}
                    className='flex w-full items-center gap-2 px-4 py-3 text-left text-sm font-medium hover:bg-muted/50 transition-colors'
                  >
                    <span>Other download formats</span>
                    <span className='ml-auto text-xs font-normal text-muted-foreground'>
                      {secondaryAssets.length} {secondaryAssets.length === 1 ? 'option' : 'options'}
                    </span>
                    {showOtherFormats
                      ? <ChevronUp className='h-4 w-4 text-muted-foreground'
                        aria-hidden='true' />
                      : <ChevronDown className='h-4 w-4 text-muted-foreground'
                        aria-hidden='true' />}
                  </button>

                  {showOtherFormats && (
                    <div className='space-y-2 border-t p-3'>
                      {secondaryAssets.map(asset => (
                        <div key={asset.name}
                          className='flex items-center gap-3 rounded-md border p-3'>
                          <div className='flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground'>
                            <AssetPlatformIcon type={asset.type} />
                          </div>
                          <div className='min-w-0 flex-1'>
                            <p className='truncate text-sm font-medium'>{asset.label}</p>
                            <p className='truncate text-xs text-muted-foreground'>
                              {asset.name}
                              {asset.size ? ` · ${formatBytes(asset.size)}` : ''}
                            </p>
                          </div>
                          <Button
                            size='sm'
                            variant='outline'
                            onClick={() => onDownloadAsset(asset.url)}
                            className='shrink-0 gap-1.5'
                            aria-label={`Download ${asset.label}`}
                          >
                            <Download className='h-3.5 w-3.5'
                              aria-hidden='true' />
                            Download
                          </Button>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </>
          )}

          {releaseBody && (
            <Accordion type='single'
              collapsible
              className='overflow-hidden rounded-lg border'>
              <AccordionItem value='release-notes'
                className='border-0'>
                <AccordionTrigger className='px-4 py-3 hover:bg-muted/50 hover:no-underline'>
                  <span>Release notes</span>
                </AccordionTrigger>
                <AccordionContent className='max-h-64 overflow-y-auto border-t px-4 pt-3 pb-4 text-sm text-foreground/90 leading-relaxed'>
                  <ReleaseNotesMarkdown body={releaseBody}
                    onOpenLink={onDownloadAsset} />
                </AccordionContent>
              </AccordionItem>
            </Accordion>
          )}

          {releaseUrl && (deployment === 'container' || primaryAsset) && (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              onClick={() => onDownloadAsset(releaseUrl)}
              className='w-full gap-2 text-muted-foreground'
            >
              <ExternalLink className='h-4 w-4'
                aria-hidden='true' />
              View full release on GitHub
            </Button>
          )}
        </div>

        <DialogFooter className='flex-shrink-0 flex-row items-center justify-between gap-2 border-t px-6 py-4'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={onDismissForever}
            className='mr-auto text-xs text-muted-foreground hover:text-foreground'
          >
            Skip this version
          </Button>
          <Button type='button'
            variant='secondary'
            size='sm'
            onClick={onDismiss}
            className='text-xs'>
            Remind me later
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DockerCommand({
  title,
  command,
  copyLabel,
  recommended = false,
  onCopy,
}: {
  title: string,
  command: string,
  copyLabel: string,
  recommended?: boolean,
  onCopy: (value: string, label: string) => void
}) {
  return (
    <div className='space-y-3 rounded-lg border p-4'>
      <div className='flex items-start justify-between gap-3'>
        <div className='min-w-0'>
          <div className='flex flex-wrap items-center gap-2'>
            <p className='text-sm font-medium'>{title}</p>
            {recommended && (
              <span className='rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground'>
                Recommended
              </span>
            )}
          </div>
        </div>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => onCopy(command, copyLabel)}
          className='h-8 shrink-0 gap-1.5 px-2 text-xs'
          aria-label={`Copy ${copyLabel.toLowerCase()}`}
        >
          <Copy className='h-3.5 w-3.5'
            aria-hidden='true' />
          Copy
        </Button>
      </div>
      <pre className='overflow-x-auto rounded-md bg-muted p-3 text-xs leading-relaxed'><code>{command}</code></pre>
    </div>
  );
}
