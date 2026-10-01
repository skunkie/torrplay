// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { afterEach, describe, expect, it, vi } from 'vitest';

import { getSystemLogs } from '@/lib/api/system';

describe('getSystemLogs', () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('preserves structured field names and colliding keys', async () => {
    localStorage.setItem('NEXT_PUBLIC_API_URL', 'http://localhost:8090');
    const entries = [{
      time: '2026-09-22T10:00:00Z',
      level: 'ERROR',
      message: 'piece failed',
      data: { piece_index: 4, foo_bar: 'original', fooBar: 'other' },
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(entries), { status: 200 }));
    global.fetch = fetchMock;

    expect(await getSystemLogs()).toEqual(entries);
    expect(fetchMock).toHaveBeenCalledWith('http://localhost:8090/api/system/logs', expect.objectContaining({ method: 'GET' }));
  });
});
