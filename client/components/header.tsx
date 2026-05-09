// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { forwardRef, useCallback, useEffect, useRef, useState } from 'react';
import useSWR from 'swr';

import { getSystemInfo } from '@/lib/api/system';
import { useAuth } from '@/lib/auth-context';
import { useLiveUpdates } from '@/lib/live-updates-context';

import { HeaderLayout } from './header-layout';

interface HeaderProps {
  homeHref: string,
  onSettingsClick: () => void,
  onMetricsClick: () => void,
  onTitleSearch: (query: string) => void
}

export const Header = forwardRef<HTMLDivElement, HeaderProps>((
  {
    homeHref,
    onSettingsClick,
    onMetricsClick,
    onTitleSearch,
  }, ref) => {
  const { liveUpdatesPaused, setLiveUpdatesPaused } = useLiveUpdates();
  const { isAuthenticated, logout, auth } = useAuth();
  const [isHidden, setIsHidden] = useState(false);
  const lastScrollY = useRef(0);

  const { data: systemInfo } = useSWR('/api/system/info', () => getSystemInfo(), {
    revalidateOnFocus: false,
    revalidateOnReconnect: true,
    refreshInterval: 0,
  });

  const version = systemInfo ? `v${systemInfo.version}` : null;

  const handleScroll = useCallback(() => {
    const currentScrollY = window.scrollY;
    if (window.innerWidth < 768) {
      if (currentScrollY > lastScrollY.current && currentScrollY > 100) {
        setIsHidden(true);
      } else {
        setIsHidden(false);
      }
    }
    lastScrollY.current = currentScrollY;
  }, []);

  useEffect(() => {
    window.addEventListener('scroll', handleScroll, { passive: true });

    return () => {
      window.removeEventListener('scroll', handleScroll);
    };
  }, [handleScroll]);

  const handlePauseClick = () => {
    setLiveUpdatesPaused(!liveUpdatesPaused);
  };

  return (
    <HeaderLayout
      homeHref={homeHref}
      ref={ref}
      onSettingsClick={onSettingsClick}
      onMetricsClick={onMetricsClick}
      onTitleSearch={onTitleSearch}
      liveUpdatesPaused={liveUpdatesPaused}
      handlePauseClick={handlePauseClick}
      version={version}
      isAuthenticated={isAuthenticated}
      logout={logout}
      auth={auth}
      isHidden={isHidden}
    />
  );
});

Header.displayName = 'Header';
