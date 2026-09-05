// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { toast } from 'sonner';

import { useAppUpdate } from '@/lib/app-update-context';
import { openExternalUrl } from '@/lib/platform';

import { UpdateDialogLayout } from './update-dialog-layout';

export function UpdateDialog({ deferWhile = false }: { deferWhile?: boolean }) {
  const update = useAppUpdate();

  if (!update.isSupported || deferWhile) return null;

  const {
    isDialogOpen,
    deployment,
    setIsDialogOpen,
    latestVersion,
    releaseBody,
    releaseUrl,
    publishedAt,
    primaryAsset,
    secondaryAssets,
    openDownload,
    dismissUpdate,
  } = update;

  if (!isDialogOpen) return null;

  const handleDownloadPrimary = () => {
    void openDownload(primaryAsset?.url || releaseUrl || undefined);
  };

  const handleDownloadAsset = (url: string) => {
    void openExternalUrl(url);
  };

  const handleCopy = (value: string, label: string) => {
    void navigator.clipboard.writeText(value)
      .then(() => toast.success(`${label} copied to clipboard`))
      .catch(err => toast.error('Failed to copy', {
        description: err instanceof Error ? err.message : 'Could not copy to clipboard.',
      }));
  };

  const handleDismiss = () => {
    setIsDialogOpen(false);
  };

  const handleDismissForever = () => {
    dismissUpdate(latestVersion || undefined);
  };

  return (
    <UpdateDialogLayout
      open={isDialogOpen}
      onOpenChange={setIsDialogOpen}
      deployment={deployment}
      latestVersion={latestVersion}
      releaseBody={releaseBody}
      releaseUrl={releaseUrl}
      publishedAt={publishedAt}
      primaryAsset={primaryAsset}
      secondaryAssets={secondaryAssets}
      onDownloadPrimary={handleDownloadPrimary}
      onDownloadAsset={handleDownloadAsset}
      onCopy={handleCopy}
      onDismiss={handleDismiss}
      onDismissForever={handleDismissForever}
    />
  );
}
