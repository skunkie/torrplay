// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { useCallback, useEffect, useState } from 'react';

import {
  getStoredCategoryFilter,
  getStoredSortBy,
  isValidSortBy,
  setStoredCategoryFilter,
  setStoredSortBy,
} from '@/lib/torrent-filter-storage';

export interface UseTorrentFilterSettingsOptions {
  searchParams?: { get: (name: string) => string | null } | null
}

export function useTorrentFilterSettings(options: UseTorrentFilterSettingsOptions = {}) {
  const router = useRouter();
  const pathname = usePathname();
  const hookSearchParams = useSearchParams();
  const searchParams = options.searchParams !== undefined ? options.searchParams : hookSearchParams;
  const categoryParam = searchParams?.get('category');
  const sortParam = searchParams?.get('sortBy') || searchParams?.get('sort');

  const [titleFilter, setTitleFilter] = useState('');
  const [isDesktop, setIsDesktop] = useState(true);

  // Initialize categoryFilter from URL search params if present, else localStorage
  const [categoryFilter, setCategoryFilter] = useState<string>(() => {
    if (categoryParam !== null && categoryParam !== undefined) {
      return categoryParam === 'all' ? '' : categoryParam;
    }
    return getStoredCategoryFilter();
  });

  // Initialize sortBy from URL search params if present and valid, else localStorage
  const [sortBy, setSortBy] = useState<string>(() => {
    if (sortParam && isValidSortBy(sortParam)) {
      return sortParam;
    }
    return getStoredSortBy();
  });

  const replaceFilterParam = useCallback((key: 'category' | 'sortBy', value: string) => {
    const params = new URLSearchParams(searchParams?.toString());
    if (key === 'sortBy') {
      params.delete('sort');
    }
    if (value) {
      params.set(key, value);
    } else {
      params.delete(key);
    }
    const query = params.toString();
    router.replace(query ? `${pathname}?${query}` : pathname, { scroll: false });
  }, [pathname, router, searchParams]);

  const handleTitleFilterChange = useCallback((query: string) => {
    setTitleFilter(query);
  }, []);

  const handleCategoryFilterChange = useCallback((value: string) => {
    const nextCategory = value === 'all' ? '' : value;
    setCategoryFilter(nextCategory);
    setStoredCategoryFilter(nextCategory);
    replaceFilterParam('category', nextCategory);
  }, [replaceFilterParam]);

  const handleSortByChange = useCallback((value: string) => {
    if (isValidSortBy(value)) {
      setSortBy(value);
      setStoredSortBy(value);
      replaceFilterParam('sortBy', value);
    }
  }, [replaceFilterParam]);

  useEffect(() => {
    if (categoryParam === null || categoryParam === undefined) return;
    const nextCategory = categoryParam === 'all' ? '' : categoryParam;
    setCategoryFilter(nextCategory);
    setStoredCategoryFilter(nextCategory);
  }, [categoryParam]);

  useEffect(() => {
    if (!sortParam || !isValidSortBy(sortParam)) return;
    setSortBy(sortParam);
    setStoredSortBy(sortParam);
  }, [sortParam]);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const mediaQuery = window.matchMedia('(min-width: 768px)');
    const handleMediaChange = (e: { matches: boolean }) => {
      setIsDesktop(e.matches);
    };

    handleMediaChange(mediaQuery);
    mediaQuery.addEventListener('change', handleMediaChange);

    return () => {
      mediaQuery.removeEventListener('change', handleMediaChange);
    };
  }, []);

  return {
    titleFilter,
    setTitleFilter,
    handleTitleFilterChange,
    categoryFilter,
    setCategoryFilter,
    handleCategoryFilterChange,
    sortBy,
    setSortBy,
    handleSortByChange,
    isDesktop,
    setIsDesktop,
  };
}
