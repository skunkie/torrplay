// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

export const STORAGE_KEY_CATEGORY_FILTER = 'torrplay_filter_category';
export const STORAGE_KEY_SORT_BY = 'torrplay_filter_sort_by';

export type SortByOption = 'date' | 'updated' | 'name' | 'size';

export const VALID_SORT_OPTIONS: readonly SortByOption[] = ['date', 'updated', 'name', 'size'];

export function isValidSortBy(value: unknown): value is SortByOption {
  return typeof value === 'string' && (VALID_SORT_OPTIONS as readonly string[]).includes(value);
}

export function getStoredCategoryFilter(): string {
  if (typeof window === 'undefined') return '';
  try {
    return localStorage.getItem(STORAGE_KEY_CATEGORY_FILTER) || '';
  } catch {
    return '';
  }
}

export function setStoredCategoryFilter(category: string): void {
  if (typeof window === 'undefined') return;
  try {
    if (!category) {
      localStorage.removeItem(STORAGE_KEY_CATEGORY_FILTER);
    } else {
      localStorage.setItem(STORAGE_KEY_CATEGORY_FILTER, category);
    }
  } catch {
    // Ignore storage quota or access errors
  }
}

export function getStoredSortBy(): SortByOption {
  if (typeof window === 'undefined') return 'date';
  try {
    const saved = localStorage.getItem(STORAGE_KEY_SORT_BY);
    if (isValidSortBy(saved)) {
      return saved;
    }
  } catch {
    // Ignore storage quota or access errors
  }
  return 'date';
}

export function setStoredSortBy(sortBy: string): void {
  if (typeof window === 'undefined') return;
  try {
    if (isValidSortBy(sortBy)) {
      localStorage.setItem(STORAGE_KEY_SORT_BY, sortBy);
    }
  } catch {
    // Ignore storage quota or access errors
  }
}
