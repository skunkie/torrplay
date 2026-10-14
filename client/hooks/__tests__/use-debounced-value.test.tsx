// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { useDebouncedValue } from '../use-debounced-value';

describe('useDebouncedValue', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('returns the latest value once it has held for the delay', () => {
    const { result, rerender } = renderHook(
      ({ value }: { value: string }) => useDebouncedValue(value, 300),
      { initialProps: { value: '' } },
    );

    rerender({ value: 'sin' });
    act(() => vi.advanceTimersByTime(200));
    rerender({ value: 'sintel' });
    act(() => vi.advanceTimersByTime(200));
    expect(result.current).toBe('');

    act(() => vi.advanceTimersByTime(100));
    expect(result.current).toBe('sintel');
  });
});
