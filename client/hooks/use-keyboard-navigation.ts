// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { RefObject, useEffect } from 'react';

interface Section<T extends HTMLElement> {
  id: string,
  ref: RefObject<T | null>,
  selector: string
}

export function useKeyboardNavigation<T extends HTMLElement>(
  sections: Section<T>[],
  getGridColumnCount: () => number,
  usePagination: boolean
) {
  useEffect(() => {
    const gridSection = sections.find(s => s.id === 'grid');

    const focusAndScroll = (element: HTMLElement | null) => {
      if (!element) return;
      element.focus();

      if (gridSection && gridSection.ref.current && gridSection.ref.current.contains(element)) {
        requestAnimationFrame(() => {
          element.scrollIntoView({
            behavior: 'smooth',
            block: 'center'
          });
        });
      }
    };

    const handleKeyDown = (e: KeyboardEvent) => {
      if (document.querySelector('[role="dialog"][data-state="open"]')) {
        return;
      }

      const activeElement = document.activeElement as HTMLElement;
      if (!activeElement || (activeElement.tagName === 'INPUT' && activeElement.getAttribute('type') !== 'search')) return;

      if (activeElement.closest('[data-nav-inside="true"]')) return;

      const isDropdown = activeElement.closest('[role="listbox"]');
      if (isDropdown) return;

      const isSlider = activeElement.getAttribute('role') === 'slider';
      if (isSlider && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
        return;
      }

      if (e.key === 'Enter') {
        e.preventDefault();
        activeElement.click();
        return;
      }

      const isArrowKey = ['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'].includes(e.key);
      if (!isArrowKey) return;

      const filteredSections = sections.filter(s => s.ref.current && s.ref.current.querySelector(s.selector));
      const currentSectionIndex = filteredSections.findIndex(s => s.ref.current?.contains(activeElement));

      if (currentSectionIndex === -1) {
        if (e.key === 'ArrowDown') {
          e.preventDefault();
          const firstSection = filteredSections[0];
          const allItems = firstSection.ref.current?.querySelectorAll<HTMLElement>(firstSection.selector);
          if (allItems) {
            const visibleItems = Array.from(allItems).filter(el => el.offsetParent !== null && !el.hasAttribute('disabled'));
            if (visibleItems.length > 0) focusAndScroll(visibleItems[0]);
          }
        }
        return;
      }

      const currentSection = filteredSections[currentSectionIndex];
      const allItems = currentSection.ref.current!.querySelectorAll<HTMLElement>(currentSection.selector);
      const visibleItems = Array.from(allItems).filter(el => el.offsetParent !== null && !el.hasAttribute('disabled'));
      const currentIndex = visibleItems.indexOf(activeElement);

      if (currentIndex === -1) return;

      const moveSection = (direction: 'up' | 'down') => {
        const newSectionIndex = direction === 'up' ? currentSectionIndex - 1 : currentSectionIndex + 1;
        if (newSectionIndex >= 0 && newSectionIndex < filteredSections.length) {
          const nextSection = filteredSections[newSectionIndex];
          const nextAllItems = nextSection.ref.current!.querySelectorAll<HTMLElement>(nextSection.selector);
          const nextVisibleItems = Array.from(nextAllItems).filter(el => el.offsetParent !== null && !el.hasAttribute('disabled'));
          let targetItem: HTMLElement | null = null;
          if (direction === 'up') {
            targetItem = nextVisibleItems.length > 0 ? nextVisibleItems[nextVisibleItems.length - 1] : null;
          } else {
            targetItem = nextVisibleItems.length > 0 ? nextVisibleItems[0] : null;
          }
          if(targetItem) {
            e.preventDefault();
            e.stopPropagation();
            focusAndScroll(targetItem);
          }
        }
      };

      if (currentSection.id === 'grid') {
        e.preventDefault();
        e.stopPropagation();
        if (e.key === 'ArrowUp') {
          const newIndex = currentIndex - getGridColumnCount();
          if (newIndex >= 0) {
            focusAndScroll(visibleItems[newIndex]);
          } else {
            moveSection('up');
          }
        } else if (e.key === 'ArrowDown') {
          const newIndex = currentIndex + getGridColumnCount();
          if (newIndex < visibleItems.length) {
            focusAndScroll(visibleItems[newIndex]);
          } else {
            moveSection('down');
          }
        } else if (e.key === 'ArrowLeft') {
          if (currentIndex > 0) {
            focusAndScroll(visibleItems[currentIndex - 1]);
          }
        } else if (e.key === 'ArrowRight') {
          if (currentIndex < visibleItems.length - 1) {
            focusAndScroll(visibleItems[currentIndex + 1]);
          }
        }
      } else {
        e.preventDefault();
        e.stopPropagation();
        if (e.key === 'ArrowUp') {
          if (currentIndex > 0) {
            focusAndScroll(visibleItems[currentIndex - 1]);
          } else {
            moveSection('up');
          }
        } else if (e.key === 'ArrowDown') {
          if (currentIndex < visibleItems.length - 1) {
            focusAndScroll(visibleItems[currentIndex + 1]);
          } else {
            moveSection('down');
          }
        } else if (e.key === 'ArrowLeft') {
          if (currentIndex > 0) {
            focusAndScroll(visibleItems[currentIndex - 1]);
          }
        } else if (e.key === 'ArrowRight') {
          if (currentIndex < visibleItems.length - 1) {
            focusAndScroll(visibleItems[currentIndex + 1]);
          }
        }
      }
    };

    document.addEventListener('keydown', handleKeyDown, true);

    return () => {
      document.removeEventListener('keydown', handleKeyDown, true);
    };
  }, [sections, getGridColumnCount, usePagination]);
}
