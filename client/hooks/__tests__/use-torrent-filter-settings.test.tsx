// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  STORAGE_KEY_CATEGORY_FILTER,
  STORAGE_KEY_SORT_BY,
} from '@/lib/torrent-filter-storage';

import { useTorrentFilterSettings } from '../use-torrent-filter-settings';

const navigationMocks = vi.hoisted(() => ({
  pathname: '/',
  replace: vi.fn(),
}));

vi.mock('next/navigation', () => ({
  usePathname: () => navigationMocks.pathname,
  useRouter: () => ({ replace: navigationMocks.replace }),
  useSearchParams: () => new URLSearchParams(),
}));

describe('useTorrentFilterSettings', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    navigationMocks.pathname = '/';
  });

  it('provides default filter settings when storage and params are empty', () => {
    const { result } = renderHook(() =>
      useTorrentFilterSettings({ searchParams: new URLSearchParams() })
    );

    expect(result.current.categoryFilter).toBe('');
    expect(result.current.sortBy).toBe('date');
    expect(result.current.titleFilter).toBe('');
  });

  it('restores stored selections from localStorage', () => {
    localStorage.setItem(STORAGE_KEY_CATEGORY_FILTER, 'Sci-Fi');
    localStorage.setItem(STORAGE_KEY_SORT_BY, 'size');

    const { result } = renderHook(() =>
      useTorrentFilterSettings({ searchParams: new URLSearchParams() })
    );

    expect(result.current.categoryFilter).toBe('Sci-Fi');
    expect(result.current.sortBy).toBe('size');
  });

  it('prioritizes URL search params over localStorage and persists them', () => {
    localStorage.setItem(STORAGE_KEY_CATEGORY_FILTER, 'OldCategory');
    localStorage.setItem(STORAGE_KEY_SORT_BY, 'date');

    const params = new URLSearchParams('category=Documentary&sortBy=updated');
    const { result } = renderHook(() =>
      useTorrentFilterSettings({ searchParams: params })
    );

    expect(result.current.categoryFilter).toBe('Documentary');
    expect(result.current.sortBy).toBe('updated');

    expect(localStorage.getItem(STORAGE_KEY_CATEGORY_FILTER)).toBe('Documentary');
    expect(localStorage.getItem(STORAGE_KEY_SORT_BY)).toBe('updated');
  });

  it('updates categoryFilter and persists to localStorage', () => {
    const { result } = renderHook(() =>
      useTorrentFilterSettings({ searchParams: new URLSearchParams() })
    );

    act(() => {
      result.current.handleCategoryFilterChange('Anime');
    });

    expect(result.current.categoryFilter).toBe('Anime');
    expect(localStorage.getItem(STORAGE_KEY_CATEGORY_FILTER)).toBe('Anime');
    expect(navigationMocks.replace).toHaveBeenLastCalledWith('/?category=Anime', { scroll: false });

    // Selecting 'all' clears the filter
    act(() => {
      result.current.handleCategoryFilterChange('all');
    });

    expect(result.current.categoryFilter).toBe('');
    expect(localStorage.getItem(STORAGE_KEY_CATEGORY_FILTER)).toBeNull();
    expect(navigationMocks.replace).toHaveBeenLastCalledWith('/', { scroll: false });
  });

  it('updates sortBy and persists to localStorage', () => {
    const { result } = renderHook(() =>
      useTorrentFilterSettings({ searchParams: new URLSearchParams() })
    );

    act(() => {
      result.current.handleSortByChange('name');
    });

    expect(result.current.sortBy).toBe('name');
    expect(localStorage.getItem(STORAGE_KEY_SORT_BY)).toBe('name');
    expect(navigationMocks.replace).toHaveBeenLastCalledWith('/?sortBy=name', { scroll: false });

    // Invalid sort by values are ignored
    act(() => {
      result.current.handleSortByChange('invalid_sort');
    });
    expect(result.current.sortBy).toBe('name');
  });

  it('preserves unrelated params and replaces the legacy sort alias', () => {
    navigationMocks.pathname = '/demo';
    const params = new URLSearchParams('modal=stats&sort=size');
    const { result } = renderHook(() =>
      useTorrentFilterSettings({ searchParams: params })
    );

    act(() => {
      result.current.handleSortByChange('updated');
    });

    expect(navigationMocks.replace).toHaveBeenLastCalledWith(
      '/demo?modal=stats&sortBy=updated',
      { scroll: false }
    );
  });

  it('synchronizes state and storage when URL params change', () => {
    const { result, rerender } = renderHook(
      ({ params }: { params: URLSearchParams }) =>
        useTorrentFilterSettings({ searchParams: params }),
      { initialProps: { params: new URLSearchParams('category=Movies&sortBy=name') } }
    );

    rerender({ params: new URLSearchParams('category=Series&sortBy=size') });

    expect(result.current.categoryFilter).toBe('Series');
    expect(result.current.sortBy).toBe('size');
    expect(localStorage.getItem(STORAGE_KEY_CATEGORY_FILTER)).toBe('Series');
    expect(localStorage.getItem(STORAGE_KEY_SORT_BY)).toBe('size');
  });

  it('updates titleFilter', () => {
    const { result } = renderHook(() =>
      useTorrentFilterSettings({ searchParams: new URLSearchParams() })
    );

    act(() => {
      result.current.handleTitleFilterChange('matrix');
    });

    expect(result.current.titleFilter).toBe('matrix');
  });

  it('handles responsive layout transitions between mobile and desktop', () => {
    let listener: ((e: { matches: boolean }) => void) | null = null;
    const mediaQueryMock = {
      matches: true,
      media: '(min-width: 768px)',
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn((_event: string, cb: (e: { matches: boolean }) => void) => {
        listener = cb;
      }),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    };

    vi.spyOn(window, 'matchMedia').mockReturnValue(mediaQueryMock as unknown as MediaQueryList);

    const { result } = renderHook(() =>
      useTorrentFilterSettings({ searchParams: new URLSearchParams() })
    );

    expect(result.current.isDesktop).toBe(true);

    // Switch to mobile
    act(() => {
      listener?.({ matches: false });
    });

    expect(result.current.isDesktop).toBe(false);

    // Switch back to desktop
    act(() => {
      listener?.({ matches: true });
    });

    expect(result.current.isDesktop).toBe(true);
  });
});
