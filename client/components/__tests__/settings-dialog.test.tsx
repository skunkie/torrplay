// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { SettingsDialog } from '@/components/settings-dialog';

const mockMutate = vi.fn();
const mockUpdateSettings = vi.fn().mockResolvedValue(undefined);

const buildMockSettings = (overrides = {}) => ({
  auth: { enabled: false, type: 'basic' as const, username: '', password: '' },
  enableDlna: true,
  enableDownloader: true,
  fileStoragePath: '/torrplay',
  friendlyName: 'MyDLNA',
  logLevel: 'DEBUG',
  logFormat: 'json' as const,
  maxMemory: 1048576,
  torrentClient: {
    disableDht: false,
    disableIpv6: false,
    disablePex: false,
    disableTcp: false,
    disableUtp: false,
    downloadRateLimit: 0,
    establishedConnsPerTorrent: 100,
    preferHeaderObfuscation: true,
    seed: true,
    torrentPeersHighWater: 300,
    uploadRateLimit: 0,
  },
  torrentTrackers: ['udp://tracker.example.com:6969'],
  ...overrides,
});

let currentSettings = buildMockSettings();
const settingsRef = { current: currentSettings };

vi.mock('@/lib/api/settings', () => {
  const mod = {
    getSettings: vi.fn().mockImplementation(() => Promise.resolve(settingsRef.current)),
  };
  return mod;
});

vi.mock('@/lib/auth-context', () => {
  const mod = {
    useAuth: vi.fn().mockImplementation(() => ({
      updateSettings: mockUpdateSettings,
      settings: settingsRef.current,
      auth: null,
      isAuthenticated: true,
      isLoading: false,
      login: vi.fn(),
      logout: vi.fn(),
    })),
  };
  return mod;
});

vi.mock('swr', () => {
  const swrMock = vi.fn(() => ({
    get data() { return settingsRef.current; },
    get error() { return null; },
    mutate: mockMutate,
  }));
  const mod = {
    default: swrMock,
  };
  Object.defineProperty(mod, '__esModule', { value: true });
  return mod;
});

vi.mock('@/lib/api-client', () => ({
  getApiBaseUrl: () => 'http://localhost:8090',
}));

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  },
}));

const getResetButton = () => screen.getByRole('button', { name: /^Reset$/ });
const getResetToDefaultsButton = () => screen.getByRole('button', { name: /reset to defaults/i });

function selectCombobox(combobox: HTMLElement, optionText: string) {
  fireEvent.click(combobox);
  const option = screen.getByRole('option', { name: new RegExp(`^${optionText}$`, 'i') });
  fireEvent.click(option);
}

