// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { UpdateDialog } from '@/components/update-dialog';
import * as updateContext from '@/lib/app-update-context';

describe('UpdateDialog', () => {
  const mockSetIsDialogOpen = vi.fn();
  const mockOpenDownload = vi.fn();
  const mockDismissUpdate = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders nothing when isDialogOpen is false', () => {
    vi.spyOn(updateContext, 'useAppUpdate').mockReturnValue({
      isSupported: true,
      status: 'idle',
      currentVersion: '1.0.0',
      latestVersion: null,
      releaseTitle: null,
      releaseBody: null,
      releaseUrl: null,
      publishedAt: null,
      primaryAsset: null,
      secondaryAssets: [],
      isDialogOpen: false,
      setIsDialogOpen: mockSetIsDialogOpen,
      checkForUpdates: vi.fn(),
      dismissUpdate: mockDismissUpdate,
      isDismissed: vi.fn().mockReturnValue(false),
      openDownload: mockOpenDownload,
    });

    const { container } = render(<UpdateDialog />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing when app updates are unsupported', () => {
    vi.spyOn(updateContext, 'useAppUpdate').mockReturnValue({
      isSupported: false,
      status: 'available',
      currentVersion: '1.0.0',
      latestVersion: '1.2.0',
      releaseTitle: 'Version 1.2.0',
      releaseBody: null,
      releaseUrl: 'https://github.com/torrplay/torrplay/releases/tag/v1.2.0',
      publishedAt: null,
      primaryAsset: null,
      secondaryAssets: [],
      isDialogOpen: true,
      setIsDialogOpen: mockSetIsDialogOpen,
      checkForUpdates: vi.fn(),
      dismissUpdate: mockDismissUpdate,
      isDismissed: vi.fn().mockReturnValue(false),
      openDownload: mockOpenDownload,
    });

    const { container } = render(<UpdateDialog />);
    expect(container).toBeEmptyDOMElement();
  });

  it('defers rendering while another dialog is open', () => {
    vi.spyOn(updateContext, 'useAppUpdate').mockReturnValue({
      isSupported: true,
      status: 'available',
      currentVersion: '1.0.0',
      latestVersion: '1.2.0',
      releaseTitle: null,
      releaseBody: null,
      releaseUrl: 'https://github.com/torrplay/torrplay/releases/tag/v1.2.0',
      publishedAt: null,
      primaryAsset: null,
      secondaryAssets: [],
      isDialogOpen: true,
      setIsDialogOpen: mockSetIsDialogOpen,
      checkForUpdates: vi.fn(),
      dismissUpdate: mockDismissUpdate,
      isDismissed: vi.fn().mockReturnValue(false),
      openDownload: mockOpenDownload,
    });

    const { container } = render(<UpdateDialog deferWhile={true} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders dialog and handles actions when isDialogOpen is true', () => {
    vi.spyOn(updateContext, 'useAppUpdate').mockReturnValue({
      isSupported: true,
      status: 'available',
      currentVersion: '1.0.0',
      latestVersion: '1.2.0',
      releaseTitle: 'Version 1.2.0',
      releaseBody: 'Release notes body',
      releaseUrl: 'https://github.com/torrplay/torrplay/releases/tag/v1.2.0',
      publishedAt: '2026-09-01T00:00:00Z',
      primaryAsset: {
        name: 'TorrPlay.dmg',
        label: 'macOS Installer (.dmg)',
        url: 'https://github.com/torrplay/torrplay/releases/download/v1.2.0/TorrPlay.dmg',
        type: 'macos-dmg',
      },
      secondaryAssets: [],
      isDialogOpen: true,
      setIsDialogOpen: mockSetIsDialogOpen,
      checkForUpdates: vi.fn(),
      dismissUpdate: mockDismissUpdate,
      isDismissed: vi.fn().mockReturnValue(false),
      openDownload: mockOpenDownload,
    });

    render(<UpdateDialog />);
    expect(screen.getByText('Update Available')).toBeInTheDocument();
    expect(screen.getByText('macOS Installer (.dmg)')).toBeInTheDocument();

    const downloadBtn = screen.getByRole('button', { name: /macos installer/i });
    fireEvent.click(downloadBtn);
    expect(mockOpenDownload).toHaveBeenCalledWith(
      'https://github.com/torrplay/torrplay/releases/download/v1.2.0/TorrPlay.dmg'
    );

    const dismissBtn = screen.getByRole('button', { name: /remind me later/i });
    fireEvent.click(dismissBtn);
    expect(mockSetIsDialogOpen).toHaveBeenCalledWith(false);

    const foreverBtn = screen.getByRole('button', { name: /don't ask again/i });
    fireEvent.click(foreverBtn);
    expect(mockDismissUpdate).toHaveBeenCalledWith('1.2.0');
  });
});
