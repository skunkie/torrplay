// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

export interface ParsedSemver {
  major: number,
  minor: number,
  patch: number,
  prerelease: string | null
}

const SEMVER_PATTERN = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;

export function normalizeVersion(version: string): string {
  if (!version) return '';
  return version.trim().replace(/^v/i, '');
}

export function parseSemver(version: string): ParsedSemver | null {
  const normalized = normalizeVersion(version);
  if (!normalized) return null;

  const match = SEMVER_PATTERN.exec(normalized);
  if (!match) {
    return null;
  }

  const prerelease = match[4] || null;
  if (prerelease?.split('.').some(identifier => /^\d+$/.test(identifier) && identifier.length > 1 && identifier.startsWith('0'))) {
    return null;
  }

  const major = Number(match[1]);
  const minor = Number(match[2]);
  const patch = Number(match[3]);

  if (![major, minor, patch].every(Number.isSafeInteger)) {
    return null;
  }

  return { major, minor, patch, prerelease };
}

function compareNumericIdentifiers(latestIdentifier: string, currentIdentifier: string): number {
  if (latestIdentifier.length !== currentIdentifier.length) {
    return latestIdentifier.length > currentIdentifier.length ? 1 : -1;
  }
  if (latestIdentifier === currentIdentifier) return 0;
  return latestIdentifier > currentIdentifier ? 1 : -1;
}

function comparePrerelease(current: string, latest: string): number {
  const currentIdentifiers = current.split('.');
  const latestIdentifiers = latest.split('.');
  const identifierCount = Math.max(currentIdentifiers.length, latestIdentifiers.length);

  for (let index = 0; index < identifierCount; index++) {
    const currentIdentifier = currentIdentifiers[index];
    const latestIdentifier = latestIdentifiers[index];

    if (latestIdentifier === undefined) return -1;
    if (currentIdentifier === undefined) return 1;
    if (latestIdentifier === currentIdentifier) continue;

    const currentIsNumeric = /^\d+$/.test(currentIdentifier);
    const latestIsNumeric = /^\d+$/.test(latestIdentifier);

    if (currentIsNumeric && latestIsNumeric) {
      return compareNumericIdentifiers(latestIdentifier, currentIdentifier);
    }
    if (latestIsNumeric) return -1;
    if (currentIsNumeric) return 1;
    return latestIdentifier > currentIdentifier ? 1 : -1;
  }

  return 0;
}

/**
 * Returns true if `latest` is strictly newer than `current`.
 */
export function isNewerVersion(current: string, latest: string): boolean {
  if (!current || !latest) return false;

  const currentParsed = parseSemver(current);
  const latestParsed = parseSemver(latest);

  if (!currentParsed || !latestParsed) {
    return false;
  }

  if (latestParsed.major > currentParsed.major) return true;
  if (latestParsed.major < currentParsed.major) return false;

  if (latestParsed.minor > currentParsed.minor) return true;
  if (latestParsed.minor < currentParsed.minor) return false;

  if (latestParsed.patch > currentParsed.patch) return true;
  if (latestParsed.patch < currentParsed.patch) return false;

  // Both have the same major.minor.patch
  // A version with a prerelease is considered older than a version without one (e.g. 1.0.0 > 1.0.0-beta)
  if (currentParsed.prerelease && !latestParsed.prerelease) {
    return true;
  }
  if (!currentParsed.prerelease && latestParsed.prerelease) {
    return false;
  }
  if (currentParsed.prerelease && latestParsed.prerelease) {
    return comparePrerelease(currentParsed.prerelease, latestParsed.prerelease) > 0;
  }

  return false;
}
