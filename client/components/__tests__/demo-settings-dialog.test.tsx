// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React, { useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { DemoSettingsDialog } from '@/app/demo/demo-settings-dialog';
import { AuthContext } from '@/lib/auth-context';
import { Settings } from '@/lib/types/api';

const hoisted = vi.hoisted(() => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  toastInfo: vi.fn(),
  updateSettings: vi.fn(),
}));

vi.mock('sonner', () => ({
  toast: {
    success: hoisted.toastSuccess,
    error: hoisted.toastError,
    info: hoisted.toastInfo,
  },
}));

vi.mock('@/lib/api-client', () => ({
  getApiBaseUrl: () => 'http://localhost:8090',
}));

const buildDemoSettings = (overrides: Partial<Settings> = {}) => ({
  auth: { enabled: false, type: 'basic' as const, username: '', password: '' },
  enableDlna: false,
  enableDownloader: false,
  fileStoragePath: '',
  friendlyName: 'TorrPlay',
  logLevel: 'INFO',
  logFormat: 'text' as const,
  maxMemory: 64,
  torrentClient: {
    disableDht: false,
    disableIpv6: true,
    disablePex: false,
    disableTcp: false,
    disableUtp: false,
    downloadRateLimit: 0,
    establishedConnsPerTorrent: 50,
    preferHeaderObfuscation: false,
    seed: false,
    torrentPeersHighWater: 500,
    uploadRateLimit: 0,
  },
  torrentTrackers: [],
  ...overrides,
});

function DemoAuthWrapper({ children, settings }: { children: React.ReactNode, settings: Settings }) {
  const [settingsState, setSettingsState] = useState<Settings>(settings);

  const updateSettings = async (newSettings: Partial<Settings>) => {
    hoisted.updateSettings(newSettings);
    setSettingsState((prev: Settings) => {
      const updated = { ...prev, ...newSettings };
      if (newSettings.auth) {
        updated.auth = { ...prev.auth, ...newSettings.auth };
      }
      return updated;
    });
  };

  return (
    <AuthContext.Provider value={{
      settings: settingsState,
      updateSettings,
      auth: null,
      isAuthenticated: true,
      isLoading: false,
      login: vi.fn(),
      logout: vi.fn(),
    }}>
      {children}
    </AuthContext.Provider>
  );
}

let currentSettings = buildDemoSettings();

function setupMocks() {
  vi.clearAllMocks();
  currentSettings = buildDemoSettings();
}

function selectCombobox(combobox: HTMLElement, optionText: string) {
  fireEvent.click(combobox);
  const option = screen.getByRole('option', { name: new RegExp(`^${optionText}$`, 'i') });
  fireEvent.click(option);
}

