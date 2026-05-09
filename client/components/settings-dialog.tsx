// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { isTauri } from '@tauri-apps/api/core';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import useSWR from 'swr';

import { getSettings } from '@/lib/api/settings';
import { getApiBaseUrl } from '@/lib/api-client';
import { useAuth } from '@/lib/auth-context';
import { Auth, Settings } from '@/lib/types/api';

import { SettingsDialogLayout } from './settings-dialog-layout';

interface SettingsDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void
}

const isValidUrl = (url: string) => {
  try {
    new URL(url);
    return true;
  } catch {
    return false;
  }
};

export function SettingsDialog({ open, onOpenChange }: SettingsDialogProps) {
  const { data: settings, error, mutate } = useSWR<Settings>(
    open ? '/api/v1/settings' : null,
    getSettings,
    {
      shouldRetryOnError: false,
    }
  );
  const { updateSettings } = useAuth();

  // State for settings.
  const [dlnaEnabled, setDlnaEnabled] = useState(false);
  const [downloaderEnabled, setDownloaderEnabled] = useState(false);
  const [friendlyName, setFriendlyName] = useState('');
  const [maxMemory, setMaxMemory] = useState(512);
  const [fileStoragePath, setFileStoragePath] = useState('');
  const [authSettings, setAuthSettings] = useState<Auth | null>(null);

  // State for API URL.
  const [apiUrl, setApiUrl] = useState('');
  const [isApiUrlCustom, setIsApiUrlCustom] = useState(false);
  const [initialApiUrl, setInitialApiUrl] = useState('');
  const [initialIsApiUrlCustom, setInitialIsApiUrlCustom] = useState(false);

  // State for external player.
  const [externalPlayer, setExternalPlayer] = useState('');

  // General state.
  const [saving, setSaving] = useState(false);
  const IS_TAURI = isTauri();

  useEffect(() => {
    if (open) {
      const currentApiUrl = getApiBaseUrl();
      const customApiUrl = localStorage.getItem('NEXT_PUBLIC_API_URL');
      const isCustom = customApiUrl !== null;

      setApiUrl(currentApiUrl);
      setInitialApiUrl(currentApiUrl);
      setIsApiUrlCustom(isCustom);
      setInitialIsApiUrlCustom(isCustom);

      if (IS_TAURI) {
        const externalPlayer = localStorage.getItem('external_player') || '';
        setExternalPlayer(externalPlayer);
      }
    }

    if (settings) {
      setDlnaEnabled(settings.enableDlna ?? false);
      setDownloaderEnabled(settings.enableDownloader ?? false);
      setFriendlyName(settings.friendlyName || 'TorrPlay DLNA');
      setMaxMemory(settings.maxMemory / (1024 * 1024));
      setFileStoragePath(settings.fileStoragePath || '');
      setAuthSettings(settings.auth);
    }
  }, [settings, open, IS_TAURI]);

  useEffect(() => {
    if (!fileStoragePath) {
      setDownloaderEnabled(false);
    }
  }, [fileStoragePath]);

  const handleSave = async () => {
    setSaving(true);

    if (IS_TAURI) {
      if (externalPlayer) {
        localStorage.setItem('external_player', externalPlayer);
      } else {
        localStorage.removeItem('external_player');
      }
    }

    const hasApiUrlBeenToggled = isApiUrlCustom !== initialIsApiUrlCustom;
    const hasApiUrlTextChanged = isApiUrlCustom && apiUrl !== initialApiUrl;
    const isApiUrlChangePending = hasApiUrlBeenToggled || hasApiUrlTextChanged;

    if (isApiUrlChangePending) {
      if (isApiUrlCustom) {
        if (!isValidUrl(apiUrl)) {
          toast.error('Invalid API URL', {
            description: 'Please enter a valid URL (e.g., http://localhost:8090).',
          });
          setSaving(false);
          return;
        }

        const controller = new AbortController();
        const timeoutId = setTimeout(() => controller.abort(), 5000);

        try {
          const response = await fetch(`${apiUrl}/api/v1/settings`, {
            signal: controller.signal,
          });
          if (!response.ok)
            throw new Error(`Server responded with status: ${response.status}`);
          await response.json();

          localStorage.setItem('NEXT_PUBLIC_API_URL', apiUrl);
          toast.info('API URL updated', {
            description: 'The page will now reload.',
            duration: 2500,
          });
          setTimeout(() => window.location.reload(), 2500);
          return;
        } catch (e) {
          if (e instanceof Error && e.name === 'AbortError') {
            toast.error('Connection timed out');
          } else {
            toast.error('Failed to connect to new URL');
          }
          setSaving(false);
          return;
        } finally {
          clearTimeout(timeoutId);
        }
      } else {
        localStorage.removeItem('NEXT_PUBLIC_API_URL');
        toast.info('API URL reset to default', {
          description: 'The page will now reload.',
          duration: 2500,
        });
        setTimeout(() => window.location.reload(), 2500);
        return;
      }
    }

    if (!settings) {
      toast.error('Cannot save settings', {
        description: 'The backend is offline.',
      });
      setSaving(false);
      return;
    }

    try {
      const settingsToUpdate: Partial<Settings> = {};

      if (dlnaEnabled !== settings.enableDlna) settingsToUpdate.enableDlna = dlnaEnabled;
      if (downloaderEnabled !== settings.enableDownloader) settingsToUpdate.enableDownloader = downloaderEnabled;
      if (fileStoragePath !== settings.fileStoragePath) settingsToUpdate.fileStoragePath = fileStoragePath;
      if (friendlyName !== settings.friendlyName) settingsToUpdate.friendlyName = friendlyName;
      if (maxMemory * 1024 * 1024 !== settings.maxMemory) settingsToUpdate.maxMemory = maxMemory * 1024 * 1024;

      if (authSettings) {
        const originalAuth = settings.auth;
        const authChanges: Partial<Auth> = {};

        if (authSettings.enabled !== originalAuth.enabled) authChanges.enabled = authSettings.enabled;
        if (authSettings.type !== originalAuth.type) authChanges.type = authSettings.type;
        if (authSettings.username !== originalAuth.username) authChanges.username = authSettings.username;
        if (authSettings.password && authSettings.password !== '********') {
          authChanges.password = authSettings.password;
        }

        if (Object.keys(authChanges).length > 0) {
          settingsToUpdate.auth = authChanges as Auth;
        }
      }

      if (Object.keys(settingsToUpdate).length > 0) {
        await updateSettings(settingsToUpdate);
      }

      toast.success('Settings saved');
      mutate();
      onOpenChange(false);
    } catch (e) {
      toast.error('Error saving settings', {
        description: e instanceof Error ? e.message : 'Unknown error',
      });
    } finally {
      setSaving(false);
    }
  };

  const handleReset = () => {
    // Reset API URL fields to their initial state.
    setApiUrl(initialApiUrl);
    setIsApiUrlCustom(initialIsApiUrlCustom);

    if (IS_TAURI) {
      const externalPlayer = localStorage.getItem('external_player') || '';
      setExternalPlayer(externalPlayer);
    }

    // Reset server settings fields if they were loaded.
    if (settings) {
      setDlnaEnabled(settings.enableDlna ?? false);
      setDownloaderEnabled(settings.enableDownloader ?? false);
      setFileStoragePath(settings.fileStoragePath || '');
      setFriendlyName(settings.friendlyName || 'TorrPlay DLNA');
      setMaxMemory(settings.maxMemory / (1024 * 1024));
      setAuthSettings(settings.auth);
    }
  };

  const handleResetTorrentHandlerChoice = () => {
    localStorage.removeItem('torrent_handler_choice');
    toast.success('Torrent handler choice reset');
  };

  const hasApiUrlBeenToggled = isApiUrlCustom !== initialIsApiUrlCustom;
  const hasApiUrlTextChanged = isApiUrlCustom && apiUrl !== initialApiUrl;
  const isApiUrlChangePending = hasApiUrlBeenToggled || hasApiUrlTextChanged;

  return (
    <SettingsDialogLayout
      open={open}
      onOpenChange={onOpenChange}
      settings={settings}
      error={error}
      saving={saving}
      onSave={handleSave}
      onReset={handleReset}
      onResetTorrentHandlerChoice={handleResetTorrentHandlerChoice}
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
      apiUrl={apiUrl}
      setApiUrl={setApiUrl}
      isApiUrlCustom={isApiUrlCustom}
      setIsApiUrlCustom={setIsApiUrlCustom}
      isApiUrlChangePending={isApiUrlChangePending}
      externalPlayer={externalPlayer}
      setExternalPlayer={setExternalPlayer}
    />
  );
}
