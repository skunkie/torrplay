// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it } from 'vitest';

import { PageContainer } from '../page-container';

describe('PageContainer', () => {
  it('renders children inside main container', () => {
    render(
      <PageContainer>
        <div>Content Inside Container</div>
      </PageContainer>
    );

    expect(screen.getByText('Content Inside Container')).toBeInTheDocument();
    const main = screen.getByRole('main');
    expect(main).toBeInTheDocument();
    expect(main).not.toHaveAttribute('inert');
  });

  it('applies inert attribute when inert is true', () => {
    render(
      <PageContainer inert={true}>
        <div>Content Inside Container</div>
      </PageContainer>
    );

    const main = document.querySelector('main');
    expect(main).toBeInTheDocument();
    expect(main).toHaveAttribute('inert');
  });
});
