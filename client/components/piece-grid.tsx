// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useEffect, useRef, useState } from 'react';

import { pieceGridSettings } from '@/lib/piece-grid-settings';
import type { PieceInfo, ReaderInfo } from '@/lib/types/api';

interface PieceGridProps {
  totalPieces: number,
  pieces: PieceInfo[],
  readers: ReaderInfo[]
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
    <div className='flex items-center space-x-2'>
      <div className='w-3 h-3 bg-amber-500 rounded-sm' />
      <span className='text-xs text-muted-foreground'>Position</span>
    </div>
    <div className='flex items-center space-x-2'>
      <div className='w-3 h-3 border border-amber-500 rounded-sm' />
      <span className='text-xs text-muted-foreground'>Read Window</span>
    </div>
  </div>
);

const PieceGrid = ({ totalPieces, pieces, readers }: PieceGridProps) => {
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
    readerColor,
  } = pieceGridSettings.default;

  const pieceSizeWithGap = pieceSize + gapBetweenPieces;
  const piecesInOneRow = width > 0 && pieceSizeWithGap > 0 ? Math.floor(width / pieceSizeWithGap) : 0;

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

  const { cells, height, piecesStart, piecesEnd } = (() => {
    if (totalPieces === 0 || piecesInOneRow === 0) {
      return { cells: [], height: 0, piecesStart: 0, piecesEnd: 0 };
    }

    const piecesByIndex = new Map<number, PieceInfo>();
    for (const p of pieces) {
      piecesByIndex.set(p.index, p);
    }

    const cells = [];
    for (let i = 0; i < totalPieces; i++) {
      const piece = piecesByIndex.get(i);
      cells.push({ isComplete: piece?.complete ?? false });
    }

    const calculatedHeight = piecesInOneRow > 0 ? Math.ceil(cells.length / piecesInOneRow) * pieceSizeWithGap : 0;

    return { cells, height: calculatedHeight, piecesStart: 0, piecesEnd: totalPieces };
  })();

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !width || !height) return;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    canvas.width = width;
    canvas.height = height;

    ctx.clearRect(0, 0, width, height);

    const startingXPoint = piecesInOneRow > 0 ? Math.ceil((width - pieceSizeWithGap * piecesInOneRow) / 2) : 0;

    const cellsByIndex = new Map<number, boolean>();
    for (let i = 0; i < cells.length; i++) {
      cellsByIndex.set(piecesStart + i, cells[i].isComplete);
    }

    for (let i = piecesStart; i < piecesEnd; i++) {
      const col = (i - piecesStart) % piecesInOneRow;
      const row = Math.floor((i - piecesStart) / piecesInOneRow);
      const x = col * pieceSizeWithGap + startingXPoint;
      const y = row * pieceSizeWithGap;

      const isComplete = cellsByIndex.get(i) ?? false;
      ctx.fillStyle = isComplete ? completeColor : incompleteColor;
      ctx.strokeStyle = borderColor;
      ctx.lineWidth = borderWidth;

      ctx.fillRect(x, y, pieceSize, pieceSize);
      ctx.strokeRect(x, y, pieceSize, pieceSize);
    }

    readers.forEach(reader => {
      const windowStart = Math.max(piecesStart, Math.min(reader.start, piecesEnd - 1));
      const windowEnd = Math.max(windowStart + 1, Math.min(reader.end, piecesEnd));
      const posIdx = Math.max(piecesStart, Math.min(reader.position, piecesEnd - 1));
      for (let i = windowStart; i < windowEnd; i++) {
        if (i < piecesStart || i >= piecesEnd || piecesInOneRow === 0) continue;
        const col = (i - piecesStart) % piecesInOneRow;
        const row = Math.floor((i - piecesStart) / piecesInOneRow);
        const x = col * pieceSizeWithGap + startingXPoint;
        const y = row * pieceSizeWithGap;

        if (i === posIdx) {
          ctx.fillStyle = readerColor;
          ctx.strokeStyle = readerColor;
          ctx.lineWidth = borderWidth;
          ctx.fillRect(x, y, pieceSize, pieceSize);
          ctx.strokeRect(x, y, pieceSize, pieceSize);
        } else {
          ctx.strokeStyle = readerColor;
          ctx.lineWidth = borderWidth;
          ctx.strokeRect(x, y, pieceSize, pieceSize);
        }
      }
    });
  }, [
    width,
    height,
    cells,
    readers,
    piecesInOneRow,
    pieceSizeWithGap,
    pieceSize,
    completeColor,
    incompleteColor,
    borderColor,
    borderWidth,
    readerColor,
    totalPieces,
    piecesStart,
    piecesEnd,
  ]);

  return (
    <div ref={containerRef}>
      <canvas ref={canvasRef} />
      <Legend />
    </div>
  );
};

export default PieceGrid;
