// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  classifyReleaseAssets,
  fetchLatestRelease,
  getReleasesApiUrl,
  GitHubReleaseAsset,
} from '../api/releases';

describe('releases', () => {
  const mockAssets: GitHubReleaseAsset[] = [
    {
      name: 'TorrPlay-1.1.0-x64.msi',
      browser_download_url: 'https://github.com/torrplay/torrplay/releases/download/v1.1.0/TorrPlay-1.1.0-x64.msi',
      size: 15000000,
    },
    {
      name: 'torrplay-client_1.1.0_x64_en-US.msi',
      browser_download_url: 'https://github.com/torrplay/torrplay/releases/download/v1.1.0/torrplay-client_1.1.0_x64_en-US.msi',
      size: 24000000,
    },
    {
      name: 'torrplay-windows-amd64.exe',
      browser_download_url: 'https://github.com/torrplay/torrplay/releases/download/v1.1.0/torrplay-windows-amd64.exe',
      size: 20000000,
    },
    {
      name: 'torrplay-client_1.1.0_x64-setup.exe',
      browser_download_url: 'https://github.com/torrplay/torrplay/releases/download/v1.1.0/torrplay-client_1.1.0_x64-setup.exe',
      size: 25000000,
    },
    {
      name: 'TorrPlay_1.1.0_aarch64.dmg',
      browser_download_url: 'https://github.com/torrplay/torrplay/releases/download/v1.1.0/TorrPlay_1.1.0_aarch64.dmg',
      size: 30000000,
    },
    {
      name: 'torrplay-client_1.1.0_amd64.AppImage',
      browser_download_url: 'https://github.com/torrplay/torrplay/releases/download/v1.1.0/torrplay-client_1.1.0_amd64.AppImage',
      size: 40000000,
    },
    {
      name: 'torrplay-android-arm64-v8a.apk',
      browser_download_url: 'https://github.com/torrplay/torrplay/releases/download/v1.1.0/torrplay-android-arm64-v8a.apk',
      size: 18000000,
    },
    {
      name: 'checksums.txt',
      browser_download_url: 'https://github.com/torrplay/torrplay/releases/download/v1.1.0/checksums.txt',
      size: 1024,
    },
  ];

  describe('getReleasesApiUrl', () => {
    it('returns default API url when no configured url is passed', () => {
      expect(getReleasesApiUrl()).toBe('https://api.github.com/repos/torrplay/torrplay/releases/latest');
    });

    it('transforms standard GitHub release URL to API URL', () => {
      expect(getReleasesApiUrl('https://github.com/my-fork/custom-torrplay/releases/latest')).toBe(
        'https://api.github.com/repos/my-fork/custom-torrplay/releases/latest'
      );
    });

    it('transforms bare GitHub repository URL to latest releases API URL', () => {
      expect(getReleasesApiUrl('https://github.com/my-fork/custom-torrplay')).toBe(
        'https://api.github.com/repos/my-fork/custom-torrplay/releases/latest'
      );
      expect(getReleasesApiUrl('https://github.com/my-fork/custom-torrplay/')).toBe(
        'https://api.github.com/repos/my-fork/custom-torrplay/releases/latest'
      );
    });

    it('preserves direct GitHub API URLs', () => {
      expect(getReleasesApiUrl('https://api.github.com/repos/org/repo/releases/latest')).toBe(
        'https://api.github.com/repos/org/repo/releases/latest'
      );
    });

    it('normalizes GitHub API release collection URLs', () => {
      expect(getReleasesApiUrl('https://api.github.com/repos/org/repo/releases')).toBe(
        'https://api.github.com/repos/org/repo/releases/latest'
      );
    });
  });

  describe('classifyReleaseAssets', () => {
    it('selects macOS DMG as primary asset on macOS', () => {
      const result = classifyReleaseAssets(mockAssets, 'macos', true, 'arm64');
      expect(result.primaryAsset).not.toBeNull();
      expect(result.primaryAsset?.type).toBe('macos-dmg');
      expect(result.primaryAsset?.name).toBe('TorrPlay_1.1.0_aarch64.dmg');
    });

    it('selects Windows Desktop Setup .exe as primary asset on Windows desktop', () => {
      const result = classifyReleaseAssets(mockAssets, 'windows', true, 'x64');
      expect(result.primaryAsset).not.toBeNull();
      expect(result.primaryAsset?.type).toBe('desktop-setup');
      expect(result.primaryAsset?.name).toBe('torrplay-client_1.1.0_x64-setup.exe');

      // Windows Service MSI should be in secondary assets
      const msiSecondary = result.secondaryAssets.find(a => a.type === 'windows-service');
      expect(msiSecondary).toBeDefined();
      expect(msiSecondary?.name).toBe('TorrPlay-1.1.0-x64.msi');
    });

    it('selects Windows Desktop Setup .exe on Windows web, with MSI service as secondary', () => {
      const result = classifyReleaseAssets(mockAssets, 'windows', false, 'x64');
      expect(result.primaryAsset).not.toBeNull();
      expect(result.primaryAsset?.type).toBe('desktop-setup');
      expect(result.secondaryAssets.some(a => a.type === 'windows-service')).toBe(true);
    });

    it('falls back to the service MSI on Windows web if a desktop installer is absent', () => {
      const serviceAssets = mockAssets.filter(a => a.name === 'TorrPlay-1.1.0-x64.msi');
      const result = classifyReleaseAssets(serviceAssets, 'windows', false, 'x64');
      expect(result.primaryAsset?.type).toBe('windows-service');
    });

    it('uses a desktop MSI when the Tauri setup executable is absent', () => {
      const withoutSetup = mockAssets.filter(a => !a.name.includes('setup.exe'));
      const result = classifyReleaseAssets(withoutSetup, 'windows', true, 'x64');
      expect(result.primaryAsset?.type).toBe('desktop-msi');
      expect(result.primaryAsset?.name).toContain('torrplay-client');
    });

    it('ignores raw executables and incompatible architectures', () => {
      const armResult = classifyReleaseAssets(mockAssets, 'windows', true, 'arm64');
      expect(armResult.primaryAsset).toBeNull();
      expect(armResult.secondaryAssets).toEqual([]);

      const x64Result = classifyReleaseAssets(mockAssets, 'windows', true, 'x64');
      expect(x64Result.primaryAsset?.name).toBe('torrplay-client_1.1.0_x64-setup.exe');
      expect(x64Result.allAssets.find(asset => asset.name === 'torrplay-windows-amd64.exe')?.type)
        .toBe('generic');
    });

    it('does not guess when the client architecture is unknown', () => {
      const result = classifyReleaseAssets(mockAssets, 'macos', false, 'unknown');
      expect(result.primaryAsset).toBeNull();
      expect(result.secondaryAssets).toEqual([]);
    });

    it('selects AppImage on Linux', () => {
      const result = classifyReleaseAssets(mockAssets, 'linux', false, 'x64');
      expect(result.primaryAsset).not.toBeNull();
      expect(result.primaryAsset?.type).toBe('linux-appimage');
    });

    it('does not offer release assets on Android', () => {
      const result = classifyReleaseAssets(mockAssets, 'android', false);
      expect(result.primaryAsset).toBeNull();
      expect(result.secondaryAssets.some(asset => asset.name.endsWith('.apk'))).toBe(false);
    });

    it('returns null primaryAsset for unknown OS', () => {
      const result = classifyReleaseAssets(mockAssets, 'unknown', false);
      expect(result.primaryAsset).toBeNull();
      expect(result.allAssets.length).toBe(mockAssets.length);
    });
  });

  describe('fetchLatestRelease', () => {
    const originalFetch = global.fetch;
    const mockResponse = {
      tag_name: 'v1.1.0',
      name: 'TorrPlay 1.1.0',
      body: 'Release notes',
      html_url: 'https://github.com/torrplay/torrplay/releases/tag/v1.1.0',
      published_at: '2026-09-01T00:00:00Z',
      assets: mockAssets,
    };

    beforeEach(() => {
      localStorage.clear();
      global.fetch = vi.fn();
    });

    afterEach(() => {
      global.fetch = originalFetch;
    });

    it('fetches release info successfully', async () => {
      (global.fetch as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      });

      const release = await fetchLatestRelease();
      expect(release.tag_name).toBe('v1.1.0');
      expect(global.fetch).toHaveBeenCalledWith(
        'https://api.github.com/repos/torrplay/torrplay/releases/latest',
        expect.objectContaining({
          headers: { Accept: 'application/vnd.github.v3+json' },
        })
      );
    });

    it('reuses a recent cached release', async () => {
      (global.fetch as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      });

      await fetchLatestRelease('https://example.com/releases/latest');
      await fetchLatestRelease('https://example.com/releases/latest');

      expect(global.fetch).toHaveBeenCalledTimes(1);
    });

    it('bypasses the cache for a forced refresh', async () => {
      (global.fetch as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      });

      await fetchLatestRelease('https://example.com/releases/latest');
      await fetchLatestRelease('https://example.com/releases/latest', { forceRefresh: true });

      expect(global.fetch).toHaveBeenCalledTimes(2);
    });

    it('rejects malformed release responses', async () => {
      (global.fetch as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ tag_name: 'v1.1.0' }),
      });

      await expect(fetchLatestRelease()).rejects.toThrow('invalid response');
    });

    it('throws error when response is not ok', async () => {
      (global.fetch as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 404,
      });

      await expect(fetchLatestRelease()).rejects.toThrow('HTTP 404');
    });
  });
});
