// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { useMemo } from 'react';

import { MetricsDialogLayout } from '@/components/metrics-dialog-layout';

interface DemoMetricsDialogProps {
  open: boolean,
  onOpenChange: (open: boolean) => void
}

export function DemoMetricsDialog({ open, onOpenChange }: DemoMetricsDialogProps) {
  const memoryStats = useMemo(() => {
    return {
      usedMemory: 268435456,
      maxMemory: 536870912,
      activeTorrents: 4,
      totalPieces: 4000
    };
  }, []);

  const systemMetrics = useMemo(() => {
    return {
      activeTorrents: 4,
      downloadSpeed: 1234567,
      uploadSpeed: 123456,
    };
  }, []);

  if (!open) return null;

  return (
    <MetricsDialogLayout
      open={open}
      onOpenChange={onOpenChange}
      memoryStats={memoryStats}
      systemMetrics={systemMetrics}
    />
  );
}