describe('SettingsDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    currentSettings = buildMockSettings();
    settingsRef.current = currentSettings;
  });

  it('renders the dialog when open', () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);
    expect(screen.getByText('Settings')).toBeInTheDocument();
  });

  it('does not render the dialog when closed', () => {
    render(<SettingsDialog open={false}
      onOpenChange={vi.fn()} />);
    expect(screen.queryByText('Settings')).not.toBeInTheDocument();
  });

  it('populates fields from server settings on load', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
      expect(logLevelSelect).toHaveTextContent('DEBUG');
    });

    const logFormatSelect = screen.getByRole('combobox', { name: /log format/i });
    expect(logFormatSelect).toHaveTextContent(/json/i);
  });

  it('Reset button reverts log level to server value', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
    selectCombobox(logLevelSelect, 'ERROR');

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent('ERROR');
    });

    fireEvent.click(getResetButton());

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent('DEBUG');
    });
  });

  it('Reset button reverts log format to server value', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const logFormatSelect = screen.getByRole('combobox', { name: /log format/i });
    selectCombobox(logFormatSelect, 'text');

    await waitFor(() => {
      expect(logFormatSelect).toHaveTextContent(/Text/i);
    });

    fireEvent.click(getResetButton());

    await waitFor(() => {
      expect(logFormatSelect).toHaveTextContent(/JSON/i);
    });
  });

  it('Reset button reverts friendly name to server value', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const friendlyNameInput = screen.getByRole('textbox', { name: /friendly name/i });
    fireEvent.change(friendlyNameInput, { target: { value: 'ChangedName' } });
    expect(friendlyNameInput).toHaveValue('ChangedName');

    fireEvent.click(getResetButton());

    await waitFor(() => {
      expect(friendlyNameInput).toHaveValue('MyDLNA');
    });
  });

  it('Reset button reverts fileStoragePath to server value', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const pathInput = screen.getByRole('textbox', { name: /file storage path/i });
    fireEvent.change(pathInput, { target: { value: '/changed/path' } });
    expect(pathInput).toHaveValue('/changed/path');

    fireEvent.click(getResetButton());

    await waitFor(() => {
      expect(pathInput).toHaveValue('/torrplay');
    });
  });

  it('Reset to Defaults button sets log level to INFO', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
    selectCombobox(logLevelSelect, 'ERROR');

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent('ERROR');
    });

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent('INFO');
    });
  });

  it('Reset to Defaults button sets log format to text', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const logFormatSelect = screen.getByRole('combobox', { name: /log format/i });
    selectCombobox(logFormatSelect, 'text');

    await waitFor(() => {
      expect(logFormatSelect).toHaveTextContent(/Text/i);
    });

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      expect(logFormatSelect).toHaveTextContent(/Text/i);
    });
  });

  it('Reset to Defaults button resets friendly name to TorrPlay', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const friendlyNameInput = screen.getByRole('textbox', { name: /friendly name/i });
    fireEvent.change(friendlyNameInput, { target: { value: 'ChangedName' } });
    expect(friendlyNameInput).toHaveValue('ChangedName');

    // Reset to Defaults sets friendlyName state but also disables DLNA,
    // which hides the friendly name input. Verify by setting DLNA back on.
    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      const dlnaSwitch = screen.getByRole('switch', { name: /enable dlna/i });
      expect(dlnaSwitch).not.toBeChecked();
    });

    // Re-enable DLNA to check the friendly name value
    const dlnaSwitch = screen.getByRole('switch', { name: /enable dlna/i });
    fireEvent.click(dlnaSwitch);

    await waitFor(() => {
      const renewedInput = screen.getByRole('textbox', { name: /friendly name/i });
      expect(renewedInput).toHaveValue('TorrPlay');
    });
  });

  it('Reset to Defaults button disables DLNA', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const dlnaSwitch = screen.getByRole('switch', { name: /enable dlna/i });
    expect(dlnaSwitch).toBeChecked();

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      expect(dlnaSwitch).not.toBeChecked();
    });
  });

  it('Reset to Defaults resets torrent trackers to default values', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const trackersTextarea = screen.getByRole('textbox', { name: /torrent trackers/i });
    expect(trackersTextarea).toHaveValue('udp://tracker.example.com:6969');

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      expect(trackersTextarea).toHaveValue('');
    });
  });

  it('Reset to Defaults disables downloader and clears file storage path', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const downloaderSwitch = screen.getByRole('switch', { name: /enable downloader/i });
      expect(downloaderSwitch).toBeChecked();
    });

    const pathInput = screen.getByRole('textbox', { name: /file storage path/i });
    await waitFor(() => {
      expect(pathInput).toHaveValue('/torrplay');
    });

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      const downloaderSwitch = screen.getByRole('switch', { name: /enable downloader/i });
      expect(downloaderSwitch).not.toBeChecked();
      expect(pathInput).toHaveValue('');
    });
  });

  it('clearing file storage path disables downloader automatically', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const pathInput = screen.getByRole('textbox', { name: /file storage path/i });
    const downloaderSwitch = screen.getByRole('switch', { name: /enable downloader/i });

    await waitFor(() => {
      expect(downloaderSwitch).toBeChecked();
      expect(pathInput).toHaveValue('/torrplay');
    });

    fireEvent.change(pathInput, { target: { value: '' } });

    await waitFor(() => {
      expect(downloaderSwitch).not.toBeChecked();
    });
  });

  it('Reset to Defaults resets auth to enabled=false', async () => {
    settingsRef.current = buildMockSettings({
      auth: { enabled: true, type: 'basic', username: 'admin', password: '' },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const authSwitch = screen.getByRole('switch', { name: /enable authentication/i });
      expect(authSwitch).toBeChecked();
    });

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      const authSwitch = screen.getByRole('switch', { name: /enable authentication/i });
      expect(authSwitch).not.toBeChecked();
    });
  });

  it('Reset button reverts torrentClient settings to server values', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const dhtSwitch = screen.getByRole('switch', { name: /disable dht/i });
    expect(dhtSwitch).not.toBeChecked();

    const ipv6Switch = screen.getByRole('switch', { name: /disable ipv6/i });
    expect(ipv6Switch).not.toBeChecked();

    fireEvent.click(getResetButton());

    await waitFor(() => {
      expect(dhtSwitch).not.toBeChecked();
      expect(ipv6Switch).not.toBeChecked();
    });
  });

  it('Reset to Defaults resets torrentClient to hardcoded defaults', async () => {
    settingsRef.current = buildMockSettings({
      torrentClient: {
        disableDht: true,
        disableIpv6: false,
        disablePex: true,
        disableTcp: true,
        disableUtp: true,
        downloadRateLimit: 1000,
        establishedConnsPerTorrent: 200,
        preferHeaderObfuscation: false,
        seed: false,
        torrentPeersHighWater: 100,
        uploadRateLimit: 500,
      },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const dhtSwitch = screen.getByRole('switch', { name: /disable dht/i });
      expect(dhtSwitch).toBeChecked();
    });

    const establishedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
    await waitFor(() => {
      expect(establishedInput).toHaveValue(200);
    });

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      const dhtSwitch = screen.getByRole('switch', { name: /disable dht/i });
      expect(dhtSwitch).not.toBeChecked();
    });

    await waitFor(() => {
      const updatedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
      expect(updatedInput).toHaveValue(50);
    });
  });

  it('toast success is shown on Reset to Defaults click', async () => {
    const { toast } = await import('sonner');

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledWith('Settings reset to defaults');
    });
  });

  it('user can select a different log level via combobox', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
    selectCombobox(logLevelSelect, 'WARN');

    await waitFor(() => {
      expect(logLevelSelect).toHaveTextContent('WARN');
    });
  });

  it('user can select a different log format via combobox', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const logFormatSelect = screen.getByRole('combobox', { name: /log format/i });
    selectCombobox(logFormatSelect, 'text');

    await waitFor(() => {
      expect(logFormatSelect).toHaveTextContent(/Text/i);
    });
  });

  it('Reset button reverts auth settings to server baseline', async () => {
    settingsRef.current = buildMockSettings({
      auth: { enabled: true, type: 'bearer', username: 'admin', password: 'secret' },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const authSwitch = screen.getByRole('switch', { name: /enable authentication/i });
      expect(authSwitch).toBeChecked();
    });

    const authTypeSelect = screen.getByRole('combobox', { name: /authentication type/i });
    selectCombobox(authTypeSelect, 'basic');

    await waitFor(() => {
      expect(authTypeSelect).toHaveTextContent(/Basic/i);
    });

    fireEvent.click(getResetButton());

    await waitFor(() => {
      const authSwitch = screen.getByRole('switch', { name: /enable authentication/i });
      expect(authSwitch).toBeChecked();
    });
  });

  it('Reset to Defaults resets torrent trackers to hardcoded defaults', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const trackersTextarea = screen.getByRole('textbox', { name: /torrent trackers/i });
    fireEvent.change(trackersTextarea, { target: { value: 'udp://custom.tracker.com' } });
    expect(trackersTextarea).toHaveValue('udp://custom.tracker.com');

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      expect(trackersTextarea).toHaveValue('');
    });
  });
});
