// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import React, { createContext, ReactNode, useCallback, useContext, useEffect, useState } from 'react';

import { login as apiLogin } from '@/lib/api/auth';
import { getSettings, updateSettings as apiUpdateSettings } from '@/lib/api/settings';
import { HttpError } from '@/lib/api-client';
import { Auth, Settings } from '@/lib/types/api';

export type AuthContextType = {
  auth: Auth | null,
  settings: Settings | null,
  isAuthenticated: boolean,
  isLoading: boolean,
  login: (username: string, password: string) => Promise<void>,
  logout: () => void,
  updateSettings: (newSettings: Partial<Settings>) => Promise<void>
};

const AuthContext = createContext<AuthContextType | undefined>(undefined);

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
      setIsOffline(false);
    } catch (error) {
      if (error instanceof HttpError && error.status === 401) {
        setAuth({ enabled: true } as Auth);
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
      const demoSettings = {
        auth: { enabled: false, type: 'basic' },
        enableDlna: false,
        enableDownloader: false,
        fileStoragePath: '',
        friendlyName: 'TorrPlay',
        maxMemory: 512,
      } as Settings;
      setSettings(demoSettings);
      setAuth(demoSettings.auth);
      setIsLoading(false);
    } else {
      fetchSettings();
    }
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
    }

    await fetchSettings();
  };

  const isAuthenticated = !isLoading && (
    isOffline ||
    auth?.enabled === false ||
    (auth?.enabled === true && (!!localStorage.getItem('jwt_token') || !!localStorage.getItem('basic_auth')))
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
