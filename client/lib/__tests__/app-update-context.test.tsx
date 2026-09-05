// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, renderHook, waitFor } from '@testing-library/react';
import { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { fetchLatestRelease } from '@/lib/api/releases';
import { getSystemInfo } from '@/lib/api/system';
import { AppUpdateProvider, useAppUpdate } from '@/lib/app-update-context';
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
      buildDate: '2026-09-05',
      commit: 'abcdef0',
      uptime: 1,
      version: '1.0.0',
    });

    const wrapper = ({ children }: { children: ReactNode }) => (
      <AppUpdateProvider autoCheck={false}>{children}</AppUpdateProvider>
    );
    const { result } = renderHook(() => useAppUpdate(), { wrapper });

    await waitFor(() => expect(result.current.currentVersion).toBe('2.0.0'));
    expect(getSystemInfo).not.toHaveBeenCalled();
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
