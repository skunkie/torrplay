// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { usePieceCells } from '@/hooks/use-piece-cells';
import type { PieceInfo } from '@/lib/types/api';

describe('usePieceCells', () => {
  it('returns empty cells and 0 height when totalPieces is 0', () => {
    const { result } = renderHook(() =>
      usePieceCells({
        pieces: [],
        pieceSizeWithGap: 12,
        piecesInOneRow: 10,
        totalPieces: 0,
      }),
    );

    expect(result.current.cells).toEqual([]);
    expect(result.current.height).toBe(0);
  });

  it('returns empty cells and 0 height when piecesInOneRow is 0', () => {
    const { result } = renderHook(() =>
      usePieceCells({
        pieces: [{ complete: true, inMemory: true, index: 0, size: 1024 }],
        pieceSizeWithGap: 12,
        piecesInOneRow: 0,
        totalPieces: 1,
      }),
    );

    expect(result.current.cells).toEqual([]);
    expect(result.current.height).toBe(0);
  });

  it('computes cells with complete and incomplete status accurately', () => {
    const mockPieces: PieceInfo[] = [
      { complete: true, inMemory: true, index: 0, size: 1024 },
      { complete: false, inMemory: false, index: 1, size: 1024 },
      { complete: true, inMemory: true, index: 2, size: 1024 },
    ];

    const { result } = renderHook(() =>
      usePieceCells({
        pieces: mockPieces,
        pieceSizeWithGap: 10,
        piecesInOneRow: 2,
        totalPieces: 3,
      }),
    );

    expect(result.current.cells).toHaveLength(3);
    expect(result.current.cells[0]).toEqual({ isComplete: true });
    expect(result.current.cells[1]).toEqual({ isComplete: false });
    expect(result.current.cells[2]).toEqual({ isComplete: true });
    // 3 pieces with 2 per row = 2 rows * 10 = 20
    expect(result.current.height).toBe(20);
  });
});
