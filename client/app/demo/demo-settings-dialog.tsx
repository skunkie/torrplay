// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useEffect } from 'react';
import { toast } from 'sonner';

import { SettingsDialogLayout } from '@/components/settings-dialog-layout';
import { useAuth } from '@/lib/auth-context';
import { Auth, TorrentClient } from '@/lib/types/api';

interface DemoSettingsDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void
}

const defaultSettings = {
  auth: { enabled: false, type: 'basic' as const, username: '', password: '' },
  enableDlna: false,
  enableDownloader: false,
  fileStoragePath: '',
  friendlyName: 'TorrPlay',
  logLevel: 'INFO',
  logFormat: 'text' as const,
  maxMemory: 512,
  torrentClient: {
    disableDht: false,
    disableIpv6: true,
    disablePex: false,
    disableTcp: false,
    disableUtp: false,
    downloadRateLimit: 0,
    establishedConnsPerTorrent: 50,
    preferHeaderObfuscation: false,
    seed: false,
    torrentPeersHighWater: 500,
    uploadRateLimit: 3145728,
  },
  torrentTrackers: [
    'udp://explodie.org:6969',
    'udp://tracker.leechers-paradise.org:6969,udp://tracker.opentrackr.org:1337',
  ],
};

let demoDefaultsApplied = false;

export function DemoSettingsDialog({ open, onOpenChange }: DemoSettingsDialogProps) {
  const { settings, updateSettings } = useAuth();

  useEffect(() => {
    if (open && !demoDefaultsApplied) {
      updateSettings(defaultSettings);
      demoDefaultsApplied = true;
    }
  }, [open, updateSettings]);

  const handleReset = () => {
    // Resetting to initial demo values.
    updateSettings(defaultSettings);
    demoDefaultsApplied = true;
    toast.success('Settings reset', { description: 'Demo mode - settings not actually saved' });
  };

  const handleSave = () => {
    toast.success('Settings saved', { description: 'Demo mode - settings not actually saved' });
    onOpenChange(false);
  };

  const handleAuthSettingsChange = (value: Auth | null) => {
    updateSettings({ auth: value || { enabled: false, type: 'basic' } });
  };

  const handleTorrentClientSettingsChange = (value: TorrentClient | null) => {
    if (value) {
      updateSettings({ torrentClient: value });
    }
  };

  if (!settings) {
    return null;
  }

  return (
    <SettingsDialogLayout
      open={open}
      onOpenChange={onOpenChange}
      settings={settings}
      error={null}
      saving={false}
      onSave={handleSave}
      onReset={handleReset}
      onResetTorrentHandlerChoice={() => {}}
      dlnaEnabled={settings.enableDlna}
      setDlnaEnabled={value => updateSettings({ enableDlna: value })}
      downloaderEnabled={settings.enableDownloader}
      setDownloaderEnabled={value => updateSettings({ enableDownloader: value })}
      friendlyName={settings.friendlyName}
      setFriendlyName={value => updateSettings({ friendlyName: value })}
      maxMemory={settings.maxMemory}
      setMaxMemory={value => updateSettings({ maxMemory: value })}
      fileStoragePath={settings.fileStoragePath}
      setFileStoragePath={value => updateSettings({ fileStoragePath: value })}
      authSettings={settings.auth}
      setAuthSettings={handleAuthSettingsChange}
      torrentClientSettings={settings.torrentClient}
      setTorrentClientSettings={handleTorrentClientSettingsChange}
      torrentTrackers={settings.torrentTrackers}
      setTorrentTrackers={value => updateSettings({ torrentTrackers: value })}
      logLevel={settings.logLevel || 'INFO'}
      setLogLevel={value => updateSettings({ logLevel: value })}
      logFormat={settings.logFormat || 'text'}
      setLogFormat={value => updateSettings({ logFormat: value as 'json' | 'text' })}
      apiUrl={'http://localhost:8090'}
      setApiUrl={() => {}}
      isApiUrlCustom={false}
      setIsApiUrlCustom={() => {}}
      isApiUrlChangePending={false}
      externalPlayer={''}
      setExternalPlayer={() => {}}
    />
  );
}
