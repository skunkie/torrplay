// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import Link from 'next/link';

import { buttonVariants } from '@/components/ui/button';
import type { DemoUpdateScenario, Deployment } from '@/lib/app-update-context';
import { cn } from '@/lib/utils';

import { canonicalDemoSearchParams } from './demo-url';

interface DemoScenarioLink {
  label: string,
  scenario: DemoUpdateScenario,
  deployment: Deployment
}

const scenarios: DemoScenarioLink[] = [
  { label: 'Native update', scenario: 'available', deployment: 'native' },
  { label: 'Docker update', scenario: 'available', deployment: 'container' },
  { label: 'Up to date', scenario: 'up-to-date', deployment: 'native' },
];

function scenarioHref(searchParams: string, option: DemoScenarioLink): string {
  const params = new URLSearchParams(searchParams);
  params.set('update', option.scenario);
  if (option.deployment === 'container') {
    params.set('deployment', 'container');
  } else {
    params.delete('deployment');
  }
  return `/demo?${canonicalDemoSearchParams(params).toString()}`;
}

export function DemoUpdateScenarioLinks({
  scenario,
  deployment,
  searchParams,
}: {
  scenario: DemoUpdateScenario,
  deployment: Deployment,
  searchParams: string
}) {
  return (
    <nav aria-label='Update demo scenarios'
      className='flex flex-wrap items-center gap-1 border-t pt-4'>
      <span className='mr-2 text-xs font-medium text-muted-foreground'>Update scenario</span>
      {scenarios.map(option => {
        const isActive = option.scenario === scenario && option.deployment === deployment;
        return (
          <Link
            key={`${option.scenario}-${option.deployment}`}
            href={scenarioHref(searchParams, option)}
            aria-current={isActive ? 'page' : undefined}
            className={cn(
              buttonVariants({ variant: isActive ? 'secondary' : 'ghost', size: 'sm' }),
              'h-7 px-2 text-xs'
            )}
          >
            {option.label}
          </Link>
        );
      })}
    </nav>
  );
}
