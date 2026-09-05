// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import {
  createContext,
  ReactNode,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from 'react';
import useSWR from 'swr';

import {
  classifyReleaseAssets,
  fetchLatestRelease,
  GitHubRelease,
  ReleaseAssetInfo,
} from '@/lib/api/releases';
import { getSystemInfo } from '@/lib/api/system';
import {
  getApplicationVersion,
  getArchitecture,
  getOperatingSystem,
  isTauriApp,
  openExternalUrl,
} from '@/lib/platform';
import { isNewerVersion, normalizeVersion } from '@/lib/semver';

export type UpdateStatus = 'idle' | 'checking' | 'available' | 'up-to-date' | 'error';
export type Deployment = 'native' | 'container';
export type DemoUpdateScenario = 'available' | 'up-to-date';

export interface AppUpdateContextType {
  isSupported: boolean,
  deployment: Deployment,
  status: UpdateStatus,
  currentVersion: string | null,
  latestVersion: string | null,
  releaseTitle: string | null,
  releaseBody: string | null,
  releaseUrl: string | null,
  publishedAt: string | null,
  primaryAsset: ReleaseAssetInfo | null,
  secondaryAssets: ReleaseAssetInfo[],
  isDialogOpen: boolean,
  setIsDialogOpen: (open: boolean) => void,
  checkForUpdates: (manual?: boolean) => Promise<void>,
  dismissUpdate: (version?: string) => void,
  isDismissed: (version: string) => boolean,
  openDownload: (url?: string) => Promise<void>
}

const STORAGE_KEY_DISMISSED = 'torrplay_dismissed_update_version';

const AppUpdateContext = createContext<AppUpdateContextType | undefined>(undefined);

export function AppUpdateProvider({
  children,
  currentVersion: initialCurrentVersion,
  autoCheck = true,
}: {
  children: ReactNode,
  currentVersion?: string | null,
  autoCheck?: boolean
}) {
  const localOperatingSystem = getOperatingSystem();
  const isLocalPlatformSupported = localOperatingSystem !== 'android';
  const isDesktop = isTauriApp();
  const { data: applicationVersion } = useSWR(
    !initialCurrentVersion && isLocalPlatformSupported && isDesktop
      ? 'torrplay-application-version'
      : null,
    () => getApplicationVersion(),
    { revalidateOnFocus: false, refreshInterval: 0 }
  );
  const { data: systemInfo } = useSWR(
    initialCurrentVersion || !isLocalPlatformSupported || isDesktop ? null : '/api/system/info',
    () => getSystemInfo(),
    { revalidateOnFocus: false, refreshInterval: 0 }
  );
  const isSupported = isLocalPlatformSupported && (isDesktop || systemInfo?.os !== 'android');
  const deployment: Deployment = isDesktop ? 'native' : systemInfo?.deployment || 'native';
  const currentVersion = initialCurrentVersion || applicationVersion || systemInfo?.version || null;

  const [status, setStatus] = useState<UpdateStatus>('idle');
  const [latestRelease, setLatestRelease] = useState<GitHubRelease | null>(null);
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  const autoChecked = useRef(false);

  const isDismissed = useCallback((version: string): boolean => {
    if (typeof window === 'undefined') return false;
    try {
      const dismissed = localStorage.getItem(STORAGE_KEY_DISMISSED);
      return normalizeVersion(dismissed || '') === normalizeVersion(version);
    } catch {
      return false;
    }
  }, []);

  const dismissUpdate = useCallback((version?: string) => {
    const target = version || latestRelease?.tag_name;
    try {
      if (target && typeof window !== 'undefined') {
        localStorage.setItem(STORAGE_KEY_DISMISSED, normalizeVersion(target));
      }
    } catch {
      // Persistence is best effort; dismissal should still close the dialog.
    } finally {
      setIsDialogOpen(false);
    }
  }, [latestRelease]);

  const checkForUpdates = useCallback(async (manual = false) => {
    if (!isSupported) return;
    if (!currentVersion) {
      setStatus(manual ? 'error' : 'idle');
      return;
    }

    setStatus('checking');
    try {
      const release = await fetchLatestRelease(undefined, { forceRefresh: manual });
      setLatestRelease(release);

      const latestTag = release.tag_name;
      if (isNewerVersion(currentVersion, latestTag)) {
        setStatus('available');
        if (!isDismissed(latestTag)) {
          setIsDialogOpen(true);
        }
      } else {
        setStatus(manual ? 'up-to-date' : 'idle');
      }
    } catch {
      if (manual) {
        setStatus('error');
      } else {
        setStatus('idle');
      }
    }
  }, [currentVersion, isDismissed, isSupported]);

  useEffect(() => {
    if (!isSupported || !autoCheck || autoChecked.current) return;
    if (!currentVersion || currentVersion === 'demo') return;

    autoChecked.current = true;
    const timer = setTimeout(() => {
      void checkForUpdates(false);
    }, 2000);

    return () => clearTimeout(timer);
  }, [isSupported, autoCheck, currentVersion, checkForUpdates]);

  const os = isDesktop ? localOperatingSystem : systemInfo?.os || 'unknown';
  const architecture = isDesktop ? getArchitecture() : systemInfo?.architecture || 'unknown';
  const classified = latestRelease && deployment === 'native'
    ? classifyReleaseAssets(latestRelease.assets, os, isDesktop, architecture)
    : { primaryAsset: null, secondaryAssets: [], allAssets: [] };

  const openDownload = useCallback(async (url?: string) => {
    const target = url || classified.primaryAsset?.url || latestRelease?.html_url;
    if (target) {
      await openExternalUrl(target);
    }
  }, [classified.primaryAsset, latestRelease]);

  return (
    <AppUpdateContext.Provider
      value={{
        isSupported,
        deployment,
        status,
        currentVersion: currentVersion || null,
        latestVersion: latestRelease?.tag_name || null,
        releaseTitle: latestRelease?.name || null,
        releaseBody: latestRelease?.body || null,
        releaseUrl: latestRelease?.html_url || null,
        publishedAt: latestRelease?.published_at || null,
        primaryAsset: classified.primaryAsset,
        secondaryAssets: classified.secondaryAssets,
        isDialogOpen,
        setIsDialogOpen,
        checkForUpdates,
        dismissUpdate,
        isDismissed,
        openDownload,
      }}
    >
      {children}
    </AppUpdateContext.Provider>
  );
}

