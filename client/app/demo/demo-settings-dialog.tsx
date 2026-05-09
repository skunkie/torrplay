// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React from 'react';
import { toast } from 'sonner';

import { SettingsDialogLayout } from '@/components/settings-dialog-layout';
import { useAuth } from '@/lib/auth-context';
import { Auth } from '@/lib/types/api';

interface DemoSettingsDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void
}

export function DemoSettingsDialog({ open, onOpenChange }: DemoSettingsDialogProps) {
  const { settings, updateSettings } = useAuth();

  const handleReset = () => {
    // Resetting to initial demo values.
    updateSettings({
      auth: { enabled: false, type: 'basic', username: '', password: '' },
      enableDlna: false,
      enableDownloader: false,
      fileStoragePath: '',
      friendlyName: 'TorrPlay',
      maxMemory: 512,
    });
    toast.success('Settings reset', { description: 'Demo mode - settings not actually saved' });
  };

  const handleSave = () => {
    toast.success('Settings saved', { description: 'Demo mode - settings not actually saved' });
    onOpenChange(false);
  };

  const handleAuthSettingsChange = (value: Auth | null) => {
    updateSettings({ auth: value || { enabled: false, type: 'basic' } });
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
