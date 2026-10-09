// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import { isNewerVersion, normalizeVersion, parseSemver } from '../semver';

describe('semver', () => {
  describe('normalizeVersion', () => {
    it('strips leading v or V', () => {
      expect(normalizeVersion('v1.2.3')).toBe('1.2.3');
      expect(normalizeVersion('V2.0.0')).toBe('2.0.0');
      expect(normalizeVersion('1.0.0')).toBe('1.0.0');
    });

    it('handles empty or whitespace strings', () => {
      expect(normalizeVersion('')).toBe('');
      expect(normalizeVersion('   ')).toBe('');
    });
  });

  describe('parseSemver', () => {
    it('parses valid semantic version strings', () => {
      expect(parseSemver('1.2.3')).toEqual({ major: 1, minor: 2, patch: 3, prerelease: null });
      expect(parseSemver('v2.0.0')).toEqual({ major: 2, minor: 0, patch: 0, prerelease: null });
      expect(parseSemver('1.0.0-beta.1')).toEqual({ major: 1, minor: 0, patch: 0, prerelease: 'beta.1' });
      expect(parseSemver('1.0.0-beta.1+build.42')).toEqual({
        major: 1,
        minor: 0,
        patch: 0,
        prerelease: 'beta.1',
      });
    });

    it('returns null for invalid version strings', () => {
      expect(parseSemver('')).toBeNull();
      expect(parseSemver('demo')).toBeNull();
      expect(parseSemver('invalid.version')).toBeNull();
      expect(parseSemver('1.2')).toBeNull();
      expect(parseSemver('1.2.3.4')).toBeNull();
      expect(parseSemver('1foo.2.3')).toBeNull();
      expect(parseSemver('01.2.3')).toBeNull();
      expect(parseSemver('1.2.3-beta.01')).toBeNull();
      expect(parseSemver('1.2.3-')).toBeNull();
      expect(parseSemver('1.2.3+')).toBeNull();
    });
  });

  describe('isNewerVersion', () => {
    it('detects newer major versions', () => {
      expect(isNewerVersion('1.0.0', '2.0.0')).toBe(true);
      expect(isNewerVersion('2.0.0', '1.9.9')).toBe(false);
    });

    it('detects newer minor versions', () => {
      expect(isNewerVersion('1.1.0', '1.2.0')).toBe(true);
      expect(isNewerVersion('1.2.0', '1.1.5')).toBe(false);
    });

    it('detects newer patch versions', () => {
      expect(isNewerVersion('1.0.1', '1.0.2')).toBe(true);
      expect(isNewerVersion('1.0.2', '1.0.1')).toBe(false);
      expect(isNewerVersion('1.0.0', '1.0.0')).toBe(false);
    });

    it('handles leading v prefixes symmetrically', () => {
      expect(isNewerVersion('v1.0.0', '1.0.1')).toBe(true);
      expect(isNewerVersion('1.0.0', 'v1.0.1')).toBe(true);
      expect(isNewerVersion('v1.0.0', 'v1.0.0')).toBe(false);
    });

    it('handles prerelease versions properly', () => {
      // Full release is newer than prerelease of same version
      expect(isNewerVersion('1.0.0-beta', '1.0.0')).toBe(true);
      expect(isNewerVersion('1.0.0', '1.0.0-beta')).toBe(false);
      expect(isNewerVersion('1.0.0-alpha', '1.0.0-beta')).toBe(true);
      expect(isNewerVersion('1.0.0-beta.2', '1.0.0-beta.11')).toBe(true);
      expect(isNewerVersion('1.0.0-alpha', '1.0.0-alpha.1')).toBe(true);
      expect(isNewerVersion('1.0.0-1', '1.0.0-alpha')).toBe(true);
      expect(isNewerVersion('1.0.0-alpha', '1.0.0-1')).toBe(false);
    });

    it('ignores build metadata when comparing precedence', () => {
      expect(isNewerVersion('1.0.0+build.1', '1.0.0+build.2')).toBe(false);
      expect(isNewerVersion('1.0.0-beta+build.1', '1.0.0-beta+build.2')).toBe(false);
    });

    it('returns false for invalid versions or demo version', () => {
      expect(isNewerVersion('demo', '1.0.0')).toBe(false);
      expect(isNewerVersion('1.0.0', 'demo')).toBe(false);
      expect(isNewerVersion('', '1.0.0')).toBe(false);
    });
  });
});
