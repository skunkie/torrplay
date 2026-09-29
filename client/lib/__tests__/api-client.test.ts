// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { apiFetch, clearStoredCredentials, getApiBaseUrl, setApiBaseUrlOverride } from '@/lib/api-client';

describe('apiFetch', () => {
  const originalFetch = global.fetch;

  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem('NEXT_PUBLIC_API_URL', 'http://localhost:8090');
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('includes X-Requested-With: XMLHttpRequest header on requests', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 })
    );
    global.fetch = fetchMock;

    await apiFetch('/api/v1/torrents');

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [, options] = fetchMock.mock.calls[0];
    const headers = options?.headers as Headers;
    expect(headers.get('X-Requested-With')).toBe('XMLHttpRequest');
  });

  it('performs PUT request with serialized body', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ completed_bytes: 100 }), { status: 200 })
    );
    global.fetch = fetchMock;

    const { api } = await import('@/lib/api-client');
    const result = await api.put<{ completedBytes: number }>('/api/v1/test', { testField: 123 });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, options] = fetchMock.mock.calls[0];
    expect(url).toBe('http://localhost:8090/api/v1/test');
    expect(options?.method).toBe('PUT');
    expect(options?.body).toBe(JSON.stringify({ test_field: 123 }));
    expect(result).toEqual({ completedBytes: 100 });
  });
});

describe('setApiBaseUrlOverride', () => {
  const storeCredentials = () => {
    localStorage.setItem('jwt_token', 'jwt');
    localStorage.setItem('basic_auth', 'basic');
    localStorage.setItem('playback_token', 'playback');
  };

  const storedCredentials = () => [
    localStorage.getItem('jwt_token'),
    localStorage.getItem('basic_auth'),
    localStorage.getItem('playback_token'),
  ];

  beforeEach(() => {
    localStorage.clear();
  });

  it('clears stored credentials when switching to another server', () => {
    localStorage.setItem('NEXT_PUBLIC_API_URL', 'http://old.example:8090');
    storeCredentials();

    setApiBaseUrlOverride('http://new.example:8090');

    expect(getApiBaseUrl()).toBe('http://new.example:8090');
    expect(storedCredentials()).toEqual([null, null, null]);
  });

  it('clears stored credentials when resetting to the default server', () => {
    localStorage.setItem('NEXT_PUBLIC_API_URL', 'http://old.example:8090');
    storeCredentials();

    setApiBaseUrlOverride(null);

    expect(localStorage.getItem('NEXT_PUBLIC_API_URL')).toBeNull();
    expect(storedCredentials()).toEqual([null, null, null]);
  });

  it('keeps stored credentials when the effective server is unchanged', () => {
    setApiBaseUrlOverride(getApiBaseUrl());
    storeCredentials();

    setApiBaseUrlOverride(null);

    expect(storedCredentials()).toEqual(['jwt', 'basic', 'playback']);
  });
});

describe('clearStoredCredentials', () => {
  it('removes only credential keys', () => {
    localStorage.setItem('jwt_token', 'jwt');
    localStorage.setItem('basic_auth', 'basic');
    localStorage.setItem('playback_token', 'playback');
    localStorage.setItem('NEXT_PUBLIC_API_URL', 'http://localhost:8090');

    clearStoredCredentials();

    expect(localStorage.getItem('jwt_token')).toBeNull();
    expect(localStorage.getItem('basic_auth')).toBeNull();
    expect(localStorage.getItem('playback_token')).toBeNull();
    expect(localStorage.getItem('NEXT_PUBLIC_API_URL')).toBe('http://localhost:8090');
  });
});
