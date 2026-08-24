// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { SystemInfoDialog } from '@/components/system-info-dialog';
import * as system from '@/lib/api/system';
import { SystemInfo } from '@/lib/types/api';

const mockSystemInfo: SystemInfo = {
  addresses: ['127.0.0.1:8090'],
  buildDate: '2026-08-24',
  commit: 'c793859',
  uptime: 3600,
  version: '1.0.1',
};

vi.mock('@/lib/api/system');
const mockGetSystemInfo = vi.spyOn(system, 'getSystemInfo');

describe('SystemInfoDialog', () => {
  beforeEach(() => {
    mockGetSystemInfo.mockResolvedValue(mockSystemInfo);
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('renders loading state with accessible title and description', () => {
    mockGetSystemInfo.mockImplementation(() => new Promise(() => {}));

    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <SystemInfoDialog
          open={true}
          onOpenChange={() => {}}
        />
      </SWRConfig>
    );

    expect(screen.getByText('Loading...')).toBeInTheDocument();
    expect(screen.getByText('Loading system information')).toBeInTheDocument();
  });

  it('renders system info dialog with data and description when loaded', async () => {
    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <SystemInfoDialog
          open={true}
          onOpenChange={() => {}}
        />
      </SWRConfig>
    );

    await waitFor(() => {
      expect(screen.getByText('System Information')).toBeInTheDocument();
    });

    expect(screen.getByText('Version, build date, commit, and uptime details')).toBeInTheDocument();
    expect(screen.getByText('1.0.1')).toBeInTheDocument();
    expect(screen.getByText('2026-08-24')).toBeInTheDocument();
  });
});
