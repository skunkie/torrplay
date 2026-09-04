// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { LoginForm } from '@/components/login-form';
import { HttpError } from '@/lib/api-client';

const mockLogin = vi.fn();

vi.mock('@/lib/auth-context', () => ({
  useAuth: () => ({
    login: mockLogin,
  }),
}));

describe('LoginForm', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders responsive container, safe-area classes, and accessible form inputs', () => {
    render(<LoginForm />);

    const container = screen.getByRole('heading', { name: 'TorrPlay' }).closest('div.min-h-dvh');
    expect(container).toBeInTheDocument();
    expect(container).toHaveClass('overflow-y-auto');
    expect(container).toHaveClass('pt-[max(2rem,env(safe-area-inset-top))]');
    expect(container).toHaveClass('pb-[max(2rem,env(safe-area-inset-bottom))]');

    const usernameInput = screen.getByLabelText('Username');
    expect(usernameInput).toHaveAttribute('type', 'text');
    expect(usernameInput).toHaveAttribute('autocomplete', 'username');
    expect(usernameInput).toHaveAttribute('autocapitalize', 'none');
    expect(usernameInput).toHaveAttribute('autocorrect', 'off');

    const passwordInput = screen.getByLabelText('Password');
    expect(passwordInput).toHaveAttribute('type', 'password');
    expect(passwordInput).toHaveAttribute('autocomplete', 'current-password');

    const submitBtn = screen.getByRole('button', { name: 'Login' });
    expect(submitBtn).toBeInTheDocument();
    expect(submitBtn).not.toBeDisabled();
  });

  it('calls login with username and password on submission', async () => {
    mockLogin.mockResolvedValueOnce(undefined);
    render(<LoginForm />);

    const usernameInput = screen.getByLabelText('Username');
    const passwordInput = screen.getByLabelText('Password');
    const submitBtn = screen.getByRole('button', { name: 'Login' });

    fireEvent.change(usernameInput, { target: { value: 'admin' } });
    fireEvent.change(passwordInput, { target: { value: 'secret' } });
    fireEvent.click(submitBtn);

    expect(mockLogin).toHaveBeenCalledWith('admin', 'secret');
  });

  it('displays invalid credentials error message on 401 response', async () => {
    mockLogin.mockRejectedValueOnce(new HttpError(401, 'Unauthorized', 'invalid credentials'));
    render(<LoginForm />);

    const usernameInput = screen.getByLabelText('Username');
    const passwordInput = screen.getByLabelText('Password');
    const submitBtn = screen.getByRole('button', { name: 'Login' });

    fireEvent.change(usernameInput, { target: { value: 'admin' } });
    fireEvent.change(passwordInput, { target: { value: 'wrong' } });
    fireEvent.click(submitBtn);

    await waitFor(() => {
      const errorMsg = screen.getByRole('alert');
      expect(errorMsg).toHaveTextContent('Invalid username or password.');
    });
  });

  it('displays generic error message on other errors', async () => {
    mockLogin.mockRejectedValueOnce(new Error('Network failure'));
    render(<LoginForm />);

    const usernameInput = screen.getByLabelText('Username');
    const passwordInput = screen.getByLabelText('Password');
    const submitBtn = screen.getByRole('button', { name: 'Login' });

    fireEvent.change(usernameInput, { target: { value: 'admin' } });
    fireEvent.change(passwordInput, { target: { value: 'pass' } });
    fireEvent.click(submitBtn);

    await waitFor(() => {
      const errorMsg = screen.getByRole('alert');
      expect(errorMsg).toHaveTextContent('An unknown error occurred. Please try again.');
    });
  });
});
