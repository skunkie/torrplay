// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { createContext, ReactNode, useContext, useState } from 'react';

interface LiveUpdatesContextType {
  liveUpdatesPaused: boolean,
  setLiveUpdatesPaused: (paused: boolean) => void
}

const LiveUpdatesContext = createContext<LiveUpdatesContextType | undefined>(undefined);

export function LiveUpdatesProvider({ children }: { children: ReactNode }) {
  const [liveUpdatesPaused, setLiveUpdatesPaused] = useState(false);
  return (
    <LiveUpdatesContext.Provider value={{ liveUpdatesPaused, setLiveUpdatesPaused }}>
      {children}
    </LiveUpdatesContext.Provider>
  );
}

export function useLiveUpdates() {
  const context = useContext(LiveUpdatesContext);
  if (context === undefined) {
    throw new Error('useLiveUpdates must be used within a LiveUpdatesProvider');
  }
  return context;
}
