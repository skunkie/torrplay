// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React, { useRef } from 'react';
import { describe, expect, it } from 'vitest';

import { useKeyboardNavigation } from '@/hooks/use-keyboard-navigation';

function TestNavigationComponent({ showDialog = false }: { showDialog?: boolean }) {
  const headerRef = useRef<HTMLDivElement>(null);
  const controlsRef = useRef<HTMLDivElement>(null);
  const gridRef = useRef<HTMLDivElement>(null);
  const paginationRef = useRef<HTMLDivElement>(null);

  const sections = [
    { id: 'header', ref: headerRef, selector: 'button, input' },
    { id: 'controls', ref: controlsRef, selector: 'button' },
    { id: 'grid', ref: gridRef, selector: '[data-item]' },
    { id: 'pagination', ref: paginationRef, selector: 'button' },
  ];

  useKeyboardNavigation(sections, () => 2, true);

  return (
    <div>
      <div ref={headerRef}
        data-testid='header-section'>
        <input type='search'
          data-testid='search-input'
          defaultValue='test search' />
        <button data-testid='header-btn-1'>Header 1</button>
      </div>

      <div ref={controlsRef}
        data-testid='controls-section'>
        <button data-testid='add-btn'>Add Torrent</button>
        <button data-testid='sort-btn'>Sort</button>
      </div>

      <div ref={gridRef}
        data-testid='grid-section'>
        <div tabIndex={0}
          data-item
          data-testid='card-0'>Card 0</div>
        <div tabIndex={0}
          data-item
          data-testid='card-1'>Card 1</div>
        <div tabIndex={0}
          data-item
          data-testid='card-2'>Card 2</div>
      </div>

      <div ref={paginationRef}
        data-testid='pagination-section'>
        <button data-testid='prev-btn'>Prev</button>
        <button data-testid='next-btn'>Next</button>
      </div>

      {showDialog && (
        <div role='dialog'
          data-state='open'>
          <button data-testid='dialog-btn'>Dialog Button</button>
        </div>
      )}
    </div>
  );
}

