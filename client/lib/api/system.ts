// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { api, apiFetch } from '@/lib/api-client';
import { LogEntry, SystemInfo, SystemMetrics } from '@/lib/types/api';

export async function getSystemLogs(): Promise<LogEntry[]> {
  const response = await apiFetch('/api/system/logs', { method: 'GET' });
  return response.json() as Promise<LogEntry[]>;
}

export async function getSystemInfo(): Promise<SystemInfo> {
  return api.get<SystemInfo>('/api/system/info');
}

export async function getSystemMetrics(): Promise<SystemMetrics> {
  return api.get<SystemMetrics>('/api/system/metrics');
}
