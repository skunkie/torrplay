// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useState } from 'react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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
    <div className='flex justify-center items-center min-h-screen min-h-dvh px-4 py-8 sm:py-12 overflow-y-auto pt-[max(2rem,env(safe-area-inset-top))] pb-[max(2rem,env(safe-area-inset-bottom))] pl-[max(1rem,env(safe-area-inset-left))] pr-[max(1rem,env(safe-area-inset-right))]'>
      <form
        onSubmit={handleSubmit}
        className='space-y-4 w-full max-w-sm my-auto'
      >
        <h1 className='text-2xl sm:text-3xl font-bold text-center tracking-tight'>TorrPlay</h1>
        <div className='space-y-1.5'>
          <Label htmlFor='username'>Username</Label>
          <Input
            id='username'
            type='text'
            placeholder='Username'
            aria-label='Username'
            autoComplete='username'
            autoCapitalize='none'
            autoCorrect='off'
            spellCheck={false}
            value={username}
            onChange={e => setUsername(e.target.value)}
          />
        </div>
        <div className='space-y-1.5'>
          <Label htmlFor='password'>Password</Label>
          <Input
            id='password'
            type='password'
            placeholder='Password'
            aria-label='Password'
            autoComplete='current-password'
            value={password}
            onChange={e => setPassword(e.target.value)}
          />
        </div>
        <Button
          type='submit'
          className='w-full'
          disabled={isLoading}
        >
          {isLoading ? 'Logging in...' : 'Login'}
        </Button>
        {error && (
          <p
            role='alert'
            className='text-destructive text-sm text-center'
          >
            {error}
          </p>
        )}
      </form>
    </div>
  );
}
