// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { apiFetch } from '@/lib/api-client';

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
