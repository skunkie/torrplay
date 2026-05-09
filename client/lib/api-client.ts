// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { Capacitor } from '@capacitor/core';
import camelcaseKeys from 'camelcase-keys';
import snakecaseKeys from 'snakecase-keys';

// -- Custom Error --

export class HttpError extends Error {
  status: number;
  statusText: string;

  constructor(status: number, statusText: string, message?: string) {
    super(message || `Request failed with status ${status}: ${statusText}`);
    this.name = 'HttpError';
    this.status = status;
    this.statusText = statusText;
  }
}

// -- API Client --

const getDefaultApiUrl = (): string => {
  if (typeof window === 'undefined') {
    return 'http://localhost:8090';
  }

  if (window.location.origin === 'http://tauri.localhost' || window.location.origin === 'tauri://localhost') {
    return 'http://localhost:8090';
  }

  if (Capacitor.isNativePlatform()) {
    return 'http://localhost:8090';
  }

  return window.location.origin;
};

export function getApiBaseUrl(): string {
  if (typeof window !== 'undefined') {
    // Highest priority: manual override from local storage.
    const fromStorage = localStorage.getItem('NEXT_PUBLIC_API_URL');
    if (fromStorage) {
      return fromStorage;
    }
  }

  // Second priority: build-time environment variable.
  if (process.env.NEXT_PUBLIC_API_URL) {
    return process.env.NEXT_PUBLIC_API_URL;
  }

  // Fallback: platform-specific dynamic URL.
  return getDefaultApiUrl();
}

export async function apiFetch(path: string, options: RequestInit = {}): Promise<Response> {
  const baseUrl = getApiBaseUrl();

  if (!baseUrl) {
    throw new Error('API URL is not configured. Please set it in the application settings.');
  }

  const url = `${baseUrl}${path.startsWith('/') ? path : '/' + path}`;

  const requestHeaders = new Headers(options.headers);

  if (typeof window !== 'undefined') {
    const token = localStorage.getItem('jwt_token');
    const basicAuth = localStorage.getItem('basic_auth');

    if (token) {
      requestHeaders.set('Authorization', `Bearer ${token}`);
    } else if (basicAuth) {
      requestHeaders.set('Authorization', `Basic ${basicAuth}`);
    }
  }

  const newOptions = {
    ...options,
    headers: requestHeaders,
  };

  try {
    const response = await fetch(url, newOptions);

    if (!response.ok) {
      try {
        const errorData = await response.json();
        const camelError = camelcaseKeys(errorData, { deep: true });
        throw new HttpError(response.status, response.statusText, camelError.message || camelError.detail);
      } catch (e) {
        if (e instanceof HttpError) {
          throw e;
        }
        if (e instanceof Error && e.message.startsWith('API URL')) {
          throw e;
        }
        throw new HttpError(response.status, response.statusText);
      }
    }
    return response;
  } catch (error) {
    if (error instanceof HttpError) {
      throw error;
    }
    throw new HttpError(0, 'Network Error', 'Could not connect to the server. Please check your network connection and the API URL configuration.');
  }
}

async function handleResponse<T>(response: Response): Promise<T> {
  if (response.status === 204 || response.headers.get('Content-Length') === '0') {
    return undefined as T;
  }
  const data = await response.json();
  return camelcaseKeys(data, { deep: true }) as T;
}

const buildRequestOptions = (method: string, data?: unknown, contentType?: string): RequestInit => {
  const options: RequestInit = { method };
  if (!data) return options;

  if (data instanceof FormData) {
    options.body = data;
  } else if (contentType === 'application/x-www-form-urlencoded') {
    options.headers = { 'Content-Type': contentType };
    const snakeCasedData = snakecaseKeys(data as Record<string, unknown>);
    const searchParams = new URLSearchParams();
    for (const key in snakeCasedData) {
      if (Object.prototype.hasOwnProperty.call(snakeCasedData, key)) {
        const value = snakeCasedData[key];
        if (value !== null && value !== undefined) {
          searchParams.append(key, String(value));
        }
      }
    }
    options.body = searchParams.toString();
  } else {
    options.headers = { 'Content-Type': 'application/json' };
    options.body = JSON.stringify(snakecaseKeys(data as Record<string, unknown>, { deep: true }));
  }
  return options;
};

export const api = {
  get: async <T>(path: string): Promise<T> => {
    const response = await apiFetch(path, { method: 'GET' });
    return handleResponse<T>(response);
  },
  post: async <T>(path: string, data: unknown, contentType?: string): Promise<T> => {
    const options = buildRequestOptions('POST', data, contentType);
    const response = await apiFetch(path, options);
    return handleResponse<T>(response);
  },
  patch: async <T>(path: string, data: unknown): Promise<T> => {
    const options = buildRequestOptions('PATCH', data);
    const response = await apiFetch(path, options);
    return handleResponse<T>(response);
  },
  delete: async <T>(path: string): Promise<T> => {
    const response = await apiFetch(path, { method: 'DELETE' });
    return handleResponse<T>(response);
  },
};
