// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { usePathname } from 'next/navigation';
import { ReactNode } from 'react';

import { AppUpdateProvider, DemoAppUpdateProvider } from '@/lib/app-update-context';
import { AuthProvider, DemoAuthProvider } from '@/lib/auth-context';

export function Provider({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const isDemo = pathname.startsWith('/demo');

  return isDemo ? (
    <DemoAuthProvider>
      <DemoAppUpdateProvider>{children}</DemoAppUpdateProvider>
    </DemoAuthProvider>
  ) : (
    <AuthProvider>
      <AppUpdateProvider>{children}</AppUpdateProvider>
    </AuthProvider>
  );
}
