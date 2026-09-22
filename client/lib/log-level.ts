// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import type { Settings } from './types/api';

export type LogLevel = NonNullable<Settings['logLevel']>;

// Older or external API clients may have persisted levels outside the UI enum.
export function normalizeLogLevel(value: unknown): LogLevel {
  return value === 'DEBUG' || value === 'INFO' || value === 'WARN' || value === 'ERROR'
    ? value
    : 'INFO';
}
