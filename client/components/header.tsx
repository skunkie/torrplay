// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { forwardRef, useCallback, useEffect, useRef, useState } from 'react';
import useSWR from 'swr';

import { getSystemInfo } from '@/lib/api/system';
import { useOptionalAppUpdate } from '@/lib/app-update-context';
import { useAuth } from '@/lib/auth-context';
import { useLiveUpdates } from '@/lib/live-updates-context';

import { HeaderLayout } from './header-layout';

export interface HeaderProps {
  homeHref: string,
  onMetricsClick: () => void,
  onSettingsClick: () => void,
  onSystemInfoClick: () => void,
  onTitleSearch: (query: string) => void,
  inert?: boolean,
  searchQuery?: string
}

export const Header = forwardRef<HTMLDivElement, HeaderProps>((
  {
    homeHref,
    onMetricsClick,
    onSettingsClick,
    onSystemInfoClick,
    onTitleSearch,
    inert,
    searchQuery,
  }, ref) => {
  const { liveUpdatesPaused, setLiveUpdatesPaused } = useLiveUpdates();
  const { isAuthenticated, logout, auth } = useAuth();
  const update = useOptionalAppUpdate();
  const [isHidden, setIsHidden] = useState(false);
  const lastScrollY = useRef(0);

  const { data: systemInfo } = useSWR('/api/system/info', () => getSystemInfo(), {
    revalidateOnFocus: false,
    revalidateOnReconnect: true,
    refreshInterval: 0,
  });

  const version = systemInfo ? `v${systemInfo.version}` : null;
  const hasUpdate = update?.isSupported && update.status === 'available';

  const handleScroll = useCallback(() => {
    const currentScrollY = window.scrollY;
    if (window.innerWidth < 768) {
      if (currentScrollY > lastScrollY.current && currentScrollY > 100) {
        setIsHidden(true);
      } else {
        setIsHidden(false);
      }
    } else {
      setIsHidden(false);
    }
    lastScrollY.current = currentScrollY;
  }, []);

  const handleResize = useCallback(() => {
    if (window.innerWidth >= 768) {
      setIsHidden(false);
    }
  }, []);

  useEffect(() => {
    window.addEventListener('scroll', handleScroll, { passive: true });
    window.addEventListener('resize', handleResize);

    return () => {
      window.removeEventListener('scroll', handleScroll);
      window.removeEventListener('resize', handleResize);
    };
  }, [handleScroll, handleResize]);

  const handlePauseClick = () => {
    setLiveUpdatesPaused(!liveUpdatesPaused);
  };

  return (
    <HeaderLayout
      homeHref={homeHref}
      ref={ref}
      onMetricsClick={onMetricsClick}
      onSettingsClick={onSettingsClick}
      onSystemInfoClick={onSystemInfoClick}
      onTitleSearch={onTitleSearch}
      searchQuery={searchQuery}
      liveUpdatesPaused={liveUpdatesPaused}
      handlePauseClick={handlePauseClick}
      version={version}
      hasUpdate={hasUpdate}
      onUpdateClick={update?.isSupported ? () => update.setIsDialogOpen(true) : undefined}
      isAuthenticated={isAuthenticated}
      logout={logout}
      auth={auth}
      isHidden={isHidden}
      inert={inert}
    />
  );
});

Header.displayName = 'Header';
