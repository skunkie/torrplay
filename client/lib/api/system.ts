// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { api } from '@/lib/api-client';
import { SystemInfo, SystemMetrics } from '@/lib/types/api';

export async function getSystemInfo(): Promise<SystemInfo> {
  return api.get<SystemInfo>('/api/system/info');
}

export async function getSystemMetrics(): Promise<SystemMetrics> {
  return api.get<SystemMetrics>('/api/system/metrics');
}
