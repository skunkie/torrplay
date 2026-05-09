// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { Ban, BarChart3, LogOut, RefreshCw, Search, Settings } from 'lucide-react';
import Link from 'next/link';
import { forwardRef } from 'react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { AuthContextType } from '@/lib/auth-context';

interface HeaderLayoutProps {
  homeHref: string,
  onSettingsClick: () => void,
  onMetricsClick: () => void,
  onTitleSearch: (query: string) => void,
  liveUpdatesPaused: boolean,
  handlePauseClick: () => void,
  version: string | null,
  isAuthenticated: boolean,
  logout: () => void,
  auth: AuthContextType['auth'],
  isHidden: boolean
}

export const HeaderLayout = forwardRef<HTMLDivElement, HeaderLayoutProps>((
  {
    homeHref,
    onSettingsClick,
    onMetricsClick,
    onTitleSearch,
    liveUpdatesPaused,
    handlePauseClick,
    version,
    isAuthenticated,
    logout,
    auth,
    isHidden,
  }, ref) => {
  return (
    <header
      ref={ref}
      className={`border-b border-border bg-card/50 backdrop-blur-sm sticky top-0 z-50 transition-transform duration-300 ${
        isHidden ? '-translate-y-full' : 'translate-y-0'
      } md:translate-y-0`}
    >
      <div className='container mx-auto px-4 py-2 space-y-4 max-w-screen-tv'>
        <div className='flex items-center justify-between gap-4'>
          <div className='flex items-center gap-10'>
            <Link href={homeHref}
              className='flex items-center gap-2'>
              <h1 className='text-3xl font-semibold text-foreground'>
                TorrPlay
                {version && (
                  <sup className='text-xs font-normal text-muted-foreground ml-1'>
                    <span className='font-semibold'>{version}</span>
                  </sup>
                )}
              </h1>
            </Link>

            <div className='hidden md:block'>
              <div className='relative w-64'>
                <Search className='absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground' />
                <Input
                  type='search'
                  placeholder='Search by title...'
                  onChange={e => onTitleSearch(e.target.value)}
                  className='pl-9 bg-muted/50'
                />
              </div>
            </div>
          </div>

          <div className='flex items-center gap-2'>
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant='ghost'
                    size='icon'
                    onClick={handlePauseClick}
                    className='text-muted-foreground hover:text-foreground'
                  >
                    {liveUpdatesPaused ? <Ban className='size-6' /> : <RefreshCw className='size-6' />}
                    <span className='sr-only'>{liveUpdatesPaused ? 'Resume updates' : 'Pause updates'}</span>
                  </Button>
                </TooltipTrigger>
                <TooltipContent>
                  {liveUpdatesPaused ? 'Resume live updates' : 'Pause live updates'}
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>

            <Button
              variant='ghost'
              size='icon'
              onClick={onMetricsClick}
              className='text-muted-foreground hover:text-foreground'
            >
              <BarChart3 className='size-6' />
              <span className='sr-only'>Metrics</span>
            </Button>
            <Button
              variant='ghost'
              size='icon'
              onClick={onSettingsClick}
              className='text-muted-foreground hover:text-foreground'
            >
              <Settings className='size-6' />
              <span className='sr-only'>Settings</span>
            </Button>
            {isAuthenticated && auth?.enabled && (
              <Button
                variant='ghost'
                size='icon'
                onClick={logout}
                className='text-muted-foreground hover:text-foreground'
              >
                <LogOut className='size-6' />
                <span className='sr-only'>Logout</span>
              </Button>
            )}
          </div>
        </div>

        {/* Search bar for mobile. */}
        <div className='relative md:hidden'>
          <Search className='absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground' />
          <Input
            type='search'
            placeholder='Search by title...'
            onChange={e => onTitleSearch(e.target.value)}
            className='pl-9 bg-muted/50'
          />
        </div>
      </div>
    </header>
  );
});

HeaderLayout.displayName = 'HeaderLayout';