describe('DemoSettingsDialog', () => {
  beforeEach(() => {
    currentSettings = buildDemoSettings();
  });

  it('renders the dialog when open', async () => {
    setupMocks();
    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={vi.fn()} />
      </DemoAuthWrapper>,
    );
    expect(screen.getByText('Settings')).toBeInTheDocument();
  });

  it('does not render the dialog when closed', async () => {
    setupMocks();
    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={false}
          onOpenChange={vi.fn()} />
      </DemoAuthWrapper>,
    );
    expect(screen.queryByText('Settings')).not.toBeInTheDocument();
  });

  it('populates fields from demo settings on load', async () => {
    setupMocks();
    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={vi.fn()} />
      </DemoAuthWrapper>,
    );

    const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
    expect(logLevelSelect).toHaveTextContent(/INFO/i);

    const logFormatSelect = screen.getByRole('combobox', { name: /log format/i });
    expect(logFormatSelect).toHaveTextContent(/Text/i);
  });

  it('Reset button reverts local state changes', async () => {
    setupMocks();

    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={vi.fn()} />
      </DemoAuthWrapper>,
    );

    await waitFor(() => {
      const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
      expect(logLevelSelect).toHaveTextContent(/INFO/i);
    });

    const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
    selectCombobox(logLevelSelect, 'ERROR');

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent(/ERROR/i);
    });

    const resetButton = screen.getByRole('button', { name: /Reset$/ });
    fireEvent.click(resetButton);

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent(/INFO/i);
    });

    expect(hoisted.updateSettings).not.toHaveBeenCalled();
  });

  it('Reset to Defaults button resets all values without calling updateSettings', async () => {
    setupMocks();

    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={vi.fn()} />
      </DemoAuthWrapper>,
    );

    await waitFor(() => {
      const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
      expect(logLevelSelect).toHaveTextContent(/INFO/i);
    });

    const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
    selectCombobox(logLevelSelect, 'ERROR');

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent(/ERROR/i);
    });

    const logFormatSelect = screen.getByRole('combobox', { name: /log format/i });
    selectCombobox(logFormatSelect, 'text');

    await waitFor(() => {
      expect(logFormatSelect).toHaveTextContent(/Text/i);
    });

    const resetToDefaultsButton = screen.getByRole('button', { name: /reset to defaults/i });
    fireEvent.click(resetToDefaultsButton);

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent('INFO');
    });

    await waitFor(() => {
      expect(logFormatSelect).toHaveTextContent(/Text/i);
    });

    await waitFor(() => {
      expect(hoisted.toastSuccess).toHaveBeenCalledWith(
        'Settings reset to defaults',
        { description: 'Demo mode - settings not actually saved' },
      );
    });

    expect(hoisted.updateSettings).not.toHaveBeenCalled();
  });

  it('Reset shows toast', async () => {
    setupMocks();

    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={vi.fn()} />
      </DemoAuthWrapper>,
    );

    const resetButton = screen.getByRole('button', { name: /Reset$/ });
    fireEvent.click(resetButton);

    await waitFor(() => {
      expect(hoisted.toastSuccess).toHaveBeenCalledWith(
        'Settings reset',
        { description: 'Demo mode - settings not actually saved' },
      );
    });
  });

  it('Save button calls updateSettings and closes dialog', async () => {
    setupMocks();

    const mockOnOpenChange = vi.fn();

    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={mockOnOpenChange} />
      </DemoAuthWrapper>,
    );

    const saveButton = screen.getByRole('button', { name: /save changes/i });
    fireEvent.click(saveButton);

    await waitFor(() => {
      expect(hoisted.updateSettings).toHaveBeenCalled();
    });

    await waitFor(() => {
      expect(mockOnOpenChange).toHaveBeenCalledWith(false);
    });
  });

  it('Reset and Reset to Defaults are visually distinct buttons', async () => {
    setupMocks();

    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={vi.fn()} />
      </DemoAuthWrapper>,
    );

    const resetButton = screen.getByRole('button', { name: /Reset$/ });
    const resetToDefaultsButton = screen.getByRole('button', { name: /reset to defaults/i });

    expect(resetButton).toBeInTheDocument();
    expect(resetToDefaultsButton).toBeInTheDocument();
    expect(resetButton).toHaveTextContent('Reset');
    expect(resetToDefaultsButton).toHaveTextContent('Reset to Defaults');
  });

  it('populates DLNA switch and friendlyName from settings', async () => {
    setupMocks();
    currentSettings.enableDlna = true;
    currentSettings.friendlyName = 'MyMediaServer';

    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={vi.fn()} />
      </DemoAuthWrapper>,
    );

    const dlnaSwitch = screen.getByRole('switch', { name: /dlna/i });
    expect(dlnaSwitch).toBeChecked();

    const friendlyNameInput = screen.getByRole('textbox', { name: /friendly name/i });
    expect(friendlyNameInput).toHaveValue('MyMediaServer');
  });

  it('save sends formatted values to updateSettings', async () => {
    setupMocks();
    currentSettings.enableDlna = true;
    currentSettings.friendlyName = 'TestName';
    currentSettings.logLevel = 'DEBUG';
    currentSettings.logFormat = 'json';
    currentSettings.maxMemory = 512 * 1024 * 1024;

    const mockOnOpenChange = vi.fn();

    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={mockOnOpenChange} />
      </DemoAuthWrapper>,
    );

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /save changes/i })).toBeInTheDocument();
    });

    const saveButton = screen.getByRole('button', { name: /save changes/i });
    fireEvent.click(saveButton);

    await waitFor(() => {
      expect(hoisted.updateSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          enableDlna: true,
          friendlyName: 'TestName',
          logLevel: 'DEBUG',
          logFormat: 'json',
          maxMemory: 536870912,
        }),
      );
    });

    expect(mockOnOpenChange).toHaveBeenCalledWith(false);
  });

  it('Reset reverts to last saved settings, not defaults', async () => {
    setupMocks();
    const mockOnOpenChange = vi.fn();

    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={mockOnOpenChange} />
      </DemoAuthWrapper>,
    );

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /save changes/i })).toBeInTheDocument();
    });

    const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
    selectCombobox(logLevelSelect, 'ERROR');

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent(/ERROR/i);
    });

    const logFormatSelect = screen.getByRole('combobox', { name: /log format/i });
    selectCombobox(logFormatSelect, 'json');

    await waitFor(() => {
      expect(logFormatSelect).toHaveTextContent(/JSON/i);
    });

    const resetButton = screen.getByRole('button', { name: /Reset$/ });
    fireEvent.click(resetButton);

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent(/INFO/i);
      expect(logFormatSelect).toHaveTextContent(/text/i);
    });

    expect(hoisted.updateSettings).not.toHaveBeenCalled();
  });

  it('dialog closes without saving when closed with unsaved changes', async () => {
    setupMocks();
    const mockOnOpenChange = vi.fn();

    render(
      <DemoAuthWrapper settings={currentSettings}>
        <DemoSettingsDialog open={true}
          onOpenChange={mockOnOpenChange} />
      </DemoAuthWrapper>,
    );

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: /log level/i })).toBeInTheDocument();
    });

    const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
    selectCombobox(logLevelSelect, 'ERROR');

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent(/ERROR/i);
    });

    expect(hoisted.updateSettings).not.toHaveBeenCalled();
  });
});
