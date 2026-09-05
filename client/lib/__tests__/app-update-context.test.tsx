// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, renderHook, waitFor } from '@testing-library/react';
import { ReactNode } from 'react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { fetchLatestRelease } from '@/lib/api/releases';
import { getSystemInfo } from '@/lib/api/system';
import {
  AppUpdateProvider,
  DemoAppUpdateProvider,
  DemoUpdateScenario,
  useAppUpdate,
} from '@/lib/app-update-context';
import {
  getApplicationVersion,
  getArchitecture,
  getOperatingSystem,
  isTauriApp,
} from '@/lib/platform';

vi.mock('@/lib/api/releases', async importOriginal => {
  const original = await importOriginal<typeof import('@/lib/api/releases')>();
  return {
    ...original,
    fetchLatestRelease: vi.fn(),
  };
});

vi.mock('@/lib/platform', async importOriginal => {
  const original = await importOriginal<typeof import('@/lib/platform')>();
  return {
    ...original,
    getApplicationVersion: vi.fn(),
    getArchitecture: vi.fn(),
    getOperatingSystem: vi.fn(),
    isTauriApp: vi.fn(),
  };
});

vi.mock('@/lib/api/system', () => ({
  getSystemInfo: vi.fn(),
}));

describe('AppUpdateProvider', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.mocked(getArchitecture).mockReturnValue('unknown');
    vi.mocked(getOperatingSystem).mockReturnValue('android');
    vi.mocked(isTauriApp).mockReturnValue(false);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it('disables automatic and manual update checks on Android', async () => {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <AppUpdateProvider currentVersion='1.0.0'>{children}</AppUpdateProvider>
    );
    const { result } = renderHook(() => useAppUpdate(), { wrapper });

    expect(result.current.isSupported).toBe(false);

    await act(async () => {
      await result.current.checkForUpdates(true);
      vi.advanceTimersByTime(3000);
    });

    expect(fetchLatestRelease).not.toHaveBeenCalled();
    expect(result.current.status).toBe('idle');
    expect(result.current.isDialogOpen).toBe(false);
  });

  it('uses the packaged Tauri version instead of the remote backend version', async () => {
    vi.useRealTimers();
    vi.mocked(getOperatingSystem).mockReturnValue('windows');
    vi.mocked(isTauriApp).mockReturnValue(true);
    vi.mocked(getApplicationVersion).mockResolvedValue('2.0.0');
    vi.mocked(getSystemInfo).mockResolvedValue({
      addresses: [],
      architecture: 'x64',
      buildDate: '2026-09-05',
      commit: 'abcdef0',
      deployment: 'container',
      os: 'linux',
      uptime: 1,
      version: '1.0.0',
    });

    const wrapper = ({ children }: { children: ReactNode }) => (
      <SWRConfig value={{ provider: () => new Map() }}>
        <AppUpdateProvider autoCheck={false}>{children}</AppUpdateProvider>
      </SWRConfig>
    );
    const { result } = renderHook(() => useAppUpdate(), { wrapper });

    await waitFor(() => expect(result.current.currentVersion).toBe('2.0.0'));
    expect(getSystemInfo).not.toHaveBeenCalled();
    expect(result.current.deployment).toBe('native');
  });

  it('disables update checks for an Android server opened in a browser', async () => {
    vi.useRealTimers();
    vi.mocked(getOperatingSystem).mockReturnValue('windows');
    vi.mocked(getSystemInfo).mockResolvedValue({
      addresses: [],
      architecture: 'arm64',
      buildDate: '2026-09-05',
      commit: 'abcdef0',
      deployment: 'native',
      os: 'android',
      uptime: 1,
      version: '1.0.0',
    });

    const wrapper = ({ children }: { children: ReactNode }) => (
      <SWRConfig value={{ provider: () => new Map() }}>
        <AppUpdateProvider autoCheck={false}>{children}</AppUpdateProvider>
      </SWRConfig>
    );
    const { result } = renderHook(() => useAppUpdate(), { wrapper });

    await waitFor(() => expect(result.current.isSupported).toBe(false));
    await act(async () => result.current.checkForUpdates(true));

    expect(fetchLatestRelease).not.toHaveBeenCalled();
  });

  it('selects an update for the connected server instead of the browser device', async () => {
    vi.useRealTimers();
    vi.mocked(getOperatingSystem).mockReturnValue('windows');
    vi.mocked(getArchitecture).mockReturnValue('x64');
    vi.mocked(getSystemInfo).mockResolvedValue({
      addresses: [],
      architecture: 'arm64',
      buildDate: '2026-09-05',
      commit: 'abcdef0',
      deployment: 'native',
      os: 'linux',
      uptime: 1,
      version: '1.0.0',
    });
    vi.mocked(fetchLatestRelease).mockResolvedValue({
      tag_name: 'v1.1.0',
      name: 'TorrPlay 1.1.0',
      body: null,
      html_url: 'https://github.com/torrplay/torrplay/releases/tag/v1.1.0',
      published_at: '2026-09-05T00:00:00Z',
      assets: [{
        name: 'torrplay_1.1.0_arm64.deb',
        browser_download_url: 'https://example.com/torrplay_1.1.0_arm64.deb',
      }],
    });

    const wrapper = ({ children }: { children: ReactNode }) => (
      <SWRConfig value={{ provider: () => new Map() }}>
        <AppUpdateProvider autoCheck={false}>{children}</AppUpdateProvider>
      </SWRConfig>
    );
    const { result } = renderHook(() => useAppUpdate(), { wrapper });

    await waitFor(() => expect(result.current.currentVersion).toBe('1.0.0'));
    await act(async () => result.current.checkForUpdates(true));

    await waitFor(() => expect(result.current.primaryAsset?.type).toBe('linux-server-deb'));
    expect(result.current.primaryAsset?.architecture).toBe('arm64');
  });

  it('uses Docker guidance and suppresses native packages for a container server', async () => {
    vi.useRealTimers();
    vi.mocked(getOperatingSystem).mockReturnValue('windows');
    vi.mocked(getSystemInfo).mockResolvedValue({
      addresses: [],
      architecture: 'x64',
      buildDate: '2026-09-05',
      commit: 'abcdef0',
      deployment: 'container',
      os: 'linux',
      uptime: 1,
      version: '1.0.0',
    });
    vi.mocked(fetchLatestRelease).mockResolvedValue({
      tag_name: 'v1.1.0',
      name: 'TorrPlay 1.1.0',
      body: null,
      html_url: 'https://github.com/torrplay/torrplay/releases/tag/v1.1.0',
      published_at: '2026-09-05T00:00:00Z',
      assets: [{
        name: 'torrplay_1.1.0_x64.deb',
        browser_download_url: 'https://example.com/torrplay_1.1.0_x64.deb',
      }],
    });

    const wrapper = ({ children }: { children: ReactNode }) => (
      <SWRConfig value={{ provider: () => new Map() }}>
        <AppUpdateProvider autoCheck={false}>{children}</AppUpdateProvider>
      </SWRConfig>
    );
    const { result } = renderHook(() => useAppUpdate(), { wrapper });

    await waitFor(() => expect(result.current.deployment).toBe('container'));
    await act(async () => result.current.checkForUpdates(true));

    expect(result.current.status).toBe('available');
    expect(result.current.primaryAsset).toBeNull();
    expect(result.current.secondaryAssets).toEqual([]);
  });

  it('closes the update dialog when dismissal persistence fails', () => {
    vi.useRealTimers();
    vi.mocked(getOperatingSystem).mockReturnValue('windows');
    const storageError = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });
    const wrapper = ({ children }: { children: ReactNode }) => (
      <AppUpdateProvider
        currentVersion='1.0.0'
        autoCheck={false}
      >
        {children}
      </AppUpdateProvider>
    );
    const { result } = renderHook(() => useAppUpdate(), { wrapper });

    act(() => {
      result.current.setIsDialogOpen(true);
    });
    act(() => {
      result.current.dismissUpdate('1.1.0');
    });

    expect(result.current.isDialogOpen).toBe(false);
    storageError.mockRestore();
  });
});

