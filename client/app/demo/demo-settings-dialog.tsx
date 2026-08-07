// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';

import { SettingsDialogLayout } from '@/components/settings-dialog-layout';
import { useAuth } from '@/lib/auth-context';
import { demoDefaultSettings } from '@/lib/demo-settings';
import { Auth, TorrentClient } from '@/lib/types/api';

interface DemoSettingsDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void
}

const defaultSettings = demoDefaultSettings;

export function DemoSettingsDialog({ open, onOpenChange }: DemoSettingsDialogProps) {
  const { settings, updateSettings } = useAuth();
  const [initialized, setInitialized] = useState(false);

  const [dlnaEnabled, setDlnaEnabled] = useState(false);
  const [downloaderEnabled, setDownloaderEnabled] = useState(false);
  const [friendlyName, setFriendlyName] = useState('');
  const [maxMemory, setMaxMemory] = useState(512);
  const [fileStoragePath, setFileStoragePathRaw] = useState('');
  const setFileStoragePath = (value: string) => {
    setFileStoragePathRaw(value);
    if (!value) {
      setDownloaderEnabled(false);
    }
  };
  const [authSettings, setAuthSettings] = useState<Auth | null>(null);
  const [torrentClientSettings, setTorrentClientSettings] = useState<TorrentClient | null>(null);
  const [torrentTrackers, setTorrentTrackers] = useState<string[]>([]);
  const [logLevel, setLogLevel] = useState<string>('INFO');
  const [logFormat, setLogFormat] = useState<'json' | 'text'>('text');

  // Initialize defaults on first open if no settings exist yet.
  const defaultsAppliedRef = useRef(false);
  useEffect(() => {
    if (!defaultsAppliedRef.current && !settings) {
      defaultsAppliedRef.current = true;
      updateSettings(defaultSettings);
    }
  }, [settings, updateSettings]);

  // Initialize local state from settings when dialog opens.
  useEffect(() => {
    if (!open) {
      setInitialized(false);
      return;
    }

    if (settings) {
      setDlnaEnabled(settings.enableDlna ?? false);
      setDownloaderEnabled(settings.enableDownloader ?? false);
      setFriendlyName(settings.friendlyName || 'TorrPlay');
      setMaxMemory(settings.maxMemory / (1024 * 1024));
      setFileStoragePathRaw(settings.fileStoragePath || '');
      setAuthSettings(settings.auth);
      setTorrentClientSettings(settings.torrentClient);
      setTorrentTrackers(settings.torrentTrackers || []);
      setLogLevel(settings.logLevel || 'INFO');
      setLogFormat(settings.logFormat || 'text');
      setInitialized(true);
    }
  }, [open, settings]);

  const handleSave = () => {
    updateSettings({
      enableDlna: dlnaEnabled,
      enableDownloader: downloaderEnabled,
      friendlyName: friendlyName,
      maxMemory: maxMemory * 1024 * 1024,
      fileStoragePath: fileStoragePath,
      auth: authSettings || { enabled: false, type: 'basic' as const },
      torrentClient: torrentClientSettings || defaultSettings.torrentClient,
      torrentTrackers: torrentTrackers,
      logLevel: logLevel,
      logFormat: logFormat,
    });
    toast.success('Settings saved', { description: 'Demo mode - settings not actually saved' });
    onOpenChange(false);
  };

  const handleReset = () => {
    if (!settings) return;
    setDlnaEnabled(settings.enableDlna ?? defaultSettings.enableDlna);
    setDownloaderEnabled(settings.enableDownloader ?? defaultSettings.enableDownloader);
    setFriendlyName(settings.friendlyName || defaultSettings.friendlyName);
    setMaxMemory((settings.maxMemory / (1024 * 1024)) || (defaultSettings.maxMemory / (1024 * 1024)));
    setFileStoragePathRaw(settings.fileStoragePath || defaultSettings.fileStoragePath);
    setAuthSettings(settings.auth || defaultSettings.auth);
    setTorrentClientSettings(settings.torrentClient || defaultSettings.torrentClient);
    setTorrentTrackers(settings.torrentTrackers || defaultSettings.torrentTrackers);
    setLogLevel(settings.logLevel || defaultSettings.logLevel || 'INFO');
    setLogFormat(settings.logFormat || defaultSettings.logFormat || 'text');
    toast.success('Settings reset', { description: 'Demo mode - settings not actually saved' });
  };

  const handleResetToDefaults = () => {
    setDlnaEnabled(defaultSettings.enableDlna);
    setDownloaderEnabled(defaultSettings.enableDownloader);
    setFriendlyName(defaultSettings.friendlyName);
    setMaxMemory(defaultSettings.maxMemory / (1024 * 1024));
    setFileStoragePathRaw(defaultSettings.fileStoragePath);
    setAuthSettings(defaultSettings.auth);
    setTorrentClientSettings(defaultSettings.torrentClient);
    setTorrentTrackers(defaultSettings.torrentTrackers);
    setLogLevel(defaultSettings.logLevel || 'INFO');
    setLogFormat(defaultSettings.logFormat || 'text');
    toast.success('Settings reset to defaults', { description: 'Demo mode - settings not actually saved' });
  };

  if (!settings || !initialized) {
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
      onResetToDefaults={handleResetToDefaults}
      onResetTorrentHandlerChoice={() => {}}
      dlnaEnabled={dlnaEnabled}
      setDlnaEnabled={setDlnaEnabled}
      downloaderEnabled={downloaderEnabled}
      setDownloaderEnabled={setDownloaderEnabled}
      friendlyName={friendlyName}
      setFriendlyName={setFriendlyName}
      maxMemory={maxMemory}
      setMaxMemory={setMaxMemory}
      fileStoragePath={fileStoragePath}
      setFileStoragePath={setFileStoragePath}
      authSettings={authSettings}
      setAuthSettings={setAuthSettings}
      torrentClientSettings={torrentClientSettings}
      setTorrentClientSettings={setTorrentClientSettings}
      torrentTrackers={torrentTrackers}
      setTorrentTrackers={setTorrentTrackers}
      logLevel={logLevel}
      setLogLevel={setLogLevel}
      logFormat={logFormat}
      setLogFormat={setLogFormat}
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
