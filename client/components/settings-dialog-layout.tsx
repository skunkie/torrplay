// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { Capacitor } from '@capacitor/core';
import { isTauri } from '@tauri-apps/api/core';
import { AlertTriangle, Loader2 } from 'lucide-react';
import React, { useState } from 'react';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Slider } from '@/components/ui/slider';
import { Switch } from '@/components/ui/switch';
import { formatBytes } from '@/lib/format-utils';
import { Auth, Settings, TorrentClient } from '@/lib/types/api';

interface SettingsDialogLayoutProps {
  open: boolean,
  onOpenChange: (open: boolean) => void,
  settings?: Settings | null,
  error?: Error | null,
  saving: boolean,
  onSave: () => void,
  onReset: () => void,
  onResetTorrentHandlerChoice: () => void,

  dlnaEnabled: boolean,
  setDlnaEnabled: (value: boolean) => void,
  downloaderEnabled: boolean,
  setDownloaderEnabled: (value: boolean) => void,
  friendlyName: string,
  setFriendlyName: (value: string) => void,
  maxMemory: number,
  setMaxMemory: (value: number) => void,
  fileStoragePath: string,
  setFileStoragePath: (value: string) => void,
  authSettings: Auth | null,
  setAuthSettings: (value: Auth | null) => void,
  torrentClientSettings: TorrentClient | null,
  setTorrentClientSettings: (value: TorrentClient | null) => void,

  apiUrl: string,
  setApiUrl: (value: string) => void,
  isApiUrlCustom: boolean,
  setIsApiUrlCustom: (value: boolean) => void,
  isApiUrlChangePending: boolean,

  externalPlayer: string,
  setExternalPlayer: (value: string) => void
}

