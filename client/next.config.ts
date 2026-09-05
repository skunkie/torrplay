// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  output: 'export',
  images: {
    unoptimized: true,
  },
  env: {
    NEXT_PUBLIC_APP_ARCH: process.env.APP_ARCH || process.env.NEXT_PUBLIC_APP_ARCH || '',
    NEXT_PUBLIC_RELEASES_URL: process.env.RELEASES_URL || process.env.NEXT_PUBLIC_RELEASES_URL || '',
  },
};

export default nextConfig;
