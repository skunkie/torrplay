// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { afterEach, describe, expect, it, vi } from 'vitest';

import { copyLogEntries } from '@/lib/copy-logs';
import { LogEntry } from '@/lib/types/api';

const entries: LogEntry[] = [{ time: '2026-09-22T10:00:00Z', level: 'INFO', message: 'server started' }];
const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
const execCommandDescriptor = Object.getOwnPropertyDescriptor(document, 'execCommand');

afterEach(() => {
  if (clipboardDescriptor) Object.defineProperty(navigator, 'clipboard', clipboardDescriptor);
  else Reflect.deleteProperty(navigator, 'clipboard');
  if (execCommandDescriptor) Object.defineProperty(document, 'execCommand', execCommandDescriptor);
  else Reflect.deleteProperty(document, 'execCommand');
});

describe('copyLogEntries', () => {
  it('uses the modern clipboard API when available', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });

    await copyLogEntries(entries);

    expect(writeText).toHaveBeenCalledWith(JSON.stringify(entries[0]));
  });

  it('copies on HTTP-style origins without navigator.clipboard and restores focus', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined });
    const execCommand = vi.fn().mockReturnValue(true);
    Object.defineProperty(document, 'execCommand', { configurable: true, value: execCommand });
    const button = document.createElement('button');
    document.body.appendChild(button);
    button.focus();

    await copyLogEntries(entries);

    expect(execCommand).toHaveBeenCalledWith('copy');
    expect(document.querySelector('textarea')).toBeNull();
    expect(document.activeElement).toBe(button);
    button.remove();
  });

  it('reports fallback failure without leaving a temporary field', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined });
    Object.defineProperty(document, 'execCommand', { configurable: true, value: vi.fn().mockReturnValue(false) });

    await expect(copyLogEntries(entries)).rejects.toThrow('Clipboard is unavailable');
    expect(document.querySelector('textarea')).toBeNull();
  });
});
