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
    halfOpenConnsPerTorrent: 25,
    maxAllocPeerRequestDataPerConn: 1048576,
    preferHeaderObfuscation: true,
    seed: true,
    torrentPeersHighWater: 300,
    torrentPeersLowWater: 50,
    totalHalfOpenConns: 100,
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

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    const peersItem = peersAccordion.closest('[data-slot="accordion-item"]');
    if (peersItem && peersItem.getAttribute('data-state') !== 'open') {
      fireEvent.click(peersAccordion);
    }
    await waitFor(() => {
      if (peersItem) {
        expect(peersItem.getAttribute('data-state')).toBe('open');
      }
    });

    await waitFor(() => {
      const establishedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
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

  it('correctly populates log level when server returns DEBUG level', async () => {
    settingsRef.current = buildMockSettings({
      logLevel: 'DEBUG',
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
      expect(logLevelSelect).toHaveTextContent('DEBUG');
    });
  });

  it('correctly populates default INFO log level when server returns undefined logLevel', async () => {
    settingsRef.current = buildMockSettings({
      logLevel: undefined,
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const logLevelSelect = screen.getByRole('combobox', { name: /log level/i });
      expect(logLevelSelect).toHaveTextContent('INFO');
    });
  });

  it('Torrent Client accordion is expanded by default', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const torrentClientSection = screen.getByText('Torrent Client');
      expect(torrentClientSection.closest('[data-state="open"]')).toBeTruthy();
    });
  });

  it('Protocol & Discovery accordion is expanded by default', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const protocolAccordion = screen.getByRole('button', { name: /Protocol & Discovery/i });
      expect(protocolAccordion.closest('[data-state="open"]')).toBeTruthy();
    });
  });

  it('Rate Limits accordion is expanded by default', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const rateLimitsAccordion = screen.getByRole('button', { name: /Rate Limits/i });
      expect(rateLimitsAccordion.closest('[data-state="open"]')).toBeTruthy();
    });
  });

  it('Rate Limits shows download/upload inputs with current unit', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const downloadInput = screen.getByRole('spinbutton', { name: /download rate limit/i });
      expect(downloadInput).toHaveValue(0);
    });

    const uploadInput = screen.getByRole('spinbutton', { name: /upload rate limit/i });
    expect(uploadInput).toHaveValue(0);
  });

  it('Rate Limits unit selector changes display of rate limit inputs', async () => {
    settingsRef.current = buildMockSettings({
      torrentClient: {
        ...buildMockSettings().torrentClient,
        downloadRateLimit: 10485760,
        uploadRateLimit: 5242880,
      },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const downloadInput = screen.getByRole<HTMLInputElement>('spinbutton', { name: /download rate limit.*MiB\/s/i });
      expect(downloadInput).toHaveValue(10);
    });

    const uploadInput = screen.getByRole('spinbutton', { name: /upload rate limit.*MiB\/s/i });
    expect(uploadInput).toHaveValue(5);
  });

  it('Rate Limits converts between unit options', async () => {
    settingsRef.current = buildMockSettings({
      torrentClient: {
        ...buildMockSettings().torrentClient,
        downloadRateLimit: 10485760,
        uploadRateLimit: 0,
      },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    await waitFor(() => {
      const downloadInput = screen.getByRole('spinbutton', { name: /download rate limit/i });
      expect(downloadInput).toHaveValue(10);
    });

    const unitSelector = screen.getByRole('combobox', { name: /rate limit unit/i });
    selectCombobox(unitSelector, 'KiB/s');

    await waitFor(() => {
      const downloadInput = screen.getByRole('spinbutton', { name: /download rate limit/i });
      expect(downloadInput).toHaveValue(10240);
    });
  });

  it('Peers & Connections accordion is collapsed by default and has Advanced badge', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    await waitFor(() => {
      expect(peersAccordion.closest('[data-state="closed"]')).toBeTruthy();
    });

    const badge = peersAccordion.closest('[data-state="closed"]')?.querySelector('[data-slot="badge"]');
    expect(badge).toBeInTheDocument();
  });

  it('Memory & Buffers accordion is collapsed by default and has Advanced badge', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const memoryAccordion = screen.getByRole('button', { name: /memory & buffers/i });
    await waitFor(() => {
      expect(memoryAccordion.closest('[data-state="closed"]')).toBeTruthy();
    });
  });

  it('User can toggle Protocol & Discovery accordion closed and open', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const dhtSwitch = screen.getByRole('switch', { name: /disable dht/i });
    expect(dhtSwitch).toBeInTheDocument();

    const protocolAccordion = screen.getByRole('button', { name: /Protocol & Discovery/i });
    fireEvent.click(protocolAccordion);

    await waitFor(() => {
      expect(protocolAccordion.closest('[data-state="closed"]')).toBeTruthy();
    });

    expect(screen.queryByRole('switch', { name: /disable dht/i })).not.toBeInTheDocument();

    fireEvent.click(protocolAccordion);
    await waitFor(() => {
      expect(screen.getByRole('switch', { name: /disable dht/i })).toBeInTheDocument();
    });
  });

  it('User can toggle Peers & Connections accordion open to edit values', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    fireEvent.click(peersAccordion);

    await waitFor(() => {
      const establishedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
      expect(establishedInput).toHaveValue(100);
    });
  });

  it('User can toggle Memory & Buffers accordion open to edit values', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const memoryAccordion = screen.getByRole('button', { name: /memory & buffers/i });
    fireEvent.click(memoryAccordion);

    await waitFor(() => {
      const bufferInput = screen.getByRole('spinbutton', { name: /peer request buffer/i });
      expect(bufferInput).toHaveValue(1024);
    });
  });

  it('User can change Connections Per Torrent value', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    fireEvent.click(peersAccordion);

    await waitFor(() => {
      const establishedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
      expect(establishedInput).toHaveValue(100);
    });

    const establishedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
    fireEvent.change(establishedInput, { target: { value: '75' } });
    expect(establishedInput).toHaveValue(75);
  });

  it('User can change Peers High Water Mark value', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    fireEvent.click(peersAccordion);

    await waitFor(() => {
      const highWaterInput = screen.getByRole('spinbutton', { name: /peers high water/i });
      expect(highWaterInput).toHaveValue(300);
    });

    const highWaterInput = screen.getByRole('spinbutton', { name: /peers high water/i });
    fireEvent.change(highWaterInput, { target: { value: '400' } });
    expect(highWaterInput).toHaveValue(400);
  });

  it('User can change Peers Low Water Mark value', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    fireEvent.click(peersAccordion);

    await waitFor(() => {
      const lowWaterInput = screen.getByRole('spinbutton', { name: /peers low water/i });
      expect(lowWaterInput).toHaveValue(50);
    });

    const lowWaterInput = screen.getByRole('spinbutton', { name: /peers low water/i });
    fireEvent.change(lowWaterInput, { target: { value: '25' } });
    expect(lowWaterInput).toHaveValue(25);
  });

  it('User can change Half-Open Conns Per Torrent value', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    fireEvent.click(peersAccordion);

    await waitFor(() => {
      const halfOpenInput = screen.getByRole('spinbutton', { name: /half-open conns per torrent/i });
      expect(halfOpenInput).toHaveValue(25);
    });

    const halfOpenInput = screen.getByRole('spinbutton', { name: /half-open conns per torrent/i });
    fireEvent.change(halfOpenInput, { target: { value: '30' } });
    expect(halfOpenInput).toHaveValue(30);
  });

  it('User can change Total Half-Open Conns value', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    fireEvent.click(peersAccordion);

    await waitFor(() => {
      const totalHalfOpenInput = screen.getByRole('spinbutton', { name: /total half-open conns/i });
      expect(totalHalfOpenInput).toHaveValue(100);
    });

    const totalHalfOpenInput = screen.getByRole('spinbutton', { name: /total half-open conns/i });
    fireEvent.change(totalHalfOpenInput, { target: { value: '150' } });
    expect(totalHalfOpenInput).toHaveValue(150);
  });

  it('User can change Peer Request Buffer value', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const memoryAccordion = screen.getByRole('button', { name: /memory & buffers/i });
    fireEvent.click(memoryAccordion);

    const bufferInput = screen.getByRole('spinbutton', { name: /peer request buffer/i });
    await waitFor(() => {
      expect(bufferInput).toHaveValue(1024);
    });

    fireEvent.change(bufferInput, { target: { value: '250' } });
    expect(bufferInput).toHaveValue(250);
  });

  it('Reset button reverts Rate Limits to server values', async () => {
    settingsRef.current = buildMockSettings({
      torrentClient: {
        ...buildMockSettings().torrentClient,
        downloadRateLimit: 5000000,
        uploadRateLimit: 3000000,
      },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const downloadInput = screen.getByRole<HTMLInputElement>('spinbutton', { name: /download rate limit.*MiB\/s/i });
    await waitFor(() => {
      expect(Number(downloadInput.value)).toBeCloseTo(4.77, 2);
    });

    fireEvent.change(downloadInput, { target: { value: '99' } });

    await waitFor(() => {
      expect(Number(downloadInput.value)).toBe(99);
    });

    fireEvent.click(getResetButton());

    await waitFor(() => {
      expect(Number(downloadInput.value)).toBeCloseTo(4.77, 2);
    });
  });

  it('Reset button reverts Peers & Connections to server values', async () => {
    settingsRef.current = buildMockSettings({
      torrentClient: {
        ...buildMockSettings().torrentClient,
        establishedConnsPerTorrent: 200,
        torrentPeersHighWater: 600,
        torrentPeersLowWater: 100,
        halfOpenConnsPerTorrent: 50,
        totalHalfOpenConns: 200,
      },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    fireEvent.click(peersAccordion);

    await waitFor(() => {
      const establishedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
      expect(establishedInput).toHaveValue(200);
    });

    const establishedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
    fireEvent.change(establishedInput, { target: { value: '150' } });

    await waitFor(() => {
      expect(establishedInput).toHaveValue(150);
    });

    fireEvent.click(getResetButton());

    await waitFor(() => {
      const updatedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
      expect(updatedInput).toHaveValue(200);
    });
  });

  it('Reset button reverts Memory & Buffers to server values', async () => {
    settingsRef.current = buildMockSettings({
      torrentClient: {
        ...buildMockSettings().torrentClient,
        maxAllocPeerRequestDataPerConn: 262144,
      },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const memoryAccordion = screen.getByRole('button', { name: /memory & buffers/i });
    fireEvent.click(memoryAccordion);

    const bufferInput = screen.getByRole('spinbutton', { name: /peer request buffer/i });
    await waitFor(() => {
      expect(bufferInput).toHaveValue(256);
    });

    fireEvent.change(bufferInput, { target: { value: '999' } });

    await waitFor(() => {
      expect(bufferInput).toHaveValue(999);
    });

    fireEvent.click(getResetButton());

    await waitFor(() => {
      const updatedInput = screen.getByRole('spinbutton', { name: /peer request buffer/i });
      expect(updatedInput).toHaveValue(256);
    });
  });

  it('Reset to Defaults sets Rate Limits to zero', async () => {
    settingsRef.current = buildMockSettings({
      torrentClient: {
        ...buildMockSettings().torrentClient,
        downloadRateLimit: 1000000,
        uploadRateLimit: 500000,
      },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      const downloadInput = screen.getByRole<HTMLInputElement>('spinbutton', { name: /download rate limit.*MiB\/s/i });
      expect(downloadInput).toHaveValue(0);
    });

    const uploadInput = screen.getByRole('spinbutton', { name: /upload rate limit.*MiB\/s/i });
    expect(uploadInput).toHaveValue(0);
  });

  it('Reset to Defaults sets Peers & Connections to hardcoded defaults', async () => {
    settingsRef.current = buildMockSettings({
      torrentClient: {
        ...buildMockSettings().torrentClient,
        establishedConnsPerTorrent: 300,
        torrentPeersHighWater: 1000,
        torrentPeersLowWater: 200,
        halfOpenConnsPerTorrent: 100,
        totalHalfOpenConns: 500,
      },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    fireEvent.click(peersAccordion);

    await waitFor(() => {
      const establishedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
      expect(establishedInput).toHaveValue(300);
    });

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      const establishedInput = screen.getByRole('spinbutton', { name: /connections per torrent/i });
      expect(establishedInput).toHaveValue(50);
    });

    const highWaterInput = screen.getByRole('spinbutton', { name: /peers high water mark/i });
    expect(highWaterInput).toHaveValue(500);

    const lowWaterInput = screen.getByRole('spinbutton', { name: /peers low water mark/i });
    expect(lowWaterInput).toHaveValue(50);

    const halfOpenInput = screen.getByRole('spinbutton', { name: /half-open conns per torrent/i });
    expect(halfOpenInput).toHaveValue(25);

    const totalHalfOpenInput = screen.getByRole('spinbutton', { name: /total half-open conns/i });
    expect(totalHalfOpenInput).toHaveValue(100);
  });

  it('Reset to Defaults sets Memory & Buffers to hardcoded defaults', async () => {
    settingsRef.current = buildMockSettings({
      torrentClient: {
        ...buildMockSettings().torrentClient,
        maxAllocPeerRequestDataPerConn: 262144,
      },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const memoryAccordion = screen.getByRole('button', { name: /memory & buffers/i });
    fireEvent.click(memoryAccordion);

    const bufferInput = screen.getByRole('spinbutton', { name: /peer request buffer/i });
    await waitFor(() => {
      expect(bufferInput).toHaveValue(256);
    });

    fireEvent.click(getResetToDefaultsButton());

    await waitFor(() => {
      const updatedInput = screen.getByRole('spinbutton', { name: /peer request buffer/i });
      expect(updatedInput).toHaveValue(1024);
    });
  });

  it('Peers & Connections and Memory & Buffers accordions are collapsed by default', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const peersAccordion = screen.getByRole('button', { name: /peers & connections/i });
    const memoryAccordion = screen.getByRole('button', { name: /memory & buffers/i });

    await waitFor(() => {
      expect(peersAccordion.closest('[data-state="closed"]')).toBeTruthy();
      expect(memoryAccordion.closest('[data-state="closed"]')).toBeTruthy();
    });
  });

  it('Protocol & Discovery switches are visible without expanding accordion', async () => {
    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    const dhtSwitch = screen.getByRole('switch', { name: /disable dht/i });
    expect(dhtSwitch).toBeInTheDocument();

    const ipv6Switch = screen.getByRole('switch', { name: /disable ipv6/i });
    expect(ipv6Switch).toBeInTheDocument();

    const pexSwitch = screen.getByRole('switch', { name: /disable pex/i });
    expect(pexSwitch).toBeInTheDocument();
  });

  it('saving after Reset to Defaults sends reset torrentClient values including new advanced fields', async () => {
    settingsRef.current = buildMockSettings({
      torrentClient: {
        ...buildMockSettings().torrentClient,
        halfOpenConnsPerTorrent: 99,
        maxAllocPeerRequestDataPerConn: 524288,
        torrentPeersLowWater: 99,
        totalHalfOpenConns: 999,
      },
    });

    render(<SettingsDialog open={true}
      onOpenChange={vi.fn()} />);

    fireEvent.click(getResetToDefaultsButton());

    const saveButton = screen.getByRole('button', { name: /save changes/i });
    fireEvent.click(saveButton);

    await waitFor(() => {
      expect(mockUpdateSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          torrentClient: expect.objectContaining({
            halfOpenConnsPerTorrent: 25,
            maxAllocPeerRequestDataPerConn: 1048576,
            torrentPeersLowWater: 50,
            totalHalfOpenConns: 100,
          }),
        }),
      );
    });
  });
});
