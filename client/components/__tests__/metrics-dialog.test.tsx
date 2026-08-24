// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { MetricsDialog } from '@/components/metrics-dialog';
import * as stats from '@/lib/api/stats';
import * as system from '@/lib/api/system';
import { MemoryStats, SystemMetrics } from '@/lib/types/api';

const memoryStats: MemoryStats = {
  activeTorrents: 2,
  maxMemory: 1024 * 1024 * 1024,
  totalPieces: 100,
  usedMemory: 512 * 1024 * 1024,
};

const systemMetrics: SystemMetrics = {
  activeTorrents: 2,
  downloadSpeed: 123456,
  uploadSpeed: 12345,
};

const emptySystemMetrics: SystemMetrics = {
  activeTorrents: 0,
  downloadSpeed: 0,
  uploadSpeed: 0,
};

vi.mock('@/lib/api/stats');
const mockGetMemoryStats = vi.spyOn(stats, 'getMemoryStats');

vi.mock('@/lib/api/system');
const mockGetSystemMetrics = vi.spyOn(system, 'getSystemMetrics');

describe('MetricsDialog', () => {
  beforeEach(() => {
    mockGetMemoryStats.mockResolvedValue(memoryStats);
    mockGetSystemMetrics.mockResolvedValue(systemMetrics);
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('should display "0 Bytes/s" when speeds are zero', async () => {
    mockGetSystemMetrics.mockResolvedValue(emptySystemMetrics);

    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <MetricsDialog open={true}
          onOpenChange={() => {
          }} />
      </SWRConfig>
    );

    await waitFor(() => {
      expect(screen.getByText('System Metrics')).toBeInTheDocument();
    });

    const speeds = screen.getAllByText(/0 Bytes\/s/);
    expect(speeds).toHaveLength(2);
  });

  it('should render the metrics dialog with the correct data', async () => {
    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <MetricsDialog open={true}
          onOpenChange={() => {
          }} />
      </SWRConfig>
    );

    await waitFor(() => {
      expect(screen.getByText('System Metrics')).toBeInTheDocument();
    });

    expect(screen.getByText('512 MB')).toBeInTheDocument();
    expect(screen.getByText('of 1 GB')).toBeInTheDocument();

    expect(screen.getByText('Streaming Torrents')).toBeInTheDocument();
    expect(screen.getByText('Loaded Torrents')).toBeInTheDocument();
    const torrentCounts = screen.getAllByText('2');
    expect(torrentCounts).toHaveLength(2);

    expect(screen.getByText('100')).toBeInTheDocument();
    expect(screen.getByText(/120.56 KB\/s/)).toBeInTheDocument();
    expect(screen.getByText(/12.06 KB\/s/)).toBeInTheDocument();
  });

  it('should render loading state with accessible title and description', () => {
    mockGetMemoryStats.mockImplementation(() => new Promise(() => {}));
    mockGetSystemMetrics.mockImplementation(() => new Promise(() => {}));

    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <MetricsDialog open={true}
          onOpenChange={() => {}} />
      </SWRConfig>
    );

    expect(screen.getByText('Loading...')).toBeInTheDocument();
    expect(screen.getByText('Loading system metrics')).toBeInTheDocument();
  });
});
