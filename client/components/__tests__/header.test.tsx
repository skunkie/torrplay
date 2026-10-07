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

  it('includes mobile safe-area insets and responsive typography classes', async () => {
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

    const header = document.querySelector('header');
    expect(header).toHaveClass('pt-[env(safe-area-inset-top)]');
    expect(header).toHaveClass('pl-[env(safe-area-inset-left)]');
    expect(header).toHaveClass('pr-[env(safe-area-inset-right)]');

    const title = screen.getByRole('heading', { level: 1 });
    expect(title).toHaveClass('text-xl');
    expect(title).toHaveClass('xs:text-2xl');
    expect(title).toHaveClass('sm:text-3xl');
  });

  it('synchronizes desktop and mobile search inputs when typed', async () => {
    const { Header } = await import('@/components/header');
    const onTitleSearch = vi.fn();
    const { fireEvent } = await import('@testing-library/react');

    render(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onSystemInfoClick={vi.fn()}
        onTitleSearch={onTitleSearch}
      />
    );

    const searchInputs = screen.getAllByRole('searchbox', { name: 'Search by title' });
    expect(searchInputs.length).toBe(2);

    fireEvent.change(searchInputs[0], { target: { value: 'Inception' } });
    expect(onTitleSearch).toHaveBeenCalledWith('Inception');
    expect((searchInputs[0] as HTMLInputElement).value).toBe('Inception');
    expect((searchInputs[1] as HTMLInputElement).value).toBe('Inception');
  });

  it('reflects controlled searchQuery prop in both inputs', async () => {
    const { Header } = await import('@/components/header');

    const { rerender } = render(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onSystemInfoClick={vi.fn()}
        onTitleSearch={mockOnTitleSearch}
        searchQuery='Matrix'
      />
    );

    const searchInputs = screen.getAllByRole('searchbox', { name: 'Search by title' });
    expect((searchInputs[0] as HTMLInputElement).value).toBe('Matrix');
    expect((searchInputs[1] as HTMLInputElement).value).toBe('Matrix');

    rerender(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onSystemInfoClick={vi.fn()}
        onTitleSearch={mockOnTitleSearch}
        searchQuery='Interstellar'
      />
    );

    expect((searchInputs[0] as HTMLInputElement).value).toBe('Interstellar');
    expect((searchInputs[1] as HTMLInputElement).value).toBe('Interstellar');
  });

  it('handles scroll hiding on mobile and restores visibility on desktop resize', async () => {
    const { Header } = await import('@/components/header');
    const { fireEvent } = await import('@testing-library/react');

    window.innerWidth = 400;
    window.scrollY = 0;

    render(
      <Header
        homeHref='/'
        onSettingsClick={mockOnSettingsClick}
        onMetricsClick={mockOnMetricsClick}
        onSystemInfoClick={vi.fn()}
        onTitleSearch={mockOnTitleSearch}
      />
    );

    const header = document.querySelector('header')!;
    expect(header).not.toHaveAttribute('aria-hidden');

    // Scroll down on mobile past threshold
    window.scrollY = 150;
    fireEvent.scroll(window);

    expect(header).toHaveAttribute('aria-hidden', 'true');
    expect(header).toHaveClass('-translate-y-full');

    // Resize to desktop (>= 768px)
    window.innerWidth = 1024;
    fireEvent(window, new Event('resize'));

    expect(header).not.toHaveAttribute('aria-hidden');
    expect(header).toHaveClass('translate-y-0');
  });
});