export function SettingsDialogLayout({
  open,
  onOpenChange,
  settings,
  error,
  saving,
  onSave,
  onReset,
  onResetTorrentHandlerChoice,
  dlnaEnabled,
  setDlnaEnabled,
  downloaderEnabled,
  setDownloaderEnabled,
  friendlyName,
  setFriendlyName,
  maxMemory,
  setMaxMemory,
  fileStoragePath,
  setFileStoragePath,
  authSettings,
  setAuthSettings,
  torrentClientSettings,
  setTorrentClientSettings,
  apiUrl,
  setApiUrl,
  isApiUrlCustom,
  setIsApiUrlCustom,
  isApiUrlChangePending,
  externalPlayer,
  setExternalPlayer,
}: SettingsDialogLayoutProps) {
  const IS_NATIVE = Capacitor.isNativePlatform();
  const IS_TAURI = isTauri();
  const isLoadingSettings = open && !settings && !error;
  const canSaveServerSettings = settings != null;
  const [rateLimitUnit, setRateLimitUnit] = useState('MiB/s');

  const convertRateLimit = (value: number, toUnit: string) => {
    if (toUnit === 'KiB/s') {
      return value / 1024;
    }
    if (toUnit === 'MiB/s') {
      return value / 1024 / 1024;
    }
    return value;
  };

  const getRateLimitInBytes = (value: number) => {
    if (rateLimitUnit === 'KiB/s') {
      return value * 1024;
    }
    if (rateLimitUnit === 'MiB/s') {
      return value * 1024 * 1024;
    }
    return value;
  };

  return (
    <Dialog open={open}
      onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Settings</DialogTitle>
          <DialogDescription>Configure application settings</DialogDescription>
        </DialogHeader>

        <div className='grid gap-y-6 py-4 max-h-[60vh] overflow-y-auto pr-3'>
          {!IS_NATIVE && (
            <div className='space-y-4'>
              <h3 className='text-lg font-medium text-foreground'>API Configuration</h3>
              <div className='flex items-center justify-between'>
                <Label htmlFor='custom-api-url'>Use Custom API URL</Label>
                <Switch id='custom-api-url'
                  checked={isApiUrlCustom}
                  onCheckedChange={setIsApiUrlCustom} />
              </div>
              {isApiUrlCustom && (
                <div className='space-y-2'>
                  <Label htmlFor='api-url'>API URL</Label>
                  <Input
                    id='api-url'
                    placeholder='http://localhost:8090'
                    value={apiUrl}
                    onChange={e => setApiUrl(e.target.value)}
                  />
                  <p className='text-sm text-muted-foreground'>
                    The URL of your TorrPlay server. The page will reload if you change this.
                  </p>
                </div>
              )}
            </div>
          )}

          {IS_TAURI && (
            <div className='space-y-4'>
              <h3 className='text-lg font-medium text-foreground'>External Player</h3>
              <div className='space-y-2'>
                <Label htmlFor='player-name'>Player Name</Label>
                <Input
                  id='player-name'
                  placeholder='vlc, mpv, etc.'
                  value={externalPlayer}
                  onChange={e => setExternalPlayer(e.target.value)}
                />
                <p className='text-sm text-muted-foreground'>
                  The executable name of your desired video player. Leave empty to use the system default.
                </p>
              </div>
            </div>
          )}

          {IS_NATIVE && (
            <div className='space-y-4'>
              <h3 className='text-lg font-medium text-foreground'>Torrent Handler</h3>
              <div className='flex items-center justify-between'>
                <div className='space-y-0.5'>
                  <Label>Play Action Preference</Label>
                  <p className='text-sm text-muted-foreground'>
                    Reset the saved preference for either Play or Add and Play a torrent.
                  </p>
                </div>
                <Button variant='outline'
                  onClick={onResetTorrentHandlerChoice}>
                  Reset
                </Button>
              </div>
            </div>
          )}

          {isLoadingSettings && (
            <div className='flex flex-col items-center justify-center gap-2 text-center py-8'>
              <Loader2 className='h-6 w-6 animate-spin text-muted-foreground' />
              <p className='text-sm text-muted-foreground'>Loading server settings...</p>
            </div>
          )}

          {error && !isLoadingSettings && (
            <div className='rounded-md bg-destructive/10 border border-destructive/20 p-3'>
              <div className='flex items-start gap-3'>
                <AlertTriangle className='h-5 w-5 text-destructive flex-shrink-0 mt-0.5' />
                <div>
                  <h4 className='font-semibold text-destructive'>Failed to load server settings</h4>
                  <p className='text-sm text-destructive/80 mt-1'>
                    Could not connect to the backend. The service might be starting or unavailable.
                  </p>
                </div>
              </div>
            </div>
          )}

          {(canSaveServerSettings || isApiUrlChangePending) && settings && (
            <div className='space-y-6'>
              <div className='space-y-4'>
                <h3 className='text-lg font-medium text-foreground'>Authentication</h3>
                <div className='flex items-center justify-between'>
                  <Label htmlFor='auth-enabled'>Enable Authentication</Label>
                  <Switch
                    id='auth-enabled'
                    checked={authSettings?.enabled ?? false}
                    onCheckedChange={checked => setAuthSettings({ ...authSettings, enabled: checked } as Auth)}
                  />
                </div>
                {authSettings?.enabled && (
                  <div className='space-y-4'>
                    <div className='space-y-2'>
                      <Label htmlFor='auth-type'>Authentication Type</Label>
                      <Select
                        value={authSettings.type}
                        onValueChange={value => setAuthSettings({ ...authSettings, type: value } as Auth)}
                      >
                        <SelectTrigger>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value='basic'>Basic</SelectItem>
                          <SelectItem value='bearer'>Bearer</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    <div className='space-y-2'>
                      <Label htmlFor='auth-username'>Username</Label>
                      <Input
                        id='auth-username'
                        value={authSettings.username}
                        onChange={e => setAuthSettings({ ...authSettings, username: e.target.value } as Auth)}
                      />
                    </div>
                    <div className='space-y-2'>
                      <Label htmlFor='auth-password'>Password</Label>
                      <Input
                        id='auth-password'
                        type='password'
                        value={authSettings.password}
                        onChange={e => setAuthSettings({ ...authSettings, password: e.target.value } as Auth)}
                      />
                    </div>
                  </div>
                )}
              </div>

              <div className='space-y-4'>
                <h3 className='text-lg font-medium text-foreground'>DLNA Configuration</h3>

                <div className='flex flex-col items-start gap-4 sm:flex-row sm:items-center sm:justify-between'>
                  <div className='space-y-0.5'>
                    <Label htmlFor='dlna-enabled'>Enable DLNA</Label>
                    <p className='text-sm text-muted-foreground'>Allow media streaming via DLNA protocol.</p>
                  </div>
                  <Switch id='dlna-enabled'
                    checked={dlnaEnabled}
                    onCheckedChange={setDlnaEnabled} />
                </div>

                {dlnaEnabled && (
                  <div className='space-y-2'>
                    <Label htmlFor='friendly-name'>Friendly Name</Label>
                    <Input
                      id='friendly-name'
                      placeholder='TorrPlay DLNA'
                      value={friendlyName}
                      onChange={e => setFriendlyName(e.target.value)}
                      maxLength={64}
                    />
                    <p className='text-sm text-muted-foreground'>Display name for DLNA devices (1-64 characters).</p>
                  </div>
                )}
              </div>

              <div className='space-y-4'>
                <h3 className='text-lg font-medium text-foreground'>Storage</h3>
                <div className='space-y-2'>
                  <Label htmlFor='file-storage-path'>File Storage Path</Label>
                  <Input
                    id='file-storage-path'
                    placeholder='/torrplay'
                    value={fileStoragePath}
                    onChange={e => setFileStoragePath(e.target.value)}
                  />
                  <p className='text-sm text-muted-foreground'>
                    The path where torrent files will be stored. Leave empty to disable file storage.
                  </p>
                </div>

                <div className='flex flex-col items-start gap-4 sm:flex-row sm:items-center sm:justify-between'>
                  <div className='space-y-0.5'>
                    <Label htmlFor='downloader-enabled'>Enable Downloader</Label>
                    <p className='text-sm text-muted-foreground'>Enable background downloading for torrents with file storage.</p>
                  </div>
                  <Switch
                    id='downloader-enabled'
                    checked={downloaderEnabled}
                    onCheckedChange={setDownloaderEnabled}
                    disabled={!fileStoragePath}
                  />
                </div>
              </div>

              <div className='space-y-4'>
                <h3 className='text-lg font-medium text-foreground'>Torrent Client</h3>
                <div className='flex items-center justify-between'>
                  <Label htmlFor='disable-dht'>Disable DHT</Label>
                  <Switch
                    id='disable-dht'
                    checked={torrentClientSettings?.disableDht ?? false}
                    onCheckedChange={checked => setTorrentClientSettings({ ...torrentClientSettings, disableDht: checked } as TorrentClient)}
                  />
                </div>
                <div className='flex items-center justify-between'>
                  <Label htmlFor='disable-ipv6'>Disable IPv6</Label>
                  <Switch
                    id='disable-ipv6'
                    checked={torrentClientSettings?.disableIpv6 ?? false}
                    onCheckedChange={checked => setTorrentClientSettings({ ...torrentClientSettings, disableIpv6: checked } as TorrentClient)}
                  />
                </div>
                <div className='flex items-center justify-between'>
                  <Label htmlFor='disable-pex'>Disable PEX</Label>
                  <Switch
                    id='disable-pex'
                    checked={torrentClientSettings?.disablePex ?? false}
                    onCheckedChange={checked => setTorrentClientSettings({ ...torrentClientSettings, disablePex: checked } as TorrentClient)}
                  />
                </div>
                <div className='flex items-center justify-between'>
                  <Label htmlFor='disable-tcp'>Disable TCP</Label>
                  <Switch
                    id='disable-tcp'
                    checked={torrentClientSettings?.disableTcp ?? false}
                    onCheckedChange={checked => setTorrentClientSettings({ ...torrentClientSettings, disableTcp: checked } as TorrentClient)}
                  />
                </div>
                <div className='flex items-center justify-between'>
                  <Label htmlFor='disable-utp'>Disable uTP</Label>
                  <Switch
                    id='disable-utp'
                    checked={torrentClientSettings?.disableUtp ?? false}
                    onCheckedChange={checked => setTorrentClientSettings({ ...torrentClientSettings, disableUtp: checked } as TorrentClient)}
                  />
                </div>
                <div className='flex items-center justify-between'>
                  <Label htmlFor='seed'>Enable Seeding</Label>
                  <Switch
                    id='seed'
                    checked={torrentClientSettings?.seed ?? false}
                    onCheckedChange={checked => setTorrentClientSettings({ ...torrentClientSettings, seed: checked } as TorrentClient)}
                  />
                </div>
                <div className='flex items-center justify-between'>
                  <Label htmlFor='prefer-header-obfuscation'>Prefer Header Obfuscation</Label>
                  <Switch
                    id='prefer-header-obfuscation'
                    checked={torrentClientSettings?.preferHeaderObfuscation ?? false}
                    onCheckedChange={checked => setTorrentClientSettings({ ...torrentClientSettings, preferHeaderObfuscation: checked } as TorrentClient)}
                  />
                </div>

                <div className='space-y-2'>
                  <div className='flex items-center justify-between'>
                    <Label>Rate Limit Unit</Label>
                    <Select value={rateLimitUnit}
                      onValueChange={setRateLimitUnit}>
                      <SelectTrigger className='w-[120px]'>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value='bytes/s'>bytes/s</SelectItem>
                        <SelectItem value='KiB/s'>KiB/s</SelectItem>
                        <SelectItem value='MiB/s'>MiB/s</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                </div>

                <div className='space-y-2'>
                  <Label htmlFor='download-rate-limit'>Download Rate Limit ({rateLimitUnit})</Label>
                  <Input
                    id='download-rate-limit'
                    type='number'
                    value={convertRateLimit(torrentClientSettings?.downloadRateLimit ?? 0, rateLimitUnit)}
                    onChange={e => setTorrentClientSettings({ ...torrentClientSettings, downloadRateLimit: getRateLimitInBytes(parseInt(e.target.value)) } as TorrentClient)}
                  />
                  <p className='text-sm text-muted-foreground'>
                    0 means unlimited.
                  </p>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='upload-rate-limit'>Upload Rate Limit ({rateLimitUnit})</Label>
                  <Input
                    id='upload-rate-limit'
                    type='number'
                    value={convertRateLimit(torrentClientSettings?.uploadRateLimit ?? 0, rateLimitUnit)}
                    onChange={e => setTorrentClientSettings({ ...torrentClientSettings, uploadRateLimit: getRateLimitInBytes(parseInt(e.target.value)) } as TorrentClient)}
                  />
                  <p className='text-sm text-muted-foreground'>
                    0 means unlimited.
                  </p>
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='established-conns-per-torrent'>Connections Per Torrent</Label>
                  <Input
                    id='established-conns-per-torrent'
                    type='number'
                    value={torrentClientSettings?.establishedConnsPerTorrent ?? 50}
                    onChange={e => setTorrentClientSettings({ ...torrentClientSettings, establishedConnsPerTorrent: parseInt(e.target.value) } as TorrentClient)}
                  />
                </div>
                <div className='space-y-2'>
                  <Label htmlFor='torrent-peers-high-water'>Peers High Water Mark</Label>
                  <Input
                    id='torrent-peers-high-water'
                    type='number'
                    value={torrentClientSettings?.torrentPeersHighWater ?? 500}
                    onChange={e => setTorrentClientSettings({ ...torrentClientSettings, torrentPeersHighWater: parseInt(e.target.value) } as TorrentClient)}
                  />
                </div>
              </div>

              <div className='space-y-4'>
                <h3 className='text-lg font-medium text-foreground'>Memory Management</h3>

                <div className='space-y-3'>
                  <div className='flex flex-col items-start gap-2 sm:flex-row sm:items-center sm:justify-between'>
                    <Label htmlFor='max-memory'>Maximum Memory</Label>
                    <span className='text-sm font-mono text-muted-foreground'>
                      {formatBytes(maxMemory * 1024 * 1024)}
                    </span>
                  </div>

                  <Slider
                    id='max-memory'
                    min={32}
                    max={2048}
                    step={32}
                    value={[maxMemory]}
                    onValueChange={value => setMaxMemory(value[0])}
                    className='py-4'
                  />

                  <div className='flex justify-between text-sm text-muted-foreground'>
                    <span>32 MB</span>
                    <span>2 GB</span>
                  </div>

                  <p className='text-sm text-muted-foreground'>
                    Memory limit for piece storage. Lower values reduce memory usage but may impact streaming performance.
                  </p>

                  {maxMemory < 64 && (
                    <div className='rounded-md bg-destructive/10 border border-destructive/20 p-3'>
                      <p className='text-sm text-destructive'>
                        Warning: Low memory settings may cause poor streaming performance for large files.
                      </p>
                    </div>
                  )}
                </div>
              </div>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant='outline'
            onClick={onReset}
            disabled={saving}>
            Reset
          </Button>
          <Button onClick={onSave}
            disabled={saving || (!isApiUrlChangePending && !canSaveServerSettings)}>
            {saving ? (
              <>
                <Loader2 className='h-4 w-4 mr-2 animate-spin' />
                Saving...
              </>
            ) : (
              'Save Changes'
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
