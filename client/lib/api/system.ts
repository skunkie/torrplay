// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { api } from '@/lib/api-client';
import { SystemInfo } from '@/lib/types/api';

export async function getSystemInfo(): Promise<SystemInfo> {
  return api.get<SystemInfo>('/api/system/info');
}
