// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { isTauri } from '@tauri-apps/api/core';
import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import useSWR from 'swr';

import { getSettings } from '@/lib/api/settings';
import { getSystemLogs } from '@/lib/api/system';
import { getApiBaseUrl, setApiBaseUrlOverride } from '@/lib/api-client';
import { useAuth } from '@/lib/auth-context';
import { copyLogEntries } from '@/lib/copy-logs';
import { LogLevel, normalizeLogLevel } from '@/lib/log-level';
import { Auth, Settings, TorrentClient } from '@/lib/types/api';

import { LogsViewLayout } from './logs-view-layout';
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
  const [logsView, setLogsView] = useState(false);
  const { data: logEntries = [], error: logsError, isLoading: logsLoading, mutate: refreshLogs } = useSWR(
    open && logsView ? '/api/system/logs' : null,
    getSystemLogs,
    { shouldRetryOnError: false }
  );
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
  const [stremioEnabled, setStremioEnabled] = useState(false);
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
  const [corsAllowedOrigins, setCorsAllowedOrigins] = useState<string[]>([]);
  const [torrentClientSettings, setTorrentClientSettings] = useState<TorrentClient | null>(null);
  const [torrentTrackers, setTorrentTrackers] = useState<string[]>([]);
  const [logLevel, setLogLevel] = useState<LogLevel>('INFO');
  const [logFormat, setLogFormat] = useState<'json' | 'text'>('text');

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
    if (!open) setLogsView(false);
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
      setStremioEnabled(settings.enableStremio ?? false);
      setDownloaderEnabled(settings.enableDownloader ?? false);
      setFriendlyName(settings.friendlyName || 'TorrPlay');
      setMaxMemory(settings.maxMemory / (1024 * 1024));
      setFileStoragePath(settings.fileStoragePath || '');
      setAuthSettings(settings.auth);
      setCorsAllowedOrigins(settings.corsAllowedOrigins || []);
      setTorrentClientSettings(settings.torrentClient);
      setTorrentTrackers(settings.torrentTrackers || []);
      setLogLevel(normalizeLogLevel(settings.logLevel));
      setLogFormat(settings.logFormat || 'text');
    }
  }, [settings, open, IS_TAURI]);

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
            headers: {
              'X-Requested-With': 'XMLHttpRequest',
            },
          });
          if (!response.ok)
            throw new Error(`Server responded with status: ${response.status}`);
          await response.json();

          setApiBaseUrlOverride(apiUrl);
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
        setApiBaseUrlOverride(null);
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

    // The server never returns the stored password, so enabling authentication
    // must set one explicitly rather than silently reuse a forgotten password.
    if (authSettings?.enabled && !settings.auth?.enabled && !authSettings.password) {
      toast.error('Password required', {
        description: 'Enter a password to enable authentication.',
      });
      setSaving(false);
      return;
    }

    // Mirror the API's length limits so the error names the offending field.
    // The API counts Unicode code points, not UTF-16 code units.
    if (authSettings?.enabled) {
      const username = authSettings.username ?? '';
      const usernameLength = Array.from(username).length;
      if (username !== settings.auth?.username && (usernameLength < 4 || usernameLength > 64)) {
        toast.error('Invalid username', {
          description: 'The username must be 4 to 64 characters long.',
        });
        setSaving(false);
        return;
      }
      const password = authSettings.password ?? '';
      const passwordLength = Array.from(password).length;
      if (password && (passwordLength < 4 || passwordLength > 128)) {
        toast.error('Invalid password', {
          description: 'The password must be 4 to 128 characters long.',
        });
        setSaving(false);
        return;
      }
    }

    try {
      const settingsToUpdate: Partial<Settings> = {};
      const normalizedCorsAllowedOrigins = corsAllowedOrigins.map(origin => origin.trim()).filter(Boolean);

      if (dlnaEnabled !== settings.enableDlna) settingsToUpdate.enableDlna = dlnaEnabled;
      if (stremioEnabled !== settings.enableStremio) settingsToUpdate.enableStremio = stremioEnabled;
      if (downloaderEnabled !== settings.enableDownloader) settingsToUpdate.enableDownloader = downloaderEnabled;
      if (fileStoragePath !== settings.fileStoragePath) settingsToUpdate.fileStoragePath = fileStoragePath;
      if (friendlyName !== settings.friendlyName) settingsToUpdate.friendlyName = friendlyName;
      if (maxMemory * 1024 * 1024 !== settings.maxMemory) settingsToUpdate.maxMemory = maxMemory * 1024 * 1024;
      if (JSON.stringify(torrentTrackers) !== JSON.stringify(settings.torrentTrackers)) settingsToUpdate.torrentTrackers = torrentTrackers;
      if (logLevel !== settings.logLevel) settingsToUpdate.logLevel = logLevel;
      if (logFormat !== settings.logFormat) settingsToUpdate.logFormat = logFormat;
      if (JSON.stringify(normalizedCorsAllowedOrigins) !== JSON.stringify(settings.corsAllowedOrigins || [])) settingsToUpdate.corsAllowedOrigins = normalizedCorsAllowedOrigins;

      if (authSettings) {
        const originalAuth = settings.auth;
        const authChanges: Partial<Auth> = {};

        if (authSettings.enabled !== originalAuth.enabled) authChanges.enabled = authSettings.enabled;
        // Credentials are hidden while authentication is off; leave the stored
        // ones untouched rather than sending cleared fields the API rejects.
        if (authSettings.enabled) {
          if (authSettings.type !== originalAuth.type) authChanges.type = authSettings.type;
          if (authSettings.username !== originalAuth.username) authChanges.username = authSettings.username;
          if (authSettings.password) authChanges.password = authSettings.password;
        }

        if (Object.keys(authChanges).length > 0) {
          settingsToUpdate.auth = authChanges as Auth;
        }
      }

      if (torrentClientSettings) {
        const originalTorrentClientSettings = settings.torrentClient;
        const torrentClientChanges: Partial<TorrentClient> = {};

        if (torrentClientSettings.disableDht !== originalTorrentClientSettings.disableDht) torrentClientChanges.disableDht = torrentClientSettings.disableDht;
        if (torrentClientSettings.disableIpv6 !== originalTorrentClientSettings.disableIpv6) torrentClientChanges.disableIpv6 = torrentClientSettings.disableIpv6;
        if (torrentClientSettings.disablePex !== originalTorrentClientSettings.disablePex) torrentClientChanges.disablePex = torrentClientSettings.disablePex;
        if (torrentClientSettings.disableTcp !== originalTorrentClientSettings.disableTcp) torrentClientChanges.disableTcp = torrentClientSettings.disableTcp;
        if (torrentClientSettings.disableUtp !== originalTorrentClientSettings.disableUtp) torrentClientChanges.disableUtp = torrentClientSettings.disableUtp;
        if (torrentClientSettings.downloadRateLimit !== originalTorrentClientSettings.downloadRateLimit) torrentClientChanges.downloadRateLimit = torrentClientSettings.downloadRateLimit;
        if (torrentClientSettings.establishedConnsPerTorrent !== originalTorrentClientSettings.establishedConnsPerTorrent) torrentClientChanges.establishedConnsPerTorrent = torrentClientSettings.establishedConnsPerTorrent;
        if (torrentClientSettings.halfOpenConnsPerTorrent !== originalTorrentClientSettings.halfOpenConnsPerTorrent) torrentClientChanges.halfOpenConnsPerTorrent = torrentClientSettings.halfOpenConnsPerTorrent;
        if (torrentClientSettings.maxAllocPeerRequestDataPerConn !== originalTorrentClientSettings.maxAllocPeerRequestDataPerConn) torrentClientChanges.maxAllocPeerRequestDataPerConn = torrentClientSettings.maxAllocPeerRequestDataPerConn;
        if (torrentClientSettings.preferHeaderObfuscation !== originalTorrentClientSettings.preferHeaderObfuscation) torrentClientChanges.preferHeaderObfuscation = torrentClientSettings.preferHeaderObfuscation;
        if (torrentClientSettings.seed !== originalTorrentClientSettings.seed) torrentClientChanges.seed = torrentClientSettings.seed;
        if (torrentClientSettings.torrentPeersHighWater !== originalTorrentClientSettings.torrentPeersHighWater) torrentClientChanges.torrentPeersHighWater = torrentClientSettings.torrentPeersHighWater;
        if (torrentClientSettings.torrentPeersLowWater !== originalTorrentClientSettings.torrentPeersLowWater) torrentClientChanges.torrentPeersLowWater = torrentClientSettings.torrentPeersLowWater;
        if (torrentClientSettings.totalHalfOpenConns !== originalTorrentClientSettings.totalHalfOpenConns) torrentClientChanges.totalHalfOpenConns = torrentClientSettings.totalHalfOpenConns;
        if (torrentClientSettings.uploadRateLimit !== originalTorrentClientSettings.uploadRateLimit) torrentClientChanges.uploadRateLimit = torrentClientSettings.uploadRateLimit;

        if (Object.keys(torrentClientChanges).length > 0) {
          settingsToUpdate.torrentClient = torrentClientChanges as TorrentClient;
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
      setStremioEnabled(settings.enableStremio ?? false);
      setDownloaderEnabled(settings.enableDownloader ?? false);
      setFileStoragePath(settings.fileStoragePath || '');
      setFriendlyName(settings.friendlyName || 'TorrPlay');
      setMaxMemory(settings.maxMemory / (1024 * 1024));
      setAuthSettings(settings.auth);
      setCorsAllowedOrigins(settings.corsAllowedOrigins || []);
      setTorrentClientSettings(settings.torrentClient);
      setTorrentTrackers(settings.torrentTrackers || []);
      setLogLevel(normalizeLogLevel(settings.logLevel));
      setLogFormat(settings.logFormat || 'text');
    }
  };

  const handleResetToDefaults = () => {
    setApiUrl(initialApiUrl);
    setIsApiUrlCustom(initialIsApiUrlCustom);

    if (IS_TAURI) {
      const externalPlayer = localStorage.getItem('external_player') || '';
      setExternalPlayer(externalPlayer);
    }

    setDlnaEnabled(false);
    setStremioEnabled(false);
    setDownloaderEnabled(false);
    setFileStoragePath('');
    setFriendlyName('TorrPlay');
    setMaxMemory(64);
    setAuthSettings({ enabled: false, type: 'basic', username: '', password: '' });
    setCorsAllowedOrigins([]);
    setTorrentClientSettings({
      disableDht: false,
      disableIpv6: true,
      disablePex: false,
      disableTcp: false,
      disableUtp: false,
      downloadRateLimit: 0,
      establishedConnsPerTorrent: 50,
      halfOpenConnsPerTorrent: 25,
      maxAllocPeerRequestDataPerConn: 1048576,
      preferHeaderObfuscation: false,
      seed: false,
      torrentPeersHighWater: 500,
      torrentPeersLowWater: 50,
      totalHalfOpenConns: 100,
      uploadRateLimit: 0,
    });
    setTorrentTrackers([]);
    setLogLevel('INFO');
    setLogFormat('text');

    toast.success('Settings reset to defaults');
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
      logsView={logsView}
      onViewLogs={() => setLogsView(true)}
      onBackFromLogs={() => setLogsView(false)}
      logsContent={<LogsViewLayout entries={logEntries}
        error={logsError}
        loading={logsLoading}
        retainedCount={settings?.logStoreSize}
        onRefresh={() => { void refreshLogs(); }}
        onCopyVisible={entries => {
          void copyLogEntries(entries)
            .then(() => toast.success('Logs copied'))
            .catch(() => toast.error('Could not copy logs'));
        }} />}
      settings={settings}
      error={error}
      saving={saving}
      onSave={handleSave}
      onReset={handleReset}
      onResetToDefaults={handleResetToDefaults}
      onResetTorrentHandlerChoice={handleResetTorrentHandlerChoice}
      dlnaEnabled={dlnaEnabled}
      setDlnaEnabled={setDlnaEnabled}
      stremioEnabled={stremioEnabled}
      setStremioEnabled={setStremioEnabled}
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
      corsAllowedOrigins={corsAllowedOrigins}
      setCorsAllowedOrigins={setCorsAllowedOrigins}
      torrentClientSettings={torrentClientSettings}
      setTorrentClientSettings={setTorrentClientSettings}
      torrentTrackers={torrentTrackers}
      setTorrentTrackers={setTorrentTrackers}
      logLevel={logLevel}
      setLogLevel={setLogLevel}
      logFormat={logFormat}
      setLogFormat={setLogFormat}
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
