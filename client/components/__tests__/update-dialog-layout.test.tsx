// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { DOCKER_UPDATE_GUIDE_URL, UpdateDialogLayout } from '../update-dialog-layout';

describe('UpdateDialogLayout', () => {
  const defaultProps = {
    open: true,
    onOpenChange: vi.fn(),
    deployment: 'native' as const,
    latestVersion: '1.1.0',
    releaseBody: '- Added automatic updates\n- Improved streaming stability',
    releaseUrl: 'https://github.com/torrplay/torrplay/releases/tag/v1.1.0',
    publishedAt: '2026-09-01T12:00:00Z',
    primaryAsset: {
      name: 'torrplay-client_1.1.0_x64-setup.exe',
      label: 'Windows Desktop Installer (.exe)',
      url: 'https://github.com/torrplay/torrplay/releases/download/v1.1.0/setup.exe',
      size: 25000000,
      type: 'desktop-setup' as const,
    },
    secondaryAssets: [
      {
        name: 'TorrPlay-1.1.0-x64.msi',
        label: 'Windows Service Installer (.msi)',
        url: 'https://github.com/torrplay/torrplay/releases/download/v1.1.0/service.msi',
        size: 15000000,
        type: 'windows-service' as const,
      },
    ],
    onDownloadPrimary: vi.fn(),
    onDownloadAsset: vi.fn(),
    onCopy: vi.fn(),
    onDismiss: vi.fn(),
    onDismissForever: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders a versioned heading, platform icon, and primary download action', () => {
    render(<UpdateDialogLayout {...defaultProps} />);

    expect(screen.getByRole('heading', { name: 'TorrPlay v1.1.0 is available' })).toBeInTheDocument();
    expect(document.querySelector('.lucide-circle-arrow-down')?.parentElement)
      .toHaveClass('bg-muted', 'text-muted-foreground');
    expect(document.querySelector('.lucide-monitor')?.parentElement)
      .toHaveClass('bg-muted', 'text-muted-foreground');
    expect(screen.queryByText('Update available')).not.toBeInTheDocument();
    expect(screen.getByText('Released Sep 1, 2026')).toBeInTheDocument();
    expect(screen.queryByText('Download the package for this system to update TorrPlay.'))
      .not.toBeInTheDocument();
    expect(screen.queryByText('v1.0.0')).not.toBeInTheDocument();
    expect(screen.getByText('Windows Desktop Installer (.exe)')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Download update' })).toBeInTheDocument();
  });

  it('renders safe GitHub-flavored Markdown in release notes collapsed by default', async () => {
    const releaseBody = [
      '## Highlights',
      '- **Faster** startup with `cached data`.',
      '',
      '| Feature | Status |',
      '| --- | --- |',
      '| Updates | Ready |',
      '',
      '[Changelog](https://example.com/changelog)',
      '',
      '<span data-testid="raw-html">unsafe</span>',
    ].join('\n');
    const { container } = render(
      <UpdateDialogLayout {...defaultProps}
        releaseBody={releaseBody} />
    );

    expect(screen.queryByRole('heading', { name: 'Highlights' })).not.toBeInTheDocument();
    expect(screen.getByText('Released Sep 1, 2026')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /release notes/i }));

    expect(await screen.findByRole('heading', { name: 'Highlights' })).toBeInTheDocument();
    expect(screen.getByText('Faster').tagName).toBe('STRONG');
    expect(screen.getByText('cached data')).toBeInTheDocument();
    expect(screen.getByRole('table')).toBeInTheDocument();
    expect(container.querySelector('[data-testid="raw-html"]')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('link', { name: 'Changelog' }));
    expect(defaultProps.onDownloadAsset).toHaveBeenCalledWith('https://example.com/changelog');
  });

  it('triggers onDownloadPrimary when primary download button is clicked', () => {
    render(<UpdateDialogLayout {...defaultProps} />);

    const downloadBtn = screen.getByRole('button', { name: 'Download update' });
    fireEvent.click(downloadBtn);
    expect(defaultProps.onDownloadPrimary).toHaveBeenCalled();
  });

  it('toggles other download formats and triggers onDownloadAsset', () => {
    render(<UpdateDialogLayout {...defaultProps} />);

    const toggleBtn = screen.getByText(/Other download formats/i);
    fireEvent.click(toggleBtn);

    expect(screen.getByText('Windows Service Installer (.msi)')).toBeInTheDocument();
    const serviceDownloadBtn = screen.getByRole('button', {
      name: 'Download Windows Service Installer (.msi)',
    });
    fireEvent.click(serviceDownloadBtn);
    expect(defaultProps.onDownloadAsset).toHaveBeenCalledWith(
      'https://github.com/torrplay/torrplay/releases/download/v1.1.0/service.msi'
    );
  });

  it('triggers dismiss and dismiss forever callbacks', () => {
    render(<UpdateDialogLayout {...defaultProps} />);

    const dismissBtn = screen.getByRole('button', { name: /remind me later/i });
    fireEvent.click(dismissBtn);
    expect(defaultProps.onDismiss).toHaveBeenCalled();

    const foreverBtn = screen.getByRole('button', { name: /skip this version/i });
    fireEvent.click(foreverBtn);
    expect(defaultProps.onDismissForever).toHaveBeenCalled();
    expect(foreverBtn).toHaveClass('mr-auto');
    expect(foreverBtn.parentElement?.firstElementChild).toBe(foreverBtn);
  });

  it.each(['1.1.0', 'v1.1.0', 'vv1.1.0'])(
    'renders container guidance with one leading v for %s',
    latestVersion => {
      render(
        <UpdateDialogLayout
          {...defaultProps}
          deployment='container'
          latestVersion={latestVersion}
        />
      );

      expect(screen.getByText(/existing configuration and data remain in place/i)).toBeInTheDocument();
      expect(screen.getByText((_, element) => (
        element?.tagName === 'CODE'
          && element.textContent === 'docker compose pull\ndocker compose up -d'
      ))).toBeInTheDocument();
      expect(screen.getByText('docker pull ghcr.io/torrplay/torrplay:v1.1.0')).toBeInTheDocument();
      expect(screen.queryByText('For configurations that use the latest image tag.'))
        .not.toBeInTheDocument();
      expect(screen.queryByText(/version-pinned or custom setups/i)).not.toBeInTheDocument();
      expect(screen.queryByText('Windows Desktop Installer (.exe)')).not.toBeInTheDocument();
      expect(screen.queryByText(/Other download formats/i)).not.toBeInTheDocument();
      expect(screen.queryByText('Pull the latest image and recreate the container.'))
        .not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: /release notes/i })).toHaveAttribute('aria-expanded', 'false');
    }
  );

  it('copies Docker commands and opens the documentation root', () => {
    render(
      <UpdateDialogLayout
        {...defaultProps}
        deployment='container'
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Copy compose commands' }));
    expect(defaultProps.onCopy).toHaveBeenCalledWith(
      'docker compose pull\ndocker compose up -d',
      'Compose commands'
    );

    fireEvent.click(screen.getByRole('button', { name: 'Copy docker pull command' }));
    expect(defaultProps.onCopy).toHaveBeenCalledWith(
      'docker pull ghcr.io/torrplay/torrplay:v1.1.0',
      'Docker pull command'
    );

    expect(DOCKER_UPDATE_GUIDE_URL).toBe('https://torrplay.github.io');
    fireEvent.click(screen.getByRole('button', { name: 'Open documentation' }));
    expect(defaultProps.onDownloadAsset).toHaveBeenCalledWith(DOCKER_UPDATE_GUIDE_URL);
  });
});
