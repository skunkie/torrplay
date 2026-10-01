// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { LogsViewLayout } from '@/components/logs-view-layout';
import { LogEntry } from '@/lib/types/api';

const entries: LogEntry[] = [
  { time: '2026-09-22T10:00:00Z', level: 'INFO', message: 'server started' },
  { time: '2026-09-22T10:01:00Z', level: 'ERROR', message: 'metadata failed', data: { hash: 'ABC123', error: 'timeout' } },
];

describe('LogsViewLayout', () => {
  it('shows newest entries first and searches structured fields', () => {
    render(<LogsViewLayout entries={entries}
      loading={false}
      onRefresh={vi.fn()}
      onCopyVisible={vi.fn()} />);

    const rows = screen.getAllByText(/server started|metadata failed/);
    expect(rows[0]).toHaveTextContent('metadata failed');
    fireEvent.change(screen.getByRole('textbox', { name: 'Search logs' }), { target: { value: 'abc123' } });
    expect(screen.getByText('metadata failed')).toBeInTheDocument();
    expect(screen.queryByText('server started')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('metadata failed'));
    expect(screen.getByText(/"hash": "ABC123"/)).toBeInTheDocument();
  });

  it('filters by level and copies only visible entries', () => {
    const onCopyVisible = vi.fn();
    render(<LogsViewLayout entries={entries}
      loading={false}
      onRefresh={vi.fn()}
      onCopyVisible={onCopyVisible} />);

    fireEvent.click(screen.getByRole('combobox', { name: 'Log level' }));
    fireEvent.click(screen.getByRole('option', { name: 'ERROR' }));
    expect(screen.queryByText('server started')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Copy visible logs' }));
    expect(onCopyVisible).toHaveBeenCalledWith([entries[1]]);
  });

  it('refreshes and distinguishes errors from empty matches', () => {
    const onRefresh = vi.fn();
    const { rerender } = render(<LogsViewLayout entries={[]}
      loading={false}
      onRefresh={onRefresh}
      onCopyVisible={vi.fn()} />);
    expect(screen.getByText('No matching log entries.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(onRefresh).toHaveBeenCalledOnce();
    rerender(<LogsViewLayout entries={[]}
      error={new Error('offline')}
      loading={false}
      onRefresh={onRefresh}
      onCopyVisible={vi.fn()} />);
    expect(screen.getByText('Could not load logs. Try refreshing.')).toBeInTheDocument();
  });

  it('keeps cached entries visible and copyable when refresh fails', () => {
    const onCopyVisible = vi.fn();
    render(<LogsViewLayout entries={entries}
      error={new Error('offline')}
      loading={false}
      onRefresh={vi.fn()}
      onCopyVisible={onCopyVisible} />);

    expect(screen.getByRole('alert')).toHaveTextContent('Could not refresh logs');
    expect(screen.getByText('metadata failed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Copy visible logs' }));
    expect(onCopyVisible).toHaveBeenCalledWith([entries[1], entries[0]]);
  });
});
