// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import {
  Architecture,
  getArchitecture,
  getOperatingSystem,
  isTauriApp,
  OperatingSystem,
} from '@/lib/platform';

export interface GitHubReleaseAsset {
  name: string,
  browser_download_url: string,
  size?: number,
  content_type?: string
}

export interface GitHubRelease {
  tag_name: string,
  name: string | null,
  body: string | null,
  html_url: string,
  published_at: string,
  assets: GitHubReleaseAsset[]
}

export type AssetType =
  | 'desktop-setup'
  | 'desktop-msi'
  | 'windows-service'
  | 'macos-dmg'
  | 'linux-appimage'
  | 'linux-desktop-deb'
  | 'linux-desktop-rpm'
  | 'linux-server-deb'
  | 'linux-server-rpm'
  | 'generic';

export interface ReleaseAssetInfo {
  name: string,
  label: string,
  url: string,
  size?: number,
  type: AssetType,
  architecture?: Architecture
}

export interface ClassifiedAssets {
  primaryAsset: ReleaseAssetInfo | null,
  secondaryAssets: ReleaseAssetInfo[],
  allAssets: ReleaseAssetInfo[]
}

export const DEFAULT_RELEASES_URL = 'https://github.com/torrplay/torrplay/releases/latest';
export const RELEASE_CACHE_TTL_MS = 6 * 60 * 60 * 1000;

const RELEASE_CACHE_KEY = 'torrplay_latest_release_cache';

interface CachedRelease {
  endpoint: string,
  fetchedAt: number,
  release: GitHubRelease
}

function isGitHubRelease(value: unknown): value is GitHubRelease {
  if (!value || typeof value !== 'object') return false;
  const release = value as Partial<GitHubRelease>;
  return typeof release.tag_name === 'string' &&
    (typeof release.name === 'string' || release.name === null) &&
    (typeof release.body === 'string' || release.body === null) &&
    typeof release.html_url === 'string' &&
    typeof release.published_at === 'string' &&
    Array.isArray(release.assets) &&
    release.assets.every(asset =>
      typeof asset?.name === 'string' && typeof asset.browser_download_url === 'string'
    );
}

function readCachedRelease(endpoint: string): GitHubRelease | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = localStorage.getItem(RELEASE_CACHE_KEY);
    if (!raw) return null;
    const cached = JSON.parse(raw) as Partial<CachedRelease>;
    if (cached.endpoint !== endpoint ||
        typeof cached.fetchedAt !== 'number' ||
        Date.now() - cached.fetchedAt >= RELEASE_CACHE_TTL_MS ||
        !isGitHubRelease(cached.release)) {
      return null;
    }
    return cached.release;
  } catch {
    return null;
  }
}

function writeCachedRelease(endpoint: string, release: GitHubRelease): void {
  if (typeof window === 'undefined') return;
  try {
    const cached: CachedRelease = { endpoint, fetchedAt: Date.now(), release };
    localStorage.setItem(RELEASE_CACHE_KEY, JSON.stringify(cached));
  } catch {
    // Caching is best effort.
  }
}

export function getReleasesApiUrl(configuredUrl?: string): string {
  const raw = (configuredUrl || process.env.NEXT_PUBLIC_RELEASES_URL || DEFAULT_RELEASES_URL).trim();
  if (!raw) {
    return 'https://api.github.com/repos/torrplay/torrplay/releases/latest';
  }

  try {
    const parsed = new URL(raw);
    if (parsed.hostname === 'api.github.com' && parsed.pathname.startsWith('/repos/')) {
      const repositoryPath = parsed.pathname.replace(/\/releases(?:\/.*)?$/, '').replace(/\/+$/, '');
      return `https://api.github.com${repositoryPath}/releases/latest`;
    }
    if (parsed.hostname === 'github.com') {
      const repositoryPath = parsed.pathname.replace(/\/releases(?:\/.*)?$/, '').replace(/\/+$/, '');
      return `https://api.github.com/repos${repositoryPath}/releases/latest`;
    }
  } catch {
    // Preserve custom non-URL endpoints for the caller to handle.
  }

  return raw;
}

export async function fetchLatestRelease(
  url?: string,
  { forceRefresh = false }: { forceRefresh?: boolean } = {}
): Promise<GitHubRelease> {
  const endpoint = url || getReleasesApiUrl();
  if (!forceRefresh) {
    const cached = readCachedRelease(endpoint);
    if (cached) return cached;
  }

  const response = await fetch(endpoint, {
    headers: {
      Accept: 'application/vnd.github.v3+json',
    },
  });

  if (!response.ok) {
    throw new Error(`Failed to fetch latest release: HTTP ${response.status}`);
  }

  const release: unknown = await response.json();
  if (!isGitHubRelease(release)) {
    throw new Error('Failed to fetch latest release: invalid response');
  }
  writeCachedRelease(endpoint, release);
  return release;
}

