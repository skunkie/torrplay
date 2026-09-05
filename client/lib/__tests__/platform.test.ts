// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { getArchitecture, getOperatingSystem, isTauriApp, openExternalUrl } from '../platform';

describe('platform', () => {
  const originalUserAgent = window.navigator.userAgent;

  beforeEach(() => {
    vi.stubEnv('NEXT_PUBLIC_APP_ARCH', '');
  });

  afterEach(() => {
    Object.defineProperty(window.navigator, 'userAgent', {
      value: originalUserAgent,
      configurable: true,
    });
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
  });

  it('detects macos from user agent', () => {
    Object.defineProperty(window.navigator, 'userAgent', {
      value: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36',
      configurable: true,
    });
    expect(getOperatingSystem()).toBe('macos');
  });

  it('detects windows from user agent', () => {
    Object.defineProperty(window.navigator, 'userAgent', {
      value: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36',
      configurable: true,
    });
    expect(getOperatingSystem()).toBe('windows');
  });

  it('detects linux from user agent', () => {
    Object.defineProperty(window.navigator, 'userAgent', {
      value: 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36',
      configurable: true,
    });
    expect(getOperatingSystem()).toBe('linux');
  });

  it('detects android from user agent', () => {
    Object.defineProperty(window.navigator, 'userAgent', {
      value: 'Mozilla/5.0 (Linux; U; Android 14; en-us; Pixel 8) AppleWebKit/537.36',
      configurable: true,
    });
    expect(getOperatingSystem()).toBe('android');
  });

  it('detects architecture from user agent', () => {
    Object.defineProperty(window.navigator, 'userAgent', {
      value: 'Mozilla/5.0 (Windows NT 10.0; ARM64) AppleWebKit/537.36',
      configurable: true,
    });
    expect(getArchitecture()).toBe('arm64');

    Object.defineProperty(window.navigator, 'userAgent', {
      value: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36',
      configurable: true,
    });
    expect(getArchitecture()).toBe('x64');
  });

  it('uses the build architecture when configured', () => {
    vi.stubEnv('NEXT_PUBLIC_APP_ARCH', 'ARM64');
    expect(getArchitecture()).toBe('arm64');
  });

  it('detects ios from user agent', () => {
    Object.defineProperty(window.navigator, 'userAgent', {
      value: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15',
      configurable: true,
    });
    expect(getOperatingSystem()).toBe('ios');
  });

  it('returns false for isTauriApp in standard mock environment', () => {
    expect(isTauriApp()).toBe(false);
  });

  it('opens external URL via window.open in browser environment', async () => {
    const mockOpen = vi.spyOn(window, 'open').mockImplementation(() => null);
    await openExternalUrl('https://example.com');
    expect(mockOpen).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer');
  });

  it('rejects non-HTTP external URLs', async () => {
    const mockOpen = vi.spyOn(window, 'open').mockImplementation(() => null);
    await openExternalUrl('javascript:alert(1)');
    await openExternalUrl('file:///tmp/update');
    await openExternalUrl('not a URL');
    expect(mockOpen).not.toHaveBeenCalled();
  });
});
