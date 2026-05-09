// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { Ghost } from 'lucide-react';
import Link from 'next/link';

import { buttonVariants } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export default function NotFound() {
  return (
    <div className='flex flex-col items-center justify-center min-h-[calc(100vh-10rem)]'>
      <div className='flex flex-col items-center text-center space-y-4'>
        <Ghost className='h-24 w-24 text-muted-foreground/50' />
        <div className='space-y-2'>
          <h1 className='text-2xl font-semibold tracking-tight'>
            404 - Page Not Found
          </h1>
          <p className='text-sm text-muted-foreground'>
            Oops! The page you&apos;re looking for doesn&apos;t exist or has been moved.
          </p>
        </div>
        <Link
          href='/'
          className={cn(
            buttonVariants({ variant: 'default' }),
            'mt-4',
          )}
        >
          Go back to Home
        </Link>
      </div>
    </div>
  );
}
