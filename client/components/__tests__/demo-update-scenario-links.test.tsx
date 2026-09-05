// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { DemoSystemInfoDialog } from '@/app/demo/demo-system-info-dialog';
import { DemoUpdateScenarioLinks } from '@/app/demo/demo-update-scenario-links';
import { DemoAppUpdateProvider } from '@/lib/app-update-context';

describe('DemoUpdateScenarioLinks', () => {
  it('marks the selected scenario and links to the alternatives', () => {
    render(
      <DemoUpdateScenarioLinks
        scenario='available'
        deployment='native'
        searchParams='sortBy=name&modal=system-info'
      />
    );

    expect(screen.getByRole('link', { name: 'Native update' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('link', { name: 'Docker update' })).toHaveAttribute(
      'href',
      '/demo?modal=system-info&update=available&deployment=container&sortBy=name'
    );
    expect(screen.getByRole('link', { name: 'Up to date' })).toHaveAttribute(
      'href',
      '/demo?modal=system-info&update=up-to-date&sortBy=name'
    );
    expect(screen.queryByRole('link', { name: 'Check error' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Idle' })).not.toBeInTheDocument();
  });

  it('removes the Docker deployment parameter from native scenarios', () => {
    render(
      <DemoUpdateScenarioLinks
        scenario='available'
        deployment='container'
        searchParams='update=available&deployment=container'
      />
    );

    expect(screen.getByRole('link', { name: 'Docker update' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('link', { name: 'Up to date' })).toHaveAttribute(
      'href',
      '/demo?update=up-to-date'
    );
  });

  it('renders the scenario links inside the demo System Information dialog', () => {
    render(
      <DemoAppUpdateProvider scenario='available'>
        <DemoSystemInfoDialog
          open={true}
          onOpenChange={vi.fn()}
          updateScenario='available'
          deployment='native'
          searchParams='modal=system-info'
        />
      </DemoAppUpdateProvider>
    );

    expect(screen.getByRole('heading', { name: 'System Information' })).toBeInTheDocument();
    expect(screen.getByRole('navigation', { name: 'Update demo scenarios' })).toBeInTheDocument();
  });
});
