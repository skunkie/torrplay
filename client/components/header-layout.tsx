// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { Ban, BarChart3, Info, LogOut, RefreshCw, Search, Settings } from 'lucide-react';
import Link from 'next/link';
import { forwardRef, useEffect, useState } from 'react';

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
  onMetricsClick: () => void,
  onSettingsClick: () => void,
  onSystemInfoClick: () => void,
  onTitleSearch: (query: string) => void,
  liveUpdatesPaused: boolean,
  handlePauseClick: () => void,
  version: string | null,
  isAuthenticated: boolean,
  logout: () => void,
  auth: AuthContextType['auth'],
  isHidden: boolean,
  inert?: boolean,
  searchQuery?: string
}

export const HeaderLayout = forwardRef<HTMLDivElement, HeaderLayoutProps>((
  {
    homeHref,
    onMetricsClick,
    onSettingsClick,
    onSystemInfoClick,
    onTitleSearch,
    liveUpdatesPaused,
    handlePauseClick,
    version,
    isAuthenticated,
    logout,
    auth,
    isHidden,
    inert,
    searchQuery,
  }, ref) => {
  const [internalQuery, setInternalQuery] = useState(searchQuery ?? '');

  useEffect(() => {
    if (searchQuery !== undefined) {
      setInternalQuery(searchQuery);
    }
  }, [searchQuery]);

  const searchVal = searchQuery !== undefined ? searchQuery : internalQuery;

  const handleSearchChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    if (searchQuery === undefined) {
      setInternalQuery(value);
    }
    onTitleSearch(value);
  };

  return (
    <header
      ref={ref}
      inert={inert}
      aria-hidden={isHidden ? true : undefined}
      className={`border-b border-border bg-card/50 backdrop-blur-sm sticky top-0 z-50 pt-[env(safe-area-inset-top)] pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] transition-transform duration-300 ${
        isHidden ? '-translate-y-full pointer-events-none invisible md:visible md:pointer-events-auto' : 'translate-y-0'
      } md:translate-y-0`}
    >
      <div className='container mx-auto px-3 sm:px-4 py-2 space-y-3 sm:space-y-4 max-w-screen-tv'>
        <div className='flex items-center justify-between gap-2 sm:gap-4'>
          <div className='flex items-center gap-3 sm:gap-6 md:gap-8 lg:gap-10 min-w-0'>
            <Link href={homeHref}
              className='flex items-center gap-1.5 sm:gap-2 shrink-0'>
              <h1 className='text-xl xs:text-2xl sm:text-3xl font-semibold text-foreground tracking-tight'>
                TorrPlay
                {version && (
                  <sup className='text-[10px] xs:text-xs font-normal text-muted-foreground ml-1'>
                    <span className='font-semibold'>{version}</span>
                  </sup>
                )}
              </h1>
            </Link>

            <div className='hidden md:block'>
              <div className='relative w-44 md:w-52 lg:w-64'>
                <Search className='absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground' />
                <Input
                  type='search'
                  value={searchVal}
                  placeholder='Search by title...'
                  aria-label='Search by title'
                  onChange={handleSearchChange}
                  className='pl-9 bg-muted/50'
                />
              </div>
            </div>
          </div>

          <div className='flex items-center gap-1 sm:gap-2 shrink-0'>
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant='ghost'
                    size='icon'
                    onClick={handlePauseClick}
                    className='h-8 w-8 sm:h-9 sm:w-9 text-muted-foreground hover:text-foreground'
                  >
                    {liveUpdatesPaused ? <Ban className='size-5 sm:size-6' /> : <RefreshCw className='size-5 sm:size-6' />}
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
              className='h-8 w-8 sm:h-9 sm:w-9 text-muted-foreground hover:text-foreground'
            >
              <BarChart3 className='size-5 sm:size-6' />
              <span className='sr-only'>Metrics</span>
            </Button>
            <Button
              variant='ghost'
              size='icon'
              onClick={onSystemInfoClick}
              className='h-8 w-8 sm:h-9 sm:w-9 text-muted-foreground hover:text-foreground'
            >
              <Info className='size-5 sm:size-6' />
              <span className='sr-only'>System Info</span>
            </Button>
            <Button
              variant='ghost'
              size='icon'
              onClick={onSettingsClick}
              className='h-8 w-8 sm:h-9 sm:w-9 text-muted-foreground hover:text-foreground'
            >
              <Settings className='size-5 sm:size-6' />
              <span className='sr-only'>Settings</span>
            </Button>
            {isAuthenticated && auth?.enabled && (
              <Button
                variant='ghost'
                size='icon'
                onClick={logout}
                className='h-8 w-8 sm:h-9 sm:w-9 text-muted-foreground hover:text-foreground'
              >
                <LogOut className='size-5 sm:size-6' />
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
            value={searchVal}
            placeholder='Search by title...'
            aria-label='Search by title'
            onChange={handleSearchChange}
            className='pl-9 bg-muted/50'
          />
        </div>
      </div>
    </header>
  );
});

HeaderLayout.displayName = 'HeaderLayout';
