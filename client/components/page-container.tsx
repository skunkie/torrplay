// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { ReactNode } from 'react';

interface PageContainerProps {
  children: ReactNode,
  inert?: boolean
}

export function PageContainer({ children, inert }: PageContainerProps) {
  return (
    <main
      inert={inert}
      className='container mx-auto px-3 py-3 max-w-screen-tv bg-background'
    >
      {children}
    </main>
  );
}
