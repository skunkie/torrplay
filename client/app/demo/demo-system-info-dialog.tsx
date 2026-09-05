// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { SystemInfoDialogLayout } from '@/components/system-info-dialog-layout';
import { useOptionalAppUpdate } from '@/lib/app-update-context';
import { SystemInfo } from '@/lib/types/api';

const systemInfo: SystemInfo = {
  addresses: ['127.0.0.1:8090', '192.168.1.100:8090'],
  buildDate: '2026-01-01',
  commit: 'a1b2c3d',
  uptime: 86400,
  version: 'demo',
};

interface DemoSystemInfoDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void
}

export function DemoSystemInfoDialog({ open, onOpenChange }: DemoSystemInfoDialogProps) {
  const update = useOptionalAppUpdate();

  return (
    <SystemInfoDialogLayout
      open={open}
      onOpenChange={onOpenChange}
      systemInfo={systemInfo}
      updateStatus={update?.status}
      latestVersion={update?.latestVersion}
      onCheckForUpdates={update?.isSupported ? () => void update.checkForUpdates(true) : undefined}
      onViewUpdate={update?.isSupported ? () => {
        onOpenChange(false);
        update.setIsDialogOpen(true);
      } : undefined}
    />
  );
}