export function classifyAsset(asset: GitHubReleaseAsset): ReleaseAssetInfo {
  const name = asset.name.toLowerCase();

  let label = asset.name;
  let type: AssetType = 'generic';

  if (name.endsWith('.dmg')) {
    label = 'macOS Installer (.dmg)';
    type = 'macos-dmg';
  } else if (name.endsWith('.msi')) {
    if (name.includes('torrplay-client')) {
      label = 'Windows Desktop Installer (.msi)';
      type = 'desktop-msi';
    } else if (/^torrplay-\d/.test(name)) {
      label = 'Windows Service Installer (.msi)';
      type = 'windows-service';
    }
  } else if (name.endsWith('.exe')) {
    if (name.includes('torrplay-client') && /setup|installer/.test(name)) {
      label = 'Windows Desktop Installer (.exe)';
      type = 'desktop-setup';
    }
  } else if (name.endsWith('.appimage')) {
    if (name.includes('torrplay-client')) {
      label = 'Linux AppImage (.AppImage)';
      type = 'linux-appimage';
    }
  } else if (name.endsWith('.deb')) {
    if (name.includes('torrplay-client')) {
      label = 'Debian / Ubuntu Desktop Package (.deb)';
      type = 'linux-desktop-deb';
    } else {
      label = 'Debian / Ubuntu Server Package (.deb)';
      type = 'linux-server-deb';
    }
  } else if (name.endsWith('.rpm')) {
    if (name.includes('torrplay-client')) {
      label = 'Fedora / RHEL Desktop Package (.rpm)';
      type = 'linux-desktop-rpm';
    } else {
      label = 'Fedora / RHEL Server Package (.rpm)';
      type = 'linux-server-rpm';
    }
  }

  let architecture: Architecture = 'unknown';
  if (/arm64|aarch64/.test(name)) {
    architecture = 'arm64';
  } else if (/x86_64|x64|amd64/.test(name)) {
    architecture = 'x64';
  }

  return {
    name: asset.name,
    label,
    url: asset.browser_download_url,
    size: asset.size,
    type,
    architecture,
  };
}

function isCompatibleArchitecture(asset: ReleaseAssetInfo, architecture: Architecture): boolean {
  return asset.architecture === 'unknown' ||
    (architecture !== 'unknown' && asset.architecture === architecture);
}

function isRelevantToOperatingSystem(asset: ReleaseAssetInfo, os: OperatingSystem): boolean {
  if (os === 'macos') return asset.type === 'macos-dmg';
  if (os === 'windows') {
    return ['desktop-setup', 'desktop-msi', 'windows-service'].includes(asset.type);
  }
  if (os === 'linux') {
    return [
      'linux-appimage',
      'linux-desktop-deb',
      'linux-desktop-rpm',
      'linux-server-deb',
      'linux-server-rpm',
    ].includes(asset.type);
  }
  return false;
}

export function classifyReleaseAssets(
  assets: GitHubReleaseAsset[] = [],
  os: OperatingSystem = getOperatingSystem(),
  isDesktop: boolean = isTauriApp(),
  architecture: Architecture = getArchitecture()
): ClassifiedAssets {
  const classified = assets.map(classifyAsset);
  const compatible = classified.filter(asset =>
    isRelevantToOperatingSystem(asset, os) && isCompatibleArchitecture(asset, architecture)
  );

  let primaryAsset: ReleaseAssetInfo | null = null;
  const secondaryAssets: ReleaseAssetInfo[] = [];

  switch (os) {
    case 'macos': {
      // Primary is macOS DMG
      primaryAsset = compatible.find(a => a.type === 'macos-dmg') || null;
      break;
    }
    case 'windows': {
      // For Windows: if running inside desktop app, primary is .exe setup installer
      // If .exe is not available or user is on web, still prefer .exe setup with .msi as secondary
      const exeAsset = compatible.find(a => a.type === 'desktop-setup');
      const desktopMsiAsset = compatible.find(a => a.type === 'desktop-msi');
      const serviceMsiAsset = compatible.find(a => a.type === 'windows-service');

      if (isDesktop) {
        primaryAsset = exeAsset || desktopMsiAsset || null;
      } else {
        primaryAsset = exeAsset || desktopMsiAsset || serviceMsiAsset || null;
      }
      break;
    }
    case 'linux': {
      const appImage = compatible.find(a => a.type === 'linux-appimage');
      const desktopDeb = compatible.find(a => a.type === 'linux-desktop-deb');
      const desktopRpm = compatible.find(a => a.type === 'linux-desktop-rpm');
      const serverDeb = compatible.find(a => a.type === 'linux-server-deb');
      const serverRpm = compatible.find(a => a.type === 'linux-server-rpm');
      primaryAsset = isDesktop
        ? appImage || desktopDeb || desktopRpm || null
        : appImage || desktopDeb || desktopRpm || serverDeb || serverRpm || null;
      break;
    }
    default: {
      primaryAsset = null;
      break;
    }
  }

  // Populate other relevant secondary assets (excluding primary)
  for (const asset of compatible) {
    if (asset !== primaryAsset && !secondaryAssets.includes(asset)) {
      if (asset.type !== 'generic') {
        secondaryAssets.push(asset);
      }
    }
  }

  return {
    primaryAsset,
    secondaryAssets,
    allAssets: classified,
  };
}
