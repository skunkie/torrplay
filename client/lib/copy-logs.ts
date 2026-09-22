// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { LogEntry } from '@/lib/types/api';

export async function copyLogEntries(entries: LogEntry[]): Promise<void> {
  const text = entries.map(entry => JSON.stringify(entry)).join('\n');
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const activeElement = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.readOnly = true;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);

  try {
    textarea.select();
    if (!document.execCommand?.('copy')) {
      throw new Error('Clipboard is unavailable');
    }
  } finally {
    textarea.remove();
    activeElement?.focus();
  }
}
