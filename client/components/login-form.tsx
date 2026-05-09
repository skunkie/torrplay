// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { HttpError } from '@/lib/api-client';
import { useAuth } from '@/lib/auth-context';

export function LoginForm() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const { login } = useAuth();
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setIsLoading(true);
    try {
      await login(username, password);
    } catch (err) {
      if (err instanceof HttpError && (err.status === 401 || err.status === 403)) {
        setError('Invalid username or password.');
      } else {
        setError('An unknown error occurred. Please try again.');
      }
    }
    finally {
      setIsLoading(false);
    }
  };

  return (
    <div className='flex justify-center items-center h-screen px-4'>
      <form onSubmit={handleSubmit}
        className='space-y-4 w-full max-w-sm'>
        <h1 className='text-2xl font-bold text-center'>TorrPlay</h1>
        <div className='space-y-2'>
          <Input
            id='username'
            type='text'
            placeholder='Username'
            value={username}
            onChange={e => setUsername(e.target.value)}
          />
        </div>
        <div className='space-y-2'>
          <Input
            id='password'
            type='password'
            placeholder='Password'
            value={password}
            onChange={e => setPassword(e.target.value)}
          />
        </div>
        <Button type='submit'
          className='w-full'
          disabled={isLoading}>
          {isLoading ? 'Logging in...' : 'Login'}
        </Button>
        {error && <p className='text-destructive text-center'>{error}</p>}
      </form>
    </div>
  );
}
