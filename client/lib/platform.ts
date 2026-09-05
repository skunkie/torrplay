// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { getVersion } from '@tauri-apps/api/app';
import { isTauri } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';

export type OperatingSystem = 'macos' | 'windows' | 'linux' | 'android' | 'ios' | 'unknown';
export type Architecture = 'x64' | 'arm64' | 'unknown';

export function getOperatingSystem(): OperatingSystem {
  if (typeof window === 'undefined' || !window.navigator) {
    return 'unknown';
  }

  const userAgent = (window.navigator.userAgent || '').toLowerCase();
  const platform = (
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (window.navigator as any).userAgentData?.platform ||
    window.navigator.platform ||
    ''
  ).toLowerCase();

  if (userAgent.includes('android')) {
    return 'android';
  }
  if (userAgent.includes('iphone') || userAgent.includes('ipad') || userAgent.includes('ipod')) {
    return 'ios';
  }
  if (userAgent.includes('macintosh') || userAgent.includes('mac os x') || platform.includes('mac')) {
    return 'macos';
  }
  if (userAgent.includes('windows') || userAgent.includes('win32') || platform.includes('win')) {
    return 'windows';
  }
  if (userAgent.includes('linux') || platform.includes('linux')) {
    return 'linux';
  }

  return 'unknown';
}

export function isTauriApp(): boolean {
  try {
    return isTauri();
  } catch {
    return false;
  }
}

export function getArchitecture(): Architecture {
  const configuredArchitecture = (process.env.NEXT_PUBLIC_APP_ARCH || '').toLowerCase();
  if (['arm64', 'aarch64'].includes(configuredArchitecture)) return 'arm64';
  if (['x64', 'x86_64', 'amd64'].includes(configuredArchitecture)) return 'x64';

  if (typeof window === 'undefined' || !window.navigator) return 'unknown';

  const userAgent = (window.navigator.userAgent || '').toLowerCase();
  if (/arm64|aarch64/.test(userAgent)) return 'arm64';
  if (/x86_64|x64|amd64|win64/.test(userAgent)) return 'x64';
  return 'unknown';
}

export async function getApplicationVersion(): Promise<string | null> {
  if (!isTauriApp()) return null;
  try {
    return await getVersion();
  } catch {
    return null;
  }
}

export async function openExternalUrl(url: string): Promise<void> {
  if (!url) return;

  try {
    const parsed = new URL(url);
    if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') return;
  } catch {
    return;
  }

  if (isTauriApp()) {
    try {
      await openUrl(url);
      return;
    } catch {
      // Fall back to window.open if tauri opener fails
    }
  }

  if (typeof window !== 'undefined') {
    window.open(url, '_blank', 'noopener,noreferrer');
  }
}
