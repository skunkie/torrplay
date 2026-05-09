// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { ReactNode } from 'react';

interface PageContainerProps {
  children: ReactNode
}

export function PageContainer({ children }: PageContainerProps) {
  return (
    <main className='container mx-auto px-3 py-3 max-w-screen-tv bg-background'>
      {children}
    </main>
  );
}
