// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { SystemInfoDialogLayout } from '@/components/system-info-dialog-layout';

const systemInfo = {
  addresses: [
    '127.0.0.1:8090',
    '[fd12:3456:789a:1::1234]:8090',
    '[2001:db8:85a3::8a2e:370:7334]:8090',
    '192.168.1.100:8090',
  ],
  architecture: 'x64' as const,
  buildDate: '2026-09-05',
  commit: 'abcdef0',
  deployment: 'container' as const,
  os: 'linux' as const,
  uptime: 3600,
  version: '1.0.2',
};

describe('SystemInfoDialogLayout', () => {
  it('places the available version action beside the current version', () => {
    const onViewUpdate = vi.fn();
    render(
      <SystemInfoDialogLayout
        open={true}
        onOpenChange={vi.fn()}
        systemInfo={systemInfo}
        updateStatus='available'
        latestVersion='v1.0.3'
        onCheckForUpdates={vi.fn()}
        onViewUpdate={onViewUpdate}
      />
    );

    const version = screen.getByText('1.0.2');
    const versionLabel = screen.getByText('Version');
    const action = screen.getByRole('button', { name: 'v1.0.3 available' });
    expect(version.parentElement).toContainElement(action);
    expect(versionLabel).not.toHaveClass('sr-only');
    expect(versionLabel.parentElement).toHaveClass('min-h-5');
    expect(action).toHaveClass('h-6', 'text-[11px]', 'px-1.5');
    expect(screen.queryByRole('button', { name: 'View update' })).not.toBeInTheDocument();
    fireEvent.click(action);
    expect(onViewUpdate).toHaveBeenCalledOnce();
    expect(screen.getByText('Deployment')).toBeInTheDocument();
    expect(screen.getByText('Container')).toBeInTheDocument();
  });

  it('uses a generic combined action when the latest version is unavailable', () => {
    render(
      <SystemInfoDialogLayout
        open={true}
        onOpenChange={vi.fn()}
        systemInfo={systemInfo}
        updateStatus='available'
        onCheckForUpdates={vi.fn()}
        onViewUpdate={vi.fn()}
      />
    );

    expect(screen.getByRole('button', { name: 'Update available' })).toBeInTheDocument();
  });

  it('matches the compact check-again treatment before an update has been checked', () => {
    render(
      <SystemInfoDialogLayout
        open={true}
        onOpenChange={vi.fn()}
        systemInfo={systemInfo}
        updateStatus='idle'
        onCheckForUpdates={vi.fn()}
      />
    );

    expect(screen.getByRole('button', { name: 'Check for updates' }))
      .toHaveClass('h-6', 'text-[11px]', 'px-1.5', 'text-muted-foreground');
  });

  it('combines deployment and scrollable unbroken addresses in a full-width Server card', () => {
    render(
      <SystemInfoDialogLayout
        open={true}
        onOpenChange={vi.fn()}
        systemInfo={systemInfo}
      />
    );

    const deploymentCard = screen.getByText('Deployment').closest('[data-slot="card"]');
    const addressesCard = screen.getByText('Addresses').closest('[data-slot="card"]');
    const addresses = screen.getByRole('list', { name: 'Server addresses' });

    expect(deploymentCard).toBe(addressesCard);
    expect(addressesCard).toHaveClass('sm:col-span-2');
    expect(screen.getByText('Deployment').parentElement?.parentElement).toHaveClass(
      'grid-cols-1',
      'sm:grid-cols-[minmax(7rem,1fr)_minmax(0,2fr)]'
    );
    expect(addresses).toHaveAttribute('tabindex', '0');
    expect(addresses).toHaveClass(
      'max-h-[4.5rem]',
      'overflow-auto',
      'overscroll-contain',
      'font-mono'
    );
    expect(screen.getByText('[2001:db8:85a3::8a2e:370:7334]:8090'))
      .toHaveClass('w-max', 'min-w-full', 'whitespace-nowrap');
  });

  it('shows the Server card when no addresses are reported', () => {
    render(
      <SystemInfoDialogLayout
        open={true}
        onOpenChange={vi.fn()}
        systemInfo={{ ...systemInfo, addresses: [] }}
      />
    );

    expect(screen.getByText('Deployment')).toBeInTheDocument();
    expect(screen.getByText('Addresses')).toBeInTheDocument();
    expect(screen.getByText('None reported')).toBeInTheDocument();
    expect(screen.queryByRole('list', { name: 'Server addresses' })).not.toBeInTheDocument();
  });

  it('renders optional additional content after the information cards', () => {
    render(
      <SystemInfoDialogLayout
        open={true}
        onOpenChange={vi.fn()}
        systemInfo={systemInfo}
        additionalContent={<nav aria-label='Demo scenarios'>Scenario links</nav>}
      />
    );

    const scenarios = screen.getByRole('navigation', { name: 'Demo scenarios' });
    expect(scenarios.previousElementSibling).toContainElement(screen.getByText('Deployment'));
  });
});
