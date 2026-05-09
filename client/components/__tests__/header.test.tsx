// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('swr', () => ({
  default: vi.fn(() => ({ data: null, error: null, mutate: vi.fn() })),
}));

vi.mock('@/lib/api/system', () => ({
  getSystemInfo: vi.fn(),
}));

vi.mock('@/lib/auth-context', () => ({
  useAuth: vi.fn(() => ({
    isAuthenticated: false,
    auth: null,
    logout: vi.fn(),
  })),
}));

vi.mock('@/lib/live-updates-context', () => ({
  useLiveUpdates: vi.fn(() => ({
    liveUpdatesPaused: false,
    setLiveUpdatesPaused: vi.fn(),
  })),
}));

describe('Header', () => {
  const mockOnSettingsClick = vi.fn();
  const mockOnMetricsClick = vi.fn();
  const mockOnTitleSearch = vi.fn();

  it('renders the version when system info is available', async () => {
    const { default: useSWR } = await import('swr');
    const mockUseSWR = useSWR as typeof useSWR;
    mockUseSWR.mockReturnValue({
      data: { version: '1.0.0' },
      error: null,
      mutate: vi.fn(),
    });

    const { Header } = await import('@/components/header');

    render(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onTitleSearch={mockOnTitleSearch}
      />,
    );

    expect(screen.getByText('v1.0.0')).toBeInTheDocument();
  });

  it('does not render version when system info is unavailable', async () => {
    const { default: useSWR } = await import('swr');
    const mockUseSWR = useSWR as typeof useSWR;
    mockUseSWR.mockReturnValue({
      data: null,
      error: null,
      mutate: vi.fn(),
    });

    const { Header } = await import('@/components/header');

    render(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onTitleSearch={mockOnTitleSearch}
      />,
    );

    expect(screen.queryByText(/v\d+\.\d+\.\d+/)).not.toBeInTheDocument();
  });

  it('renders version when it changes after initial null state', async () => {
    const { default: useSWR } = await import('swr');
    const mockUseSWR = useSWR as typeof useSWR;
    mockUseSWR.mockReturnValue({
      data: { version: '2.5.3' },
      error: null,
      mutate: vi.fn(),
    });

    const { Header } = await import('@/components/header');

    render(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onTitleSearch={mockOnTitleSearch}
      />,
    );

    expect(screen.getByText('v2.5.3')).toBeInTheDocument();
  });
});
