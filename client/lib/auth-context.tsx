// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { createContext, ReactNode, useCallback, useContext, useEffect, useState } from 'react';

import { login as apiLogin } from '@/lib/api/auth';
import { getSettings, updateSettings as apiUpdateSettings } from '@/lib/api/settings';
import { HttpError, onUnauthorized } from '@/lib/api-client';
import { Auth, Settings } from '@/lib/types/api';

import { demoDefaultSettings } from './demo-settings';

export type AuthContextType = {
  auth: Auth | null,
  settings: Settings | null,
  isAuthenticated: boolean,
  isLoading: boolean,
  login: (username: string, password: string) => Promise<void>,
  logout: () => void,
  updateSettings: (newSettings: Partial<Settings>) => Promise<void>
};

export const AuthContext = createContext<AuthContextType | undefined>(undefined);

function parseAuthType(wwwAuth?: string | null): 'bearer' | 'basic' | undefined {
  if (!wwwAuth) return undefined;
  const trimmed = wwwAuth.trim();
  if (/^bearer/i.test(trimmed)) {
    return 'bearer';
  }
  if (/^basic/i.test(trimmed)) {
    return 'basic';
  }
  return undefined;
}

function useAuthStore(isDemo = false) {
  const [auth, setAuth] = useState<Auth | null>(null);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [isLoading, setIsLoading] = useState(!isDemo);
  const [isOffline, setIsOffline] = useState(false);

  const fetchSettings = useCallback(async () => {
    if (isDemo) return;
    setIsLoading(true);
    try {
      const fetchedSettings = await getSettings();
      if (fetchedSettings.auth?.enabled && !fetchedSettings.auth.type) {
        fetchedSettings.auth.type = 'basic';
      }
      setSettings(fetchedSettings);
      setAuth(fetchedSettings.auth);
      if (fetchedSettings.playbackToken) {
        localStorage.setItem('playback_token', fetchedSettings.playbackToken);
      } else {
        localStorage.removeItem('playback_token');
      }
      setIsOffline(false);
    } catch (error) {
      if (error instanceof HttpError && error.status === 401) {
        const detectedType = parseAuthType(error.wwwAuthenticate);
        localStorage.removeItem('jwt_token');
        localStorage.removeItem('basic_auth');
        localStorage.removeItem('playback_token');
        setAuth({
          enabled: true,
          type: detectedType ?? 'basic',
        } as Auth);
        setSettings(null);
      } else {
        console.error('Failed to fetch settings:', error);
        setAuth(null);
        setIsOffline(true);
      }
    } finally {
      setIsLoading(false);
    }
  }, [isDemo]);

  useEffect(() => {
    if (isDemo) {
      const demoSettings = demoDefaultSettings;
      setSettings(demoSettings);
      setAuth(demoSettings.auth);
      setIsLoading(false);
      return;
    }

    fetchSettings();

    const handleUnauthorized = (error: HttpError) => {
      const detectedType = parseAuthType(error.wwwAuthenticate);

      localStorage.removeItem('jwt_token');
      localStorage.removeItem('basic_auth');
      localStorage.removeItem('playback_token');

      setAuth(prev => ({
        ...(prev || {}),
        enabled: true,
        type: detectedType ?? (prev?.type === 'basic' ? 'bearer' : prev?.type ?? 'bearer'),
      }));
      setSettings(null);
      setIsOffline(false);
    };

    const handleStorageChange = (e: StorageEvent) => {
      if (e.key === 'jwt_token' || e.key === 'basic_auth' || e.key === null) {
        fetchSettings();
      }
    };

    const unsubscribe = onUnauthorized(handleUnauthorized);
    window.addEventListener('storage', handleStorageChange);

    return () => {
      unsubscribe();
      window.removeEventListener('storage', handleStorageChange);
    };
  }, [isDemo, fetchSettings]);

  const login = async (username: string, password: string) => {
    if (isDemo) {
      setAuth({ enabled: false } as Auth);
      return;
    }

    if (!auth) throw new Error('Auth settings not loaded');

    if (auth.type === 'bearer') {
      const { accessToken } = await apiLogin(username, password);
      localStorage.setItem('jwt_token', accessToken);
      localStorage.removeItem('basic_auth');
    } else if (auth.type === 'basic') {
      const credentials = btoa(`${username}:${password}`);
      localStorage.setItem('basic_auth', credentials);
      localStorage.removeItem('jwt_token');
    } else {
      try {
        const { accessToken } = await apiLogin(username, password);
        localStorage.setItem('jwt_token', accessToken);
        localStorage.removeItem('basic_auth');
      } catch (error) {
        console.warn('Bearer login failed, falling back to basic auth:', error);
        const credentials = btoa(`${username}:${password}`);
        localStorage.setItem('basic_auth', credentials);
        localStorage.removeItem('jwt_token');
      }
    }

    try {
      await fetchSettings();
    } catch (error) {
      localStorage.removeItem('jwt_token');
      localStorage.removeItem('basic_auth');
      throw error;
    }
  };

  const logout = () => {
    if (isDemo) {
      setAuth({ enabled: false } as Auth);
      return;
    }
    localStorage.removeItem('jwt_token');
    localStorage.removeItem('basic_auth');
    localStorage.removeItem('playback_token');
    setSettings(null);
    setAuth(prev => (prev?.enabled ? { enabled: true, type: prev.type } : null));
    window.location.reload();
  };

  const updateSettings = async (newSettings: Partial<Settings>) => {
    if (isDemo) {
      const updatedSettings = { ...settings, ...newSettings } as Settings;
      if (newSettings.auth) {
        updatedSettings.auth = { ...settings?.auth, ...newSettings.auth } as Auth;
      }
      setSettings(updatedSettings);
      setAuth(updatedSettings.auth);
      return;
    }

    if (!settings) throw new Error('Settings not loaded');

    const settingsToUpdate: Partial<Settings> = { ...newSettings };
    if (settingsToUpdate.auth?.password === '********') {
      delete settingsToUpdate.auth.password;
    }

    await apiUpdateSettings(settingsToUpdate);

    if (settingsToUpdate.auth) {
      localStorage.removeItem('jwt_token');
      localStorage.removeItem('basic_auth');
      localStorage.removeItem('playback_token');
    }

    fetchSettings();
  };

  const isAuthenticated = !isLoading && (
    isOffline ||
    auth?.enabled === false ||
    (auth?.enabled === true && (
      auth.type === 'bearer'
        ? !!localStorage.getItem('jwt_token')
        : auth.type === 'basic'
          ? !!localStorage.getItem('basic_auth')
          : (!!localStorage.getItem('jwt_token') || !!localStorage.getItem('basic_auth'))
    ))
  );

  return { auth, settings, isAuthenticated, isLoading, login, logout, updateSettings };
}

export const AuthProvider = ({ children }: { children: ReactNode }) => {
  const store = useAuthStore(false);
  return <AuthContext.Provider value={store}>{children}</AuthContext.Provider>;
};

export const DemoAuthProvider = ({ children }: { children: ReactNode }) => {
  const store = useAuthStore(true);
  return <AuthContext.Provider value={store}>{children}</AuthContext.Provider>;
};

export const useAuth = (): AuthContextType => {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
};
