// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, renderHook, waitFor } from '@testing-library/react';
import React, { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { getSettings } from '@/lib/api/settings';
import { HttpError, notifyUnauthorized } from '@/lib/api-client';
import { AuthProvider, useAuth } from '@/lib/auth-context';
import type { Auth, Settings } from '@/lib/types/api';

vi.mock('@/lib/api/settings', () => ({
  getSettings: vi.fn(),
  updateSettings: vi.fn(),
}));

vi.mock('@/lib/api/auth', () => ({
  login: vi.fn().mockResolvedValue({ accessToken: 'new-jwt-token' }),
}));

const buildTestSettings = (auth: Auth): Settings => ({
  auth,
  corsAllowedOrigins: [],
  enableDlna: false,
  enableDownloader: false,
  enableStremio: false,
  fileStoragePath: '',
  friendlyName: '',
  logLevel: 'INFO',
  logFormat: 'json',
  maxMemory: 0,
  torrentClient: {
    disableDht: false,
    disableIpv6: false,
    disablePex: false,
    disableTcp: false,
    disableUtp: false,
    downloadRateLimit: 0,
    establishedConnsPerTorrent: 100,
    halfOpenConnsPerTorrent: 25,
    maxAllocPeerRequestDataPerConn: 1048576,
    preferHeaderObfuscation: true,
    seed: true,
    torrentPeersHighWater: 300,
    torrentPeersLowWater: 50,
    totalHalfOpenConns: 100,
    uploadRateLimit: 0,
  },
  torrentTrackers: [],
});

describe('AuthContext', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
  });

  const wrapper = ({ children }: { children: ReactNode }) => (
    <AuthProvider>{children}</AuthProvider>
  );

  it('rejects authentication when auth is bearer but only basic_auth is stored in localStorage', async () => {
    localStorage.setItem('basic_auth', 'admin:password');
    vi.mocked(getSettings).mockResolvedValueOnce(
      buildTestSettings({ enabled: true, type: 'bearer' })
    );

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.auth?.type).toBe('bearer');
    expect(result.current.isAuthenticated).toBe(false);
  });

  it('rejects authentication when auth is basic but only jwt_token is stored in localStorage', async () => {
    localStorage.setItem('jwt_token', 'sample-jwt');
    vi.mocked(getSettings).mockResolvedValueOnce(
      buildTestSettings({ enabled: true, type: 'basic' })
    );

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(result.current.auth?.type).toBe('basic');
    expect(result.current.isAuthenticated).toBe(false);
  });

  it('detects switch from basic to bearer via 401 with WWW-Authenticate header and redirects to unauthenticated state', async () => {
    localStorage.setItem('basic_auth', 'admin:password');
    localStorage.setItem('playback_token', 'playback-token');

    vi.mocked(getSettings).mockResolvedValueOnce(
      buildTestSettings({ enabled: true, type: 'basic' })
    );

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => {
      expect(result.current.isAuthenticated).toBe(true);
    });

    // Simulate a background request returning 401 because server changed to bearer auth
    act(() => {
      notifyUnauthorized(
        new HttpError(401, 'Unauthorized', 'authentication failed', 'Bearer realm="TorrPlay"')
      );
    });

    expect(localStorage.getItem('basic_auth')).toBeNull();
    expect(localStorage.getItem('playback_token')).toBeNull();
    expect(result.current.auth?.type).toBe('bearer');
    expect(result.current.isAuthenticated).toBe(false);
  });

  it('detects switch from bearer to basic via 401 with WWW-Authenticate header and clears tokens', async () => {
    localStorage.setItem('jwt_token', 'jwt-token');

    vi.mocked(getSettings).mockResolvedValueOnce(
      buildTestSettings({ enabled: true, type: 'bearer' })
    );

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => {
      expect(result.current.isAuthenticated).toBe(true);
    });

    act(() => {
      notifyUnauthorized(
        new HttpError(401, 'Unauthorized', 'authentication failed', 'Basic realm="TorrPlay"')
      );
    });

    expect(localStorage.getItem('jwt_token')).toBeNull();
    expect(result.current.auth?.type).toBe('basic');
    expect(result.current.isAuthenticated).toBe(false);
  });

  it('clears stale credentials on initial mount when getSettings returns 401 with WWW-Authenticate', async () => {
    localStorage.setItem('basic_auth', 'stale:credentials');
    vi.mocked(getSettings).mockRejectedValueOnce(
      new HttpError(401, 'Unauthorized', 'invalid credentials', 'Bearer realm="TorrPlay"')
    );

    const { result } = renderHook(() => useAuth(), { wrapper });

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false);
    });

    expect(localStorage.getItem('basic_auth')).toBeNull();
    expect(result.current.auth?.enabled).toBe(true);
    expect(result.current.auth?.type).toBe('bearer');
    expect(result.current.isAuthenticated).toBe(false);
  });
});
