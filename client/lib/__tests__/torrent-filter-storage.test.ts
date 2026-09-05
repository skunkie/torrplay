// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  getStoredCategoryFilter,
  getStoredSortBy,
  isValidSortBy,
  setStoredCategoryFilter,
  setStoredSortBy,
  STORAGE_KEY_CATEGORY_FILTER,
  STORAGE_KEY_SORT_BY,
} from '../torrent-filter-storage';

describe('torrent-filter-storage', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  describe('category filter storage', () => {
    it('returns empty string by default', () => {
      expect(getStoredCategoryFilter()).toBe('');
    });

    it('persists and retrieves category filter', () => {
      setStoredCategoryFilter('Movies');
      expect(localStorage.getItem(STORAGE_KEY_CATEGORY_FILTER)).toBe('Movies');
      expect(getStoredCategoryFilter()).toBe('Movies');
    });

    it('removes item when cleared or empty', () => {
      setStoredCategoryFilter('Movies');
      setStoredCategoryFilter('');
      expect(localStorage.getItem(STORAGE_KEY_CATEGORY_FILTER)).toBeNull();
      expect(getStoredCategoryFilter()).toBe('');
    });

    it('handles localStorage errors gracefully', () => {
      vi.spyOn(Storage.prototype, 'getItem').mockImplementationOnce(() => {
        throw new Error('Access denied');
      });
      expect(getStoredCategoryFilter()).toBe('');

      vi.spyOn(Storage.prototype, 'setItem').mockImplementationOnce(() => {
        throw new Error('Quota exceeded');
      });
      expect(() => setStoredCategoryFilter('Movies')).not.toThrow();
    });
  });

  describe('sort by storage', () => {
    it('validates sort options correctly', () => {
      expect(isValidSortBy('date')).toBe(true);
      expect(isValidSortBy('updated')).toBe(true);
      expect(isValidSortBy('name')).toBe(true);
      expect(isValidSortBy('size')).toBe(true);
      expect(isValidSortBy('invalid')).toBe(false);
      expect(isValidSortBy(null)).toBe(false);
      expect(isValidSortBy(123)).toBe(false);
    });

    it('returns date by default', () => {
      expect(getStoredSortBy()).toBe('date');
    });

    it('persists and retrieves valid sort option', () => {
      setStoredSortBy('size');
      expect(localStorage.getItem(STORAGE_KEY_SORT_BY)).toBe('size');
      expect(getStoredSortBy()).toBe('size');

      setStoredSortBy('updated');
      expect(getStoredSortBy()).toBe('updated');
    });

    it('ignores invalid sort option on set and returns fallback on get', () => {
      setStoredSortBy('size');
      setStoredSortBy('invalid_sort');
      expect(getStoredSortBy()).toBe('size');

      localStorage.setItem(STORAGE_KEY_SORT_BY, 'corrupted_value');
      expect(getStoredSortBy()).toBe('date');
    });

    it('handles localStorage errors gracefully', () => {
      vi.spyOn(Storage.prototype, 'getItem').mockImplementationOnce(() => {
        throw new Error('Access denied');
      });
      expect(getStoredSortBy()).toBe('date');

      vi.spyOn(Storage.prototype, 'setItem').mockImplementationOnce(() => {
        throw new Error('Quota exceeded');
      });
      expect(() => setStoredSortBy('name')).not.toThrow();
    });
  });
});
