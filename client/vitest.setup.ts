// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import '@testing-library/jest-dom';

import { beforeAll, beforeEach, vi } from 'vitest';

vi.mock('@capacitor/core', () => ({
  Capacitor: { isNativePlatform: () => false },
}));

vi.mock('@tauri-apps/api/core', () => ({
  isTauri: () => false,
}));

vi.mock('@capgo/capacitor-intent-launcher', () => ({
  IntentLauncher: {
    startActivityAsync: () => Promise.resolve(),
  },
  ActivityAction: { VIEW: 'view' },
}));

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: () => Promise.resolve(),
}));

if (typeof window !== 'undefined') {
  if (!window.TextTrackCue) {
    // @ts-expect-error polyfill the browser cue base class for jsdom
    window.TextTrackCue = class TextTrackCue extends EventTarget {};
  }
  if (!window.VTTCue) {
    // @ts-expect-error polyfill VTTCue for jsdom environment
    window.VTTCue = class VTTCue extends window.TextTrackCue {
      startTime: number;
      endTime: number;
      text: string;
      constructor(startTime: number, endTime: number, text: string) {
        super();
        this.startTime = startTime;
        this.endTime = endTime;
        this.text = text;
      }
    };
  }
  if (!window.VTTRegion) {
    // @ts-expect-error polyfill VTTRegion for jsdom environment
    window.VTTRegion = class VTTRegion {};
  }
}

beforeAll(() => {
  // Mock IntersectionObserver for test environment
  (global as Record<string, unknown>).IntersectionObserver = class IntersectionObserver {
    constructor() {}
    disconnect() {}
    observe() {}
    takeRecords() {
      return [];
    }
    unobserve() {}
  };

  // Mock ResizeObserver for test environment
  (global as Record<string, unknown>).ResizeObserver = class ResizeObserver {
    constructor() {}
    disconnect() {}
    observe() {}
    unobserve() {}
  };

  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => true,
    }),
  });

  HTMLMediaElement.prototype.pause = () => {
    return Promise.resolve();
  };

  HTMLMediaElement.prototype.play = () => {
    return Promise.resolve();
  };
});

beforeEach(() => {
  localStorage.clear();
});
