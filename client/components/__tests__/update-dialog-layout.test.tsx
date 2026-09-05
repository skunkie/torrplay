// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { UpdateDialogLayout } from '../update-dialog-layout';

describe('UpdateDialogLayout', () => {
  const defaultProps = {
    open: true,
    onOpenChange: vi.fn(),
    currentVersion: '1.0.0',
    latestVersion: '1.1.0',
    releaseTitle: 'TorrPlay 1.1.0 - Performance & Bug Fixes',
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
    onDismiss: vi.fn(),
    onDismissForever: vi.fn(),
  };

  it('renders version comparison and primary download action', () => {
    render(<UpdateDialogLayout {...defaultProps} />);

    expect(screen.getByText('Update Available')).toBeInTheDocument();
    expect(screen.getByText('v1.0.0')).toBeInTheDocument();
    expect(screen.getByText('v1.1.0')).toBeInTheDocument();
    expect(screen.getByText('Windows Desktop Installer (.exe)')).toBeInTheDocument();
  });

  it('triggers onDownloadPrimary when primary download button is clicked', () => {
    render(<UpdateDialogLayout {...defaultProps} />);

    const downloadBtn = screen.getByText('Windows Desktop Installer (.exe)');
    fireEvent.click(downloadBtn);
    expect(defaultProps.onDownloadPrimary).toHaveBeenCalled();
  });

  it('toggles other download formats and triggers onDownloadAsset', () => {
    render(<UpdateDialogLayout {...defaultProps} />);

    const toggleBtn = screen.getByText(/Other download options/i);
    fireEvent.click(toggleBtn);

    expect(screen.getByText('Windows Service Installer (.msi)')).toBeInTheDocument();
    const serviceDownloadBtn = screen.getAllByRole('button', { name: /download/i })[1];
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

    const foreverBtn = screen.getByRole('button', { name: /don't ask again/i });
    fireEvent.click(foreverBtn);
    expect(defaultProps.onDismissForever).toHaveBeenCalled();
  });
});
