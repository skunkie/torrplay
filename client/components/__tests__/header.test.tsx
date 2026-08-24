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
    const mockUseSWR = vi.mocked(useSWR);
    mockUseSWR.mockReturnValue({
      data: { version: '1.0.0' },
      error: null,
      mutate: vi.fn(),
      isLoading: false,
      isValidating: false,
    });

    const { Header } = await import('@/components/header');

    render(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onSystemInfoClick={vi.fn()}
        onTitleSearch={mockOnTitleSearch}
      />,
    );

    expect(screen.getByText('v1.0.0')).toBeInTheDocument();
  });

  it('does not render version when system info is unavailable', async () => {
    const { default: useSWR } = await import('swr');
    const mockUseSWR = vi.mocked(useSWR);
    mockUseSWR.mockReturnValue({
      data: null,
      error: null,
      mutate: vi.fn(),
      isLoading: false,
      isValidating: false,
    });

    const { Header } = await import('@/components/header');

    render(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onSystemInfoClick={vi.fn()}
        onTitleSearch={mockOnTitleSearch}
      />,
    );

    expect(screen.queryByText(/v\d+\.\d+\.\d+/)).not.toBeInTheDocument();
  });

  it('renders version when it changes after initial null state', async () => {
    const { default: useSWR } = await import('swr');
    const mockUseSWR = vi.mocked(useSWR);
    mockUseSWR.mockReturnValue({
      data: { version: '2.5.3' },
      error: null,
      mutate: vi.fn(),
      isLoading: false,
      isValidating: false,
    });

    const { Header } = await import('@/components/header');

    render(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onSystemInfoClick={vi.fn()}
        onTitleSearch={mockOnTitleSearch}
      />
    );

    expect(screen.getByText('v2.5.3')).toBeInTheDocument();
  });

  it('calls onSettingsClick, onMetricsClick, and onSystemInfoClick when buttons are clicked', async () => {
    const { Header } = await import('@/components/header');
    const onSettingsClick = vi.fn();
    const onMetricsClick = vi.fn();
    const onSystemInfoClick = vi.fn();

    render(
      <Header
        homeHref='/'
        onSettingsClick={onSettingsClick}
        onMetricsClick={onMetricsClick}
        onSystemInfoClick={onSystemInfoClick}
        onTitleSearch={mockOnTitleSearch}
      />
    );

    const { fireEvent } = await import('@testing-library/react');
    const settingsBtn = screen.getByRole('button', { name: 'Settings' });
    fireEvent.click(settingsBtn);
    expect(onSettingsClick).toHaveBeenCalled();

    const metricsBtn = screen.getByRole('button', { name: 'Metrics' });
    fireEvent.click(metricsBtn);
    expect(onMetricsClick).toHaveBeenCalled();

    const systemInfoBtn = screen.getByRole('button', { name: 'System Info' });
    fireEvent.click(systemInfoBtn);
    expect(onSystemInfoClick).toHaveBeenCalled();
  });

  it('applies inert attribute to header element when inert is true', async () => {
    const { Header } = await import('@/components/header');
    render(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onSystemInfoClick={vi.fn()}
        onTitleSearch={mockOnTitleSearch}
        inert={true}
      />
    );

    const header = document.querySelector('header');
    expect(header).toBeInTheDocument();
    expect(header).toHaveAttribute('inert');
  });
});
