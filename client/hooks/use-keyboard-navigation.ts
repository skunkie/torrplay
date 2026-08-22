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
          element.scrollIntoView?.({
            behavior: 'smooth',
            block: 'center'
          });
        });
      }
    };

    const handleKeyDown = (e: KeyboardEvent) => {
      // Do not navigate main page if a modal dialog is open
      if (document.querySelector('[role="dialog"][data-state="open"]')) {
        return;
      }

      const activeElement = document.activeElement as HTMLElement | null;
      if (!activeElement) return;

      const isInput = activeElement.tagName === 'INPUT' || activeElement.tagName === 'TEXTAREA' || activeElement.isContentEditable;
      if (isInput) {
        // Allow user to navigate text inside inputs/textareas and submit forms naturally
        if (e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === 'Enter') {
          return;
        }
      }

      if (activeElement.closest('[data-nav-inside="true"]')) return;

      const isDropdown = activeElement.closest('[role="listbox"]') || activeElement.getAttribute('aria-expanded') === 'true';
      if (isDropdown) return;

      const isSlider = activeElement.getAttribute('role') === 'slider';
      if (isSlider && (e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
        return;
      }

      const isArrowKey = ['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'].includes(e.key);
      if (!isArrowKey) return;

      const isElementVisible = (el: HTMLElement) => {
        if (el.hasAttribute('disabled')) return false;
        if (el.offsetParent !== null) return true;
        if (typeof window !== 'undefined' && window.getComputedStyle) {
          const style = window.getComputedStyle(el);
          return style.display !== 'none' && style.visibility !== 'hidden';
        }
        return true;
      };

      const filteredSections = sections.filter(s => s.ref.current && s.ref.current.querySelector(s.selector));
      const currentSectionIndex = filteredSections.findIndex(s => s.ref.current?.contains(activeElement));

      if (currentSectionIndex === -1) {
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          e.preventDefault();
          const targetSection = e.key === 'ArrowDown' ? filteredSections[0] : filteredSections[filteredSections.length - 1];
          if (targetSection?.ref.current) {
            const allItems = targetSection.ref.current.querySelectorAll<HTMLElement>(targetSection.selector);
            const visibleItems = Array.from(allItems).filter(isElementVisible);
            if (visibleItems.length > 0) focusAndScroll(visibleItems[0]);
          }
        }
        return;
      }

      const currentSection = filteredSections[currentSectionIndex];
      const allItems = currentSection.ref.current!.querySelectorAll<HTMLElement>(currentSection.selector);
      const visibleItems = Array.from(allItems).filter(isElementVisible);
      const currentIndex = visibleItems.indexOf(activeElement);

      if (currentIndex === -1) return;

      const moveSection = (direction: 'up' | 'down') => {
        const newSectionIndex = direction === 'up' ? currentSectionIndex - 1 : currentSectionIndex + 1;
        if (newSectionIndex >= 0 && newSectionIndex < filteredSections.length) {
          const nextSection = filteredSections[newSectionIndex];
          const nextAllItems = nextSection.ref.current!.querySelectorAll<HTMLElement>(nextSection.selector);
          const nextVisibleItems = Array.from(nextAllItems).filter(isElementVisible);
          if (nextVisibleItems.length === 0) return;

          // Pick the item closest horizontally to activeElement center
          const currentRect = activeElement.getBoundingClientRect();
          const currentCenterX = currentRect.left + currentRect.width / 2;
          let bestItem = nextVisibleItems[0];
          let minDistance = Infinity;

          for (const item of nextVisibleItems) {
            const itemRect = item.getBoundingClientRect();
            const itemCenterX = itemRect.left + itemRect.width / 2;
            const distance = Math.abs(itemCenterX - currentCenterX);
            if (distance < minDistance) {
              minDistance = distance;
              bestItem = item;
            }
          }

          if (bestItem) {
            e.preventDefault();
            e.stopPropagation();
            focusAndScroll(bestItem);
          }
        }
      };

      if (currentSection.id === 'grid') {
        e.preventDefault();
        e.stopPropagation();
        const cols = Math.max(1, getGridColumnCount());
        if (e.key === 'ArrowUp') {
          const newIndex = currentIndex - cols;
          if (newIndex >= 0) {
            focusAndScroll(visibleItems[newIndex]);
          } else {
            moveSection('up');
          }
        } else if (e.key === 'ArrowDown') {
          const newIndex = currentIndex + cols;
          if (newIndex < visibleItems.length) {
            focusAndScroll(visibleItems[newIndex]);
          } else {
            const isLastRow = currentIndex >= Math.floor((visibleItems.length - 1) / cols) * cols;
            if (isLastRow) {
              moveSection('down');
            } else {
              focusAndScroll(visibleItems[visibleItems.length - 1]);
            }
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
          moveSection('up');
        } else if (e.key === 'ArrowDown') {
          moveSection('down');
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
