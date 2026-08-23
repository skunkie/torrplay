// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React, { useState } from 'react';
import { describe, expect, it } from 'vitest';

import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../dialog';

describe('Dialog UI component', () => {
  it('renders Dialog with title and description', () => {
    render(
      <Dialog open={true}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Test Title</DialogTitle>
            <DialogDescription>Test Description</DialogDescription>
          </DialogHeader>
          <div>Dialog body content</div>
        </DialogContent>
      </Dialog>
    );

    expect(screen.getByText('Test Title')).toBeInTheDocument();
    expect(screen.getByText('Test Description')).toBeInTheDocument();
    expect(screen.getByText('Dialog body content')).toBeInTheDocument();
  });

  it('captures trigger element and restores focus upon closing', async () => {
    function TestDialog() {
      const [open, setOpen] = useState(false);
      return (
        <div>
          <button data-testid='open-dialog-btn'
            onClick={() => setOpen(true)}>
            Open Dialog
          </button>
          <Dialog open={open}
            onOpenChange={setOpen}>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Modal Dialog</DialogTitle>
                <DialogDescription>Dialog description for accessibility</DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <DialogClose asChild>
                  <button data-testid='close-dialog-btn'>Close Dialog</button>
                </DialogClose>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </div>
      );
    }

    render(<TestDialog />);

    const openBtn = screen.getByTestId('open-dialog-btn');
    openBtn.focus();
    expect(openBtn).toHaveFocus();

    fireEvent.click(openBtn);
    expect(screen.getByText('Modal Dialog')).toBeInTheDocument();

    const closeBtn = screen.getByTestId('close-dialog-btn');
    fireEvent.click(closeBtn);

    await waitFor(() => {
      expect(openBtn).toHaveFocus();
    });
  });

  it('restores focus to active trigger element on close', async () => {
    function FocusRestoreTestDialog() {
      const [open, setOpen] = useState(false);
      return (
        <div>
          <button
            data-testid='trigger-btn'
            onClick={() => setOpen(true)}
          >
            Trigger
          </button>
          <Dialog
            open={open}
            onOpenChange={setOpen}
          >
            <DialogContent>
              <DialogTitle>Dialog Title</DialogTitle>
              <DialogDescription>Dialog Description</DialogDescription>
              <DialogClose asChild>
                <button data-testid='close-btn'>Close</button>
              </DialogClose>
            </DialogContent>
          </Dialog>
        </div>
      );
    }

    render(<FocusRestoreTestDialog />);
    const trigger = screen.getByTestId('trigger-btn');
    trigger.focus();
    expect(trigger).toHaveFocus();

    fireEvent.click(trigger);

    const closeBtn = screen.getByTestId('close-btn');
    fireEvent.click(closeBtn);

    await waitFor(() => {
      expect(trigger).toHaveFocus();
    });
  });

  it('does not add aria-hidden to sibling DOM elements when opened with modal=false default', () => {
    render(
      <div>
        <div data-testid='sibling-node'>Sibling element</div>
        <Dialog open={true}>
          <DialogContent>
            <DialogTitle>Dialog Title</DialogTitle>
            <DialogDescription>Dialog Description</DialogDescription>
          </DialogContent>
        </Dialog>
      </div>
    );

    const sibling = screen.getByTestId('sibling-node');
    expect(sibling).not.toHaveAttribute('aria-hidden');
  });
});
