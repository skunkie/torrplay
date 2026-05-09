// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { api } from '@/lib/api-client';
import { TokenResponse } from '@/lib/types/api';

export async function login(username:string, password: string): Promise<TokenResponse> {
  return api.post<TokenResponse>(
    '/oauth/token',
    {
      grantType: 'password',
      username,
      password,
    },
    'application/x-www-form-urlencoded'
  );
}
