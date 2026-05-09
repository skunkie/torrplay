// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { ArrowDown, ArrowUp } from 'lucide-react';
import { useEffect, useState } from 'react';

import { Button } from './button';

export function ScrollButtons() {
  const [showBackToTop, setShowBackToTop] = useState(false);
  const [showBackToBottom, setShowBackToBottom] = useState(false);

  useEffect(() => {
    const handleVisibility = () => {
      const isScrollable = document.body.scrollHeight > window.innerHeight + 1;
      setShowBackToTop(isScrollable && window.pageYOffset > 300);
      const isAtBottom = Math.ceil(window.innerHeight + window.pageYOffset) >= document.body.offsetHeight;
      setShowBackToBottom(isScrollable && !isAtBottom);
    };

    window.addEventListener('scroll', handleVisibility);
    window.addEventListener('resize', handleVisibility);
    handleVisibility();

    return () => {
      window.removeEventListener('scroll', handleVisibility);
      window.removeEventListener('resize', handleVisibility);
    };
  }, []);

  const scrollToTop = () => {
    window.scrollTo({
      top: 0,
      behavior: 'smooth',
    });
  };

  const scrollToBottom = () => {
    window.scrollTo({
      top: document.body.scrollHeight,
      behavior: 'smooth',
    });
  };

  if (!showBackToTop && !showBackToBottom) {
    return null;
  }

  return (
    <div className='fixed bottom-4 right-4 z-50 flex flex-col gap-2'>
      {showBackToTop && (
        <Button
          variant='outline'
          onClick={scrollToTop}
          className='p-2 rounded-full h-12 w-12'
        >
          <ArrowUp className='h-6 w-6' />
        </Button>
      )}
      {showBackToBottom && (
        <Button
          variant='outline'
          onClick={scrollToBottom}
          className='p-2 rounded-full h-12 w-12'
        >
          <ArrowDown className='h-6 w-6' />
        </Button>
      )}
    </div>
  );
}
