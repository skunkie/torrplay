// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { LogEntry } from '@/lib/types/api';

interface LogsViewLayoutProps {
  entries: LogEntry[],
  error?: Error | null,
  loading: boolean,
  onRefresh: () => void,
  onCopyVisible: (entries: LogEntry[]) => void,
  retainedCount?: number
}

const levels = ['ALL', 'DEBUG', 'INFO', 'WARN', 'ERROR'] as const;
const levelClasses: Record<string, string> = {
  DEBUG: 'border-muted-foreground/30 text-muted-foreground',
  INFO: 'border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300',
  WARN: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300',
  ERROR: 'border-destructive/30 bg-destructive/10 text-destructive',
};

export function LogsViewLayout({ entries, error, loading, onRefresh, onCopyVisible, retainedCount }: LogsViewLayoutProps) {
  const [query, setQuery] = useState('');
  const [level, setLevel] = useState<string>('ALL');
  const search = query.trim().toLowerCase();
  const visible = entries
    .filter(entry => (level === 'ALL' || entry.level.toUpperCase() === level)
      && (!search || `${entry.message} ${JSON.stringify(entry.data ?? {})}`.toLowerCase().includes(search)))
    .sort((a, b) => Date.parse(b.time) - Date.parse(a.time));

  return (
    <div className='flex min-h-0 flex-1 flex-col gap-4'>
      <div className='flex flex-col gap-2 sm:flex-row'>
        <Input aria-label='Search logs'
          placeholder='Search messages or fields…'
          value={query}
          onChange={event => setQuery(event.target.value)} />
        <Select value={level}
          onValueChange={setLevel}>
          <SelectTrigger aria-label='Log level'
            className='w-full sm:w-36'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {levels.map(item => <SelectItem key={item}
              value={item}>{item === 'ALL' ? 'All levels' : item}</SelectItem>)}
          </SelectContent>
        </Select>
        <Button type='button'
          variant='outline'
          onClick={onRefresh}
          disabled={loading}>Refresh</Button>
      </div>

      <p className='text-sm text-muted-foreground'>
        {retainedCount !== undefined ? `Up to ${retainedCount} recent entries kept in memory.` : 'Recent entries kept in memory.'}
        {' '}{visible.length} shown.
      </p>

      {error && entries.length > 0 && (
        <p role='alert'
          className='text-sm text-destructive'>Could not refresh logs. Showing the last loaded entries.</p>
      )}

      <div className='min-h-0 flex-1 overflow-y-auto rounded-md border'
        aria-live='polite'>
        {error && entries.length === 0 ? (
          <p className='p-4 text-sm text-destructive'>Could not load logs. Try refreshing.</p>
        ) : loading && entries.length === 0 ? (
          <p className='p-4 text-sm text-muted-foreground'>Loading logs…</p>
        ) : visible.length === 0 ? (
          <p className='p-4 text-sm text-muted-foreground'>No matching log entries.</p>
        ) : visible.map((entry, index) => (
          <details key={`${entry.time}-${index}`}
            className='border-b px-3 py-2 last:border-b-0'>
            <summary className='flex cursor-pointer list-none items-start gap-2 text-sm [&::-webkit-details-marker]:hidden'>
              <time dateTime={entry.time}
                title={entry.time}
                className='shrink-0 text-muted-foreground'>{new Date(entry.time).toLocaleTimeString()}</time>
              <Badge variant='outline'
                className={`w-16 ${levelClasses[entry.level.toUpperCase()] ?? ''}`}>
                {entry.level.toUpperCase()}
              </Badge>
              <span className='min-w-0 break-words'>{entry.message}</span>
            </summary>
            <div className='mt-2 pl-2 text-xs text-muted-foreground'>
              <time dateTime={entry.time}>{new Date(entry.time).toLocaleString()}</time>
              {entry.data && Object.keys(entry.data).length > 0 && (
                <pre className='mt-2 overflow-x-auto whitespace-pre-wrap break-words'>{JSON.stringify(entry.data, null, 2)}</pre>
              )}
            </div>
          </details>
        ))}
      </div>

      <Button type='button'
        variant='outline'
        className='self-start'
        disabled={visible.length === 0}
        onClick={() => onCopyVisible(visible)}>Copy visible logs</Button>
    </div>
  );
}
