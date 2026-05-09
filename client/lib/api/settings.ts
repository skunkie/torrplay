// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { api } from '../api-client';
import type { Settings } from '../types/api';

export async function getSettings(): Promise<Settings> {
  return api.get<Settings>('/api/v1/settings');
}

export async function updateSettings(data: Partial<Settings>): Promise<Settings> {
  return api.patch<Settings>('/api/v1/settings', data);
}
