// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { usePathname } from 'next/navigation';
import { ReactNode } from 'react';

import { AuthProvider, DemoAuthProvider } from '@/lib/auth-context';

export function Provider({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const isDemo = pathname.startsWith('/demo');

  return isDemo ? <DemoAuthProvider>{children}</DemoAuthProvider> : <AuthProvider>{children}</AuthProvider>;
}
