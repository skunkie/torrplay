// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { Suspense } from 'react';

import TorrPlayPage from '@/components/torrplay-page';

export default function Home() {
  return (
    <Suspense fallback={<div>Loading...</div>}>
      <TorrPlayPage homeHref='/' />
    </Suspense>
  );
}