describe('useKeyboardNavigation', () => {
  it('does not intercept Left/Right arrow keys in search input', () => {
    render(<TestNavigationComponent />);

    const searchInput = screen.getByTestId('search-input');
    searchInput.focus();
    expect(searchInput).toHaveFocus();

    const leftEvent = new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true, cancelable: true });
    searchInput.dispatchEvent(leftEvent);
    expect(leftEvent.defaultPrevented).toBe(false);
    expect(searchInput).toHaveFocus();

    const rightEvent = new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true });
    searchInput.dispatchEvent(rightEvent);
    expect(rightEvent.defaultPrevented).toBe(false);
    expect(searchInput).toHaveFocus();
  });

  it('navigates vertically between Header, Controls, Grid, and Pagination', () => {
    render(<TestNavigationComponent />);

    const headerBtn = screen.getByTestId('header-btn-1');
    headerBtn.focus();
    expect(headerBtn).toHaveFocus();

    // Down from Header to Controls
    fireEvent.keyDown(headerBtn, { key: 'ArrowDown' });
    const addBtn = screen.getByTestId('add-btn');
    const sortBtn = screen.getByTestId('sort-btn');
    expect(document.activeElement === addBtn || document.activeElement === sortBtn).toBe(true);

    // Down from Controls to Grid
    const activeControl = document.activeElement!;
    fireEvent.keyDown(activeControl, { key: 'ArrowDown' });
    const card0 = screen.getByTestId('card-0');
    const card1 = screen.getByTestId('card-1');
    expect(document.activeElement === card0 || document.activeElement === card1).toBe(true);

    // Navigate within 2-column grid: from card 0 down to card 2
    card0.focus();
    fireEvent.keyDown(card0, { key: 'ArrowDown' });
    const card2 = screen.getByTestId('card-2');
    expect(card2).toHaveFocus();

    // Down from last row of Grid to Pagination
    fireEvent.keyDown(card2, { key: 'ArrowDown' });
    const prevBtn = screen.getByTestId('prev-btn');
    const nextBtn = screen.getByTestId('next-btn');
    expect(document.activeElement === prevBtn || document.activeElement === nextBtn).toBe(true);

    // Up from Pagination to Grid
    const activePag = document.activeElement!;
    fireEvent.keyDown(activePag, { key: 'ArrowUp' });
    expect(
      document.activeElement === card0 ||
      document.activeElement === card1 ||
      document.activeElement === card2
    ).toBe(true);
  });

  it('navigates horizontally within a section', () => {
    render(<TestNavigationComponent />);

    const addBtn = screen.getByTestId('add-btn');
    const sortBtn = screen.getByTestId('sort-btn');

    addBtn.focus();
    expect(addBtn).toHaveFocus();

    fireEvent.keyDown(addBtn, { key: 'ArrowRight' });
    expect(sortBtn).toHaveFocus();

    fireEvent.keyDown(sortBtn, { key: 'ArrowLeft' });
    expect(addBtn).toHaveFocus();
  });

  it('disables keyboard navigation when a modal dialog is open', () => {
    render(<TestNavigationComponent showDialog={true} />);

    const headerBtn = screen.getByTestId('header-btn-1');
    headerBtn.focus();
    expect(headerBtn).toHaveFocus();

    fireEvent.keyDown(headerBtn, { key: 'ArrowDown' });
    // Focus should remain on header button because dialog is open
    expect(headerBtn).toHaveFocus();
  });

  it('restores focus to trigger element when dialog is closed', async () => {
    const { Dialog, DialogContent } = await import('@/components/ui/dialog');

    function DialogWrapper() {
      const [open, setOpen] = React.useState(false);
      return (
        <div>
          <button
            data-testid='trigger-btn'
            onClick={() => setOpen(true)}
          >
            Open Modal
          </button>
          <Dialog
            open={open}
            onOpenChange={setOpen}
          >
            <DialogContent>
              <button
                data-testid='close-modal-btn'
                onClick={() => setOpen(false)}
              >
                Close
              </button>
            </DialogContent>
          </Dialog>
        </div>
      );
    }

    render(<DialogWrapper />);

    const triggerBtn = screen.getByTestId('trigger-btn');
    triggerBtn.focus();
    expect(triggerBtn).toHaveFocus();

    fireEvent.click(triggerBtn);
    expect(screen.getByTestId('close-modal-btn')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('close-modal-btn'));
    await waitFor(() => {
      expect(triggerBtn).toHaveFocus();
    });
  });

  it('restores focus to distinct trigger elements across sequential dialog openings', async () => {
    const { Dialog, DialogContent } = await import('@/components/ui/dialog');

    function MultiTriggerDialogWrapper() {
      const [activeModal, setActiveModal] = React.useState<string | null>(null);

      return (
        <div>
          <button
            data-testid='trigger-btn-1'
            onClick={e => {
              e.currentTarget.focus();
              setActiveModal('modal-1');
            }}
          >
            Open Modal 1
          </button>
          <button
            data-testid='trigger-btn-2'
            onClick={e => {
              e.currentTarget.focus();
              setActiveModal('modal-2');
            }}
          >
            Open Modal 2
          </button>

          <Dialog
            open={activeModal === 'modal-1'}
            onOpenChange={open => !open && setActiveModal(null)}
          >
            <DialogContent>
              <button
                data-testid='close-modal-1'
                onClick={() => setActiveModal(null)}
              >
                Close 1
              </button>
            </DialogContent>
          </Dialog>

          <Dialog
            open={activeModal === 'modal-2'}
            onOpenChange={open => !open && setActiveModal(null)}
          >
            <DialogContent>
              <button
                data-testid='close-modal-2'
                onClick={() => setActiveModal(null)}
              >
                Close 2
              </button>
            </DialogContent>
          </Dialog>
        </div>
      );
    }

    render(<MultiTriggerDialogWrapper />);

    const triggerBtn1 = screen.getByTestId('trigger-btn-1');
    const triggerBtn2 = screen.getByTestId('trigger-btn-2');

    // Trigger modal 1
    fireEvent.click(triggerBtn1);
    expect(screen.getByTestId('close-modal-1')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('close-modal-1'));
    await waitFor(() => {
      expect(triggerBtn1).toHaveFocus();
    });

    // Trigger modal 2
    triggerBtn2.focus();
    fireEvent.click(triggerBtn2);
    expect(screen.getByTestId('close-modal-2')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('close-modal-2'));
    await waitFor(() => {
      expect(triggerBtn2).toHaveFocus();
    });
  });
});

