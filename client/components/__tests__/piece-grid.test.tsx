// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import PieceGrid from '@/components/piece-grid';
import { pieceGridSettings } from '@/lib/piece-grid-settings';
import type { PieceInfo, ReaderInfo } from '@/lib/types/api';

const {
  pieceSize,
  gapBetweenPieces,
  completeColor,
  borderWidth,
  readerColor,
} = pieceGridSettings.default;

const pieceSizeWithGap = pieceSize + gapBetweenPieces;

function makePieces(n: number, completeUpTo: number): PieceInfo[] {
  return Array.from({ length: n }, (_, i) => ({
    index: i,
    size: 1024,
    complete: i < completeUpTo,
    inMemory: false,
  }));
}

function makeReader(reader: number, start: number, end: number): ReaderInfo {
  return { reader, start, end };
}

interface DrawnRect {
  x: number,
  y: number,
  w: number,
  h: number,
  style: string
}

let drawnRects: DrawnRect[];
let drawnStrokes: DrawnRect[];
let mockCtx: Record<string, unknown>;

beforeEach(() => {
  drawnRects = [];
  drawnStrokes = [];

  let currentFillStyle = '';
  let currentStrokeStyle = '';

  mockCtx = {
    clearRect: vi.fn(),
    fillRect: vi.fn((x: number, y: number, w: number, h: number) => {
      drawnRects.push({ x, y, w, h, style: currentFillStyle });
    }),
    strokeRect: vi.fn((x: number, y: number, w: number, h: number) => {
      drawnStrokes.push({ x, y, w, h, style: currentStrokeStyle });
    }),
    beginPath: vi.fn(),
    moveTo: vi.fn(),
    lineTo: vi.fn(),
    closePath: vi.fn(),
    fill: vi.fn(),
    stroke: vi.fn(),
    get fillStyle() { return currentFillStyle; },
    set fillStyle(v: string) { currentFillStyle = v; },
    get strokeStyle() { return currentStrokeStyle; },
    set strokeStyle(v: string) { currentStrokeStyle = v; },
    lineWidth: 0,
  };

  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation((type: string) => {
    if (type === '2d') return mockCtx as unknown as CanvasRenderingContext2D;
    return null;
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  (global as Record<string, unknown>).ResizeObserver = class ResizeObserver {
    constructor() {}
    disconnect() {}
    observe() {}
    unobserve() {}
  };
});

function makeResizeCallback() {
  let savedCb: ((entries: Array<{ contentRect: { width: number, height: number } }>) => void) | null = null;

  (global as Record<string, unknown>).ResizeObserver = class {
    constructor(cb: (entries: Array<{ contentRect: { width: number, height: number } }>) => void) {
      savedCb = cb;
    }
    observe() {}
    disconnect() {}
    unobserve() {}
  };

  return (width: number, height: number) => {
    act(() => {
      savedCb?.([{ contentRect: { width, height } }]);
    });
  };
}

function readerRects() {
  return drawnRects.filter(r => r.style === readerColor);
}

function windowStrokes() {
  return drawnStrokes.filter(r => r.style === readerColor);
}

function completeRects() {
  return drawnRects.filter(r => r.style === completeColor);
}

function incompleteRects() {
  return drawnRects.filter(r => r.style === 'transparent');
}

describe('PieceGrid', () => {
  it('renders the legend with all labels', () => {
    render(
      <PieceGrid
        totalPieces={0}
        pieces={[]}
        readers={[]}
      />,
    );
    expect(screen.getByText('Incomplete')).toBeInTheDocument();
    expect(screen.getByText('Complete')).toBeInTheDocument();
    expect(screen.getByText('Reader')).toBeInTheDocument();
    expect(screen.getByText('Reader Window')).toBeInTheDocument();
  });

  it('renders a canvas element', () => {
    const { container } = render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 5)}
        readers={[]}
      />,
    );
    expect(container.querySelector('canvas')).toBeInTheDocument();
  });

  it('draws pieces with correct fill colors', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={4}
        pieces={makePieces(4, 2)}
        readers={[]}
      />,
    );
    fireResize(100, 200);

    expect(completeRects()).toHaveLength(2);
    expect(incompleteRects()).toHaveLength(2);
  });

  it('draws pieces in correct grid positions', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={6}
        pieces={makePieces(6, 0)}
        readers={[]}
      />,
    );
    fireResize(100, 200);

    expect(drawnRects).toHaveLength(6);

    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);

    expect(drawnRects[0]).toMatchObject({ x: startingX, y: 0, w: pieceSize, h: pieceSize });
    expect(drawnRects[1]).toMatchObject({ x: startingX + pieceSizeWithGap, y: 0 });
    expect(drawnRects[5]).toMatchObject({ x: startingX, y: pieceSizeWithGap });
  });

  it('fills the reader cell with reader color', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 0)}
        readers={[makeReader(0, 0, 0)]}
      />,
    );
    fireResize(100, 200);

    expect(readerRects()).toHaveLength(1);
    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);
    expect(readerRects()[0]).toMatchObject({
      x: startingX,
      y: 0,
      w: pieceSize,
      h: pieceSize,
    });
  });

  it('fills multiple reader cells at their respective positions', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 10)}
        readers={[
          makeReader(0, 0, 1),
          makeReader(9, 9, 10),
        ]}
      />,
    );
    fireResize(100, 200);

    expect(readerRects()).toHaveLength(2);
    const readerRectsList = readerRects();
    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);

    // Reader 1 at piece 0: col=0, row=0
    expect(readerRectsList[0]).toMatchObject({ x: startingX, y: 0 });
    // Reader 2 at piece 9: col=4, row=1
    expect(readerRectsList[1]).toMatchObject({ x: 4 * pieceSizeWithGap + startingX, y: 1 * pieceSizeWithGap });
  });

  it('clamps reader at end of file to last piece', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 0)}
        readers={[makeReader(9, 9, 10)]}
      />,
    );
    fireResize(100, 200);

    expect(readerRects()).toHaveLength(1);
    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);
    // piece 9: col=4, row=1
    expect(readerRects()[0]).toMatchObject({ x: 4 * pieceSizeWithGap + startingX, y: 1 * pieceSizeWithGap });
  });

  it('handles zero end gracefully (no division by zero)', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 0)}
        readers={[makeReader(0, 0, 0)]}
      />,
    );
    fireResize(100, 200);

    // reader at 0, start=0, end=0 => windowStart=0, windowEnd=1 => fills piece 0
    expect(readerRects()).toHaveLength(1);
    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);
    expect(readerRects()[0]).toMatchObject({ x: startingX, y: 0 });
  });

  it('draws no reader cells when readers is empty', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 10)}
        readers={[]}
      />,
    );
    fireResize(100, 200);

    expect(readerRects()).toHaveLength(0);
  });

  it('renders grid with zero width without crashing', () => {
    render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 5)}
        readers={[makeReader(0, 0, 0)]}
      />,
    );

    expect(drawnRects).toHaveLength(0);
  });

  it('does not draw when totalPieces is 0', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={0}
        pieces={[]}
        readers={[]}
      />,
    );
    fireResize(100, 200);

    expect(drawnRects).toHaveLength(0);
  });

  it('re-draws when readers change', () => {
    const fireResize = makeResizeCallback();
    const { rerender } = render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 10)}
        readers={[]}
      />,
    );
    fireResize(100, 200);
    expect(readerRects()).toHaveLength(0);

    drawnRects = [];

    rerender(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 10)}
        readers={[makeReader(2, 2, 3)]}
      />,
    );

    expect(readerRects()).toHaveLength(1);
    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);
    // piece 2: col=2, row=0
    expect(readerRects()[0]).toMatchObject({ x: 2 * pieceSizeWithGap + startingX, y: 0 });
  });

  it('re-draws when pieces change', () => {
    const fireResize = makeResizeCallback();
    const { rerender } = render(
      <PieceGrid
        totalPieces={4}
        pieces={makePieces(4, 0)}
        readers={[]}
      />,
    );
    fireResize(100, 200);
    expect(completeRects()).toHaveLength(0);

    drawnRects = [];

    rerender(
      <PieceGrid
        totalPieces={4}
        pieces={makePieces(4, 4)}
        readers={[]}
      />,
    );

    expect(completeRects()).toHaveLength(4);
  });

  it('calls strokeRect on every piece for borders', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={4}
        pieces={makePieces(4, 0)}
        readers={[]}
      />,
    );
    fireResize(100, 200);

    expect(mockCtx.strokeRect).toHaveBeenCalledTimes(4);
  });

  it('sets lineWidth from settings', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={4}
        pieces={makePieces(4, 0)}
        readers={[]}
      />,
    );
    fireResize(100, 200);

    expect(mockCtx.lineWidth).toBe(borderWidth);
  });

  it('positions reader window pieces correctly across grid rows', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 0)}
        readers={[makeReader(5, 4, 6)]}
      />,
    );
    fireResize(100, 200);

    // window: pieces 4-5; reader at piece 5
    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);

    // window strokes (readerColor outline on both pieces 4 and 5)
    expect(windowStrokes()).toHaveLength(2);
    expect(windowStrokes()[0]).toMatchObject({
      x: 4 * pieceSizeWithGap + startingX,
      y: 0,
      w: pieceSize,
      h: pieceSize,
    });

    // reader position (full opacity fill on piece 5 at col=0, row=1)
    expect(readerRects()).toHaveLength(1);
    expect(readerRects()[0]).toMatchObject({
      x: startingX,
      y: 1 * pieceSizeWithGap,
      w: pieceSize,
      h: pieceSize,
    });
  });

  it('positions reader window at start of file', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 0)}
        readers={[makeReader(0, 0, 2)]}
      />,
    );
    fireResize(100, 200);

    // window: pieces 0-1; reader at piece 0
    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);

    expect(windowStrokes()).toHaveLength(2);
    expect(windowStrokes()[1]).toMatchObject({
      x: startingX + pieceSizeWithGap,
      y: 0,
    });

    expect(readerRects()).toHaveLength(1);
    expect(readerRects()[0]).toMatchObject({
      x: startingX,
      y: 0,
    });
  });

  it('positions reader window at end of file', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 0)}
        readers={[makeReader(9, 8, 10)]}
      />,
    );
    fireResize(100, 200);

    // window: pieces 8-9; reader at piece 9
    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);

    expect(windowStrokes()).toHaveLength(2);
    // piece 8 at col=3, row=1
    expect(windowStrokes()[0]).toMatchObject({
      x: 3 * pieceSizeWithGap + startingX,
      y: 1 * pieceSizeWithGap,
    });

    // piece 9 at col=4, row=1
    expect(readerRects()).toHaveLength(1);
    expect(readerRects()[0]).toMatchObject({
      x: 4 * pieceSizeWithGap + startingX,
      y: 1 * pieceSizeWithGap,
    });
  });

  it('maps every piece index to the same grid position in cells and reader loops', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={10}
        pieces={makePieces(10, 0)}
        readers={[makeReader(3, 2, 4)]}
      />,
    );
    fireResize(100, 200);

    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);

    // Reader window: pieces 2-3; reader at piece 3
    // piece 2 should be at col=2, row=0 → x=2*17+8=42, y=0
    // piece 3 should be at col=3, row=0 → x=3*17+8=59, y=0
    const strokes = windowStrokes();
    const windowStroke = strokes.find(r => r.x === 2 * pieceSizeWithGap + startingX);
    const readerRect = readerRects()[0];

    expect(windowStroke).toBeDefined();
    expect(readerRect).toBeDefined();

    // Window piece (piece 2) at col=2
    expect(windowStroke!.x).toBe(2 * pieceSizeWithGap + startingX);
    expect(windowStroke!.y).toBe(0);

    // Reader piece (piece 3) at col=3
    expect(readerRect!.x).toBe(3 * pieceSizeWithGap + startingX);
    expect(readerRect!.y).toBe(0);

    // Verify these match what the cells loop would produce for the same indices
    const cellX2 = 2 * pieceSizeWithGap + startingX;
    const cellY2 = 0;
    const cellX3 = 3 * pieceSizeWithGap + startingX;
    const cellY3 = 0;
    expect(windowStroke!.x).toBe(cellX2);
    expect(windowStroke!.y).toBe(cellY2);
    expect(readerRect!.x).toBe(cellX3);
    expect(readerRect!.y).toBe(cellY3);
  });

  it('reader window strokes are drawn at the same grid positions as piece cells', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={6}
        pieces={makePieces(6, 6)}
        readers={[makeReader(3, 2, 4)]}
      />,
    );
    fireResize(100, 200);

    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);

    // All reader window strokes should be at the calculated grid position
    const allReaderStrokes = windowStrokes();
    expect(allReaderStrokes.length).toBeGreaterThan(0);

    // Window covers pieces 2 and 3, so we have strokes at both positions
    const piecesInOneRow = Math.floor(100 / pieceSizeWithGap);
    const windowStart = 2;
    const windowEnd = 4;
    const expectedPositions: Array<{x: number, y: number}> = [];
    for (let i = windowStart; i < windowEnd; i++) {
      const col = i % piecesInOneRow;
      const row = Math.floor(i / piecesInOneRow);
      expectedPositions.push({ x: col * pieceSizeWithGap + startingX, y: row * pieceSizeWithGap });
    }

    for (const pos of expectedPositions) {
      const matchingStroke = allReaderStrokes.find(r => r.x === pos.x && r.y === pos.y);
      expect(matchingStroke).toBeDefined();
    }
  });

  it('reader cell overlaps the piece cell at same position', () => {
    const fireResize = makeResizeCallback();
    render(
      <PieceGrid
        totalPieces={4}
        pieces={makePieces(4, 4)}
        readers={[makeReader(0, 0, 2)]}
      />,
    );
    fireResize(100, 200);

    // piece 0 and reader 0 are both at same position
    expect(readerRects()).toHaveLength(1);
    expect(completeRects()).toHaveLength(4);
    const startingX = Math.ceil((100 - pieceSizeWithGap * 5) / 2);
    expect(readerRects()[0]).toMatchObject({ x: startingX, y: 0 });
    // The piece at index 0 is also at the same position
    expect(completeRects()[0]).toMatchObject({ x: startingX, y: 0 });
  });
});
