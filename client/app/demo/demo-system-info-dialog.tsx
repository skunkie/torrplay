// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { SystemInfoDialogLayout } from '@/components/system-info-dialog-layout';
import {
  DemoUpdateScenario,
  Deployment,
  useOptionalAppUpdate,
} from '@/lib/app-update-context';
import { SystemInfo } from '@/lib/types/api';

import { DemoUpdateScenarioLinks } from './demo-update-scenario-links';

const systemInfo: SystemInfo = {
  addresses: ['127.0.0.1:8090', '192.168.1.100:8090'],
  architecture: 'x64',
  buildDate: '2026-01-01',
  commit: 'a1b2c3d',
  deployment: 'native',
  os: 'linux',
  uptime: 86400,
  version: '1.1.0',
};

interface DemoSystemInfoDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  updateScenario: DemoUpdateScenario,
  deployment: Deployment,
  searchParams: string
}

export function DemoSystemInfoDialog({
  open,
  onOpenChange,
  updateScenario,
  deployment,
  searchParams,
}: DemoSystemInfoDialogProps) {
  const update = useOptionalAppUpdate();

  return (
    <SystemInfoDialogLayout
      open={open}
      onOpenChange={onOpenChange}
      systemInfo={{
        ...systemInfo,
        deployment: update?.deployment || systemInfo.deployment,
      }}
      updateStatus={update?.status}
      latestVersion={update?.latestVersion}
      onCheckForUpdates={update?.isSupported ? () => void update.checkForUpdates(true) : undefined}
      onViewUpdate={update?.isSupported ? () => {
        onOpenChange(false);
        update.setIsDialogOpen(true);
      } : undefined}
      additionalContent={(
        <DemoUpdateScenarioLinks
          scenario={updateScenario}
          deployment={deployment}
          searchParams={searchParams}
        />
      )}
    />
  );
}
