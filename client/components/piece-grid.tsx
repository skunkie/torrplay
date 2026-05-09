// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useEffect, useRef, useState } from 'react';

import { usePieceCells } from '@/hooks/use-piece-cells';
import { pieceGridSettings } from '@/lib/piece-grid-settings';
import type { PieceInfo } from '@/lib/types/api';

interface PieceGridProps {
  totalPieces: number,
  pieces: PieceInfo[]
}

const Legend = () => (
  <div className='flex flex-wrap justify-center gap-x-4 gap-y-2 mt-2'>
    <div className='flex items-center space-x-2'>
      <div className='w-3 h-3 border border-gray-300 rounded-sm' />
      <span className='text-xs text-muted-foreground'>Incomplete</span>
    </div>
    <div className='flex items-center space-x-2'>
      <div className='w-3 h-3 bg-blue-500 rounded-sm' />
      <span className='text-xs text-muted-foreground'>Complete</span>
    </div>
  </div>
);

const PieceGrid = ({ totalPieces, pieces }: PieceGridProps) => {
  const [dimensions, setDimensions] = useState({ width: 0, height: 0 });
  const { width } = dimensions;
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  const {
    pieceSize,
    gapBetweenPieces,
    completeColor,
    incompleteColor,
    borderColor,
    borderWidth,
  } = pieceGridSettings.default;

  const pieceSizeWithGap = pieceSize + gapBetweenPieces;
  const piecesInOneRow = width > 0 && pieceSizeWithGap > 0 ? Math.floor(width / pieceSizeWithGap) : 0;

  const { cells, height } = usePieceCells({ totalPieces, pieces, piecesInOneRow, pieceSizeWithGap });

  useEffect(() => {
    const resizeObserver = new ResizeObserver(entries => {
      if (entries[0]) {
        setDimensions({ width: entries[0].contentRect.width, height: entries[0].contentRect.height });
      }
    });

    if (containerRef.current) {
      resizeObserver.observe(containerRef.current);
    }

    return () => {
      resizeObserver.disconnect();
    };
  }, []);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !width || !height) return;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    canvas.width = width;
    canvas.height = height;

    ctx.clearRect(0, 0, width, height);

    const startingXPoint = piecesInOneRow > 0 ? Math.ceil((width - pieceSizeWithGap * piecesInOneRow) / 2) : 0;

    cells.forEach(({ isComplete }, i) => {
      const currentRow = piecesInOneRow > 0 ? i % piecesInOneRow : 0;
      const currentColumn = piecesInOneRow > 0 ? Math.floor(i / piecesInOneRow) : 0;

      const x = currentRow * pieceSizeWithGap + startingXPoint;
      const y = currentColumn * pieceSizeWithGap;

      ctx.fillStyle = isComplete ? completeColor : incompleteColor;
      ctx.strokeStyle = borderColor;
      ctx.lineWidth = borderWidth;

      ctx.fillRect(x, y, pieceSize, pieceSize);
      ctx.strokeRect(x, y, pieceSize, pieceSize);
    });
  }, [
    width,
    height,
    cells,
    piecesInOneRow,
    pieceSizeWithGap,
    pieceSize,
    completeColor,
    incompleteColor,
    borderColor,
    borderWidth,
  ]);

  return (
    <div ref={containerRef}>
      <canvas ref={canvasRef} />
      <Legend />
    </div>
  );
};

export default PieceGrid;