describe('DemoAppUpdateProvider', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it.each<{
    scenario: DemoUpdateScenario,
    expectedStatus: DemoUpdateScenario
  }>([
    { scenario: 'available', expectedStatus: 'available' },
    { scenario: 'up-to-date', expectedStatus: 'up-to-date' },
  ])('exposes the $scenario scenario', ({ scenario, expectedStatus }) => {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <DemoAppUpdateProvider scenario={scenario}>{children}</DemoAppUpdateProvider>
    );
    const { result } = renderHook(() => useAppUpdate(), { wrapper });

    expect(result.current.status).toBe(expectedStatus);
    expect(result.current.latestVersion).toBe(scenario === 'available' ? 'v1.2.0' : null);
  });

  it('suppresses native assets for the Docker scenario', () => {
    const wrapper = ({ children }: { children: ReactNode }) => (
      <DemoAppUpdateProvider scenario='available'
        deployment='container'>
        {children}
      </DemoAppUpdateProvider>
    );
    const { result } = renderHook(() => useAppUpdate(), { wrapper });

    expect(result.current.deployment).toBe('container');
    expect(result.current.primaryAsset).toBeNull();
    expect(result.current.secondaryAssets).toEqual([]);
  });

  it('resolves a manual check to the selected available scenario', async () => {
    vi.useFakeTimers();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <DemoAppUpdateProvider scenario='available'>{children}</DemoAppUpdateProvider>
    );
    const { result } = renderHook(() => useAppUpdate(), { wrapper });

    let check = Promise.resolve();
    act(() => {
      check = result.current.checkForUpdates(true);
    });
    expect(result.current.status).toBe('checking');

    await act(async () => {
      vi.advanceTimersByTime(300);
      await check;
    });

    expect(result.current.status).toBe('available');
    expect(result.current.isDialogOpen).toBe(true);
  });
});
