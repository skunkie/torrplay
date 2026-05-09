// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import './globals.css';

import type { Metadata, Viewport } from 'next';
import type React from 'react';
import { Toaster } from 'sonner';

import { ScrollButtons } from '@/components/ui/scroll-buttons';
import { LiveUpdatesProvider } from '@/lib/live-updates-context';

import { Provider } from './provider';

export const metadata: Metadata = {
  title: 'TorrPlay',
  description: 'Stream torrents',
  icons: {
    icon: [
      { url: '/icon-16x16.png', sizes: '16x16', type: 'image/png' },
      { url: '/icon-32x32.png', sizes: '32x32', type: 'image/png' },
      { url: '/icon-64x64.png', sizes: '64x64', type: 'image/png' },
      { url: '/icon-128x128.png', sizes: '128x128', type: 'image/png' },
      { url: '/icon-256x256.png', sizes: '256x256', type: 'image/png' },
      { url: '/icon-512x512.png', sizes: '512x512', type: 'image/png' },
    ],
    apple: '/icon-512x512.png',
  },
};

export const viewport: Viewport = {
  userScalable: false,
  initialScale: 1,
  maximumScale: 1,
  minimumScale: 1,
  width: 'device-width',
  viewportFit: 'cover',
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode
}>) {
  return (
    <html lang='en'>
      <body className={'font-sans antialiased dark'}>
        <Provider>
          <LiveUpdatesProvider>
            {children}
          </LiveUpdatesProvider>
        </Provider>
        <Toaster theme='dark' />
        <ScrollButtons />
      </body>
    </html>
  );
}
