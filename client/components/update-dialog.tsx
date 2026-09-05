// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useAppUpdate } from '@/lib/app-update-context';
import { openExternalUrl } from '@/lib/platform';

import { UpdateDialogLayout } from './update-dialog-layout';

export function UpdateDialog({ deferWhile = false }: { deferWhile?: boolean }) {
  const update = useAppUpdate();

  if (!update.isSupported || deferWhile) return null;

  const {
    isDialogOpen,
    setIsDialogOpen,
    currentVersion,
    latestVersion,
    releaseTitle,
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
      currentVersion={currentVersion}
      latestVersion={latestVersion}
      releaseTitle={releaseTitle}
      releaseBody={releaseBody}
      releaseUrl={releaseUrl}
      publishedAt={publishedAt}
      primaryAsset={primaryAsset}
      secondaryAssets={secondaryAssets}
      onDownloadPrimary={handleDownloadPrimary}
      onDownloadAsset={handleDownloadAsset}
      onDismiss={handleDismiss}
      onDismissForever={handleDismissForever}
    />
  );
}
