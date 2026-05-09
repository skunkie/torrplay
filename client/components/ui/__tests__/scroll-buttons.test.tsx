// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import { type ComponentProps } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ScrollButtons } from '../scroll-buttons';

// Mock the Lucide icons so we can easily find them
vi.mock('lucide-react', async importOriginal => {
  const original = await importOriginal<typeof import('lucide-react')>();
  return {
    ...original,
    ArrowUp: (props: ComponentProps<'div'>) => <div data-testid='arrow-up'
      {...props} />,
    ArrowDown: (props: ComponentProps<'div'>) => <div data-testid='arrow-down'
      {...props} />,
  };
});

const mockScrollTo = vi.fn();

describe('ScrollButtons', () => {
  beforeEach(() => {
    // Setup mocks for window's scroll-related properties and methods
    window.scrollTo = mockScrollTo;
    vi.spyOn(window, 'addEventListener');
    vi.spyOn(window, 'removeEventListener');
  });

  afterEach(() => {
    // Clean up mocks
    vi.restoreAllMocks();
    mockScrollTo.mockClear();
  });

  // Helper function to set the scroll-related properties on window and document
  const mockScrollEnvironment = (
    pageYOffset: number,
    scrollHeight: number,
    innerHeight: number
  ) => {
    Object.defineProperty(window, 'pageYOffset', {
      value: pageYOffset,
      writable: true,
    });
    Object.defineProperty(document.body, 'scrollHeight', {
      value: scrollHeight,
      writable: true,
    });
    Object.defineProperty(document.body, 'offsetHeight', {
      value: scrollHeight,
      writable: true,
    });
    Object.defineProperty(window, 'innerHeight', {
      value: innerHeight,
      writable: true,
    });

    // Manually trigger the event handlers after updating values, simulating a scroll or resize
    fireEvent.scroll(window);
  };

  it('should not render when the page is not scrollable', () => {
    const { container } = render(<ScrollButtons />);
    // Simulate a non-scrollable page
    mockScrollEnvironment(0, 800, 800);

    expect(container.firstChild).toBeNull();
  });

  it('should show only the "scroll to bottom" button at the top of a scrollable page', () => {
    render(<ScrollButtons />);
    // Simulate being at the top of a scrollable page
    mockScrollEnvironment(0, 2000, 800);

    expect(screen.queryByTestId('arrow-up')).not.toBeInTheDocument();
    expect(screen.getByTestId('arrow-down')).toBeInTheDocument();
  });

  it('should show both buttons when scrolled down from the top', () => {
    render(<ScrollButtons />);
    // Simulate being scrolled down, but not to the bottom
    mockScrollEnvironment(400, 2000, 800);

    expect(screen.getByTestId('arrow-up')).toBeInTheDocument();
    expect(screen.getByTestId('arrow-down')).toBeInTheDocument();
  });

  it('should show only the "scroll to top" button when at the bottom of a scrollable page', () => {
    render(<ScrollButtons />);
    // Simulate being at the bottom
    mockScrollEnvironment(1200, 2000, 800);

    expect(screen.getByTestId('arrow-up')).toBeInTheDocument();
    expect(screen.queryByTestId('arrow-down')).not.toBeInTheDocument();
  });

  it('should call window.scrollTo with top: 0 when the top button is clicked', () => {
    render(<ScrollButtons />);
    mockScrollEnvironment(400, 2000, 800);

    const backToTopButton = screen.getByTestId('arrow-up').closest('button');
    expect(backToTopButton).not.toBeNull();
    fireEvent.click(backToTopButton!);

    expect(mockScrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' });
  });

  it('should call window.scrollTo with the page height when the bottom button is clicked', () => {
    render(<ScrollButtons />);
    mockScrollEnvironment(400, 2000, 800);

    const backToBottomButton = screen.getByTestId('arrow-down').closest('button');
    expect(backToBottomButton).not.toBeNull();
    fireEvent.click(backToBottomButton!);

    expect(mockScrollTo).toHaveBeenCalledWith({ top: 2000, behavior: 'smooth' });
  });

  it('should attach and clean up event listeners correctly', () => {
    const { unmount } = render(<ScrollButtons />);

    expect(window.addEventListener).toHaveBeenCalledWith('scroll', expect.any(Function));
    expect(window.addEventListener).toHaveBeenCalledWith('resize', expect.any(Function));

    unmount();

    expect(window.removeEventListener).toHaveBeenCalledWith('scroll', expect.any(Function));
    expect(window.removeEventListener).toHaveBeenCalledWith('resize', expect.any(Function));
  });
});