export function DemoAppUpdateProvider({
  children,
  scenario = 'available',
  deployment = 'native',
}: {
  children: ReactNode,
  scenario?: DemoUpdateScenario,
  deployment?: Deployment
}) {
  const [status, setStatus] = useState<UpdateStatus>(scenario);
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  const hasAvailableUpdate = scenario === 'available';

  const mockAsset: ReleaseAssetInfo = {
    name: 'torrplay-client_1.2.0_x64-setup.exe',
    label: 'Windows Desktop Installer (.exe)',
    url: 'https://github.com/torrplay/torrplay/releases/download/v1.2.0/torrplay-client_1.2.0_x64-setup.exe',
    type: 'desktop-setup',
  };

  const mockSecondaryAsset: ReleaseAssetInfo = {
    name: 'TorrPlay-1.2.0-x64.msi',
    label: 'Windows Service Installer (.msi)',
    url: 'https://github.com/torrplay/torrplay/releases/download/v1.2.0/TorrPlay-1.2.0-x64.msi',
    type: 'windows-service',
  };

  const checkForUpdates = useCallback(async () => {
    setStatus('checking');
    await new Promise(resolve => setTimeout(resolve, 300));
    setStatus(scenario);
    if (scenario === 'available') {
      setIsDialogOpen(true);
    }
  }, [scenario]);

  const dismissUpdate = useCallback(() => {
    setIsDialogOpen(false);
  }, []);

  const isDismissed = useCallback(() => false, []);

  const openDownload = useCallback(async () => {
    // No-op in demo
  }, []);

  return (
    <AppUpdateContext.Provider
      value={{
        isSupported: true,
        deployment,
        status,
        currentVersion: '1.1.0',
        latestVersion: hasAvailableUpdate ? 'v1.2.0' : null,
        releaseTitle: hasAvailableUpdate ? 'TorrPlay 1.2.0' : null,
        releaseBody: hasAvailableUpdate ? '### What\'s New\n- Performance enhancements and bug fixes.' : null,
        releaseUrl: 'https://github.com/torrplay/torrplay/releases',
        publishedAt: '2026-09-01T00:00:00Z',
        primaryAsset: hasAvailableUpdate && deployment === 'native' ? mockAsset : null,
        secondaryAssets: hasAvailableUpdate && deployment === 'native' ? [mockSecondaryAsset] : [],
        isDialogOpen,
        setIsDialogOpen,
        checkForUpdates,
        dismissUpdate,
        isDismissed,
        openDownload,
      }}
    >
      {children}
    </AppUpdateContext.Provider>
  );
}

export function useOptionalAppUpdate(): AppUpdateContextType | null {
  return useContext(AppUpdateContext) || null;
}

export function useAppUpdate(): AppUpdateContextType {
  const context = useContext(AppUpdateContext);
  if (!context) {
    throw new Error('useAppUpdate must be used within an AppUpdateProvider or DemoAppUpdateProvider');
  }
  return context;
}
