// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useMemo } from 'react';

import type { PieceInfo } from '@/lib/types/api';

interface UsePieceCellsProps {
  totalPieces: number,
  pieces: PieceInfo[],
  piecesInOneRow: number,
  pieceSizeWithGap: number
}

export const usePieceCells = ({
  totalPieces,
  pieces,
  piecesInOneRow,
  pieceSizeWithGap,
}: UsePieceCellsProps) => {
  const piecesByIndex = useMemo(() => {
    const map = new Map<number, PieceInfo>();
    for (const piece of pieces) {
      map.set(piece.index, piece);
    }
    return map;
  }, [pieces]);

  return useMemo(() => {
    if (totalPieces === 0 || piecesInOneRow === 0) {
      return { cells: [], height: 0 };
    }

    const numPiecesToRender = Math.min(totalPieces, piecesInOneRow * Math.ceil(totalPieces / piecesInOneRow));

    const cells = [];
    for (let i = 0; i < numPiecesToRender; i++) {
      const piece = piecesByIndex.get(i);
      cells.push({ isComplete: piece?.complete ?? false });
    }

    const calculatedHeight = piecesInOneRow > 0 ? Math.ceil(cells.length / piecesInOneRow) * pieceSizeWithGap : 0;

    return { cells, height: calculatedHeight };
  }, [totalPieces, piecesInOneRow, pieceSizeWithGap, piecesByIndex]);
};
