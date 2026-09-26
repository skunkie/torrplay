// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { fireEvent, render, screen } from '@testing-library/react';
import { useEffect, useState } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../select';

function LevelSelect({ loaded, onValueChange }: { loaded?: string, onValueChange?: (value: string) => void }) {
  const [value, setValue] = useState('INFO');

  // Mirrors a dialog that applies fetched settings right after mounting.
  useEffect(() => {
    if (loaded) setValue(loaded);
  }, [loaded]);

  // Radix renders its hidden native select only inside a form.
  return (
    <form>
      <Select value={value}
        onValueChange={next => {
          onValueChange?.(next);
          setValue(next);
        }}>
        <SelectTrigger aria-label='Level'>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value='DEBUG'>DEBUG</SelectItem>
          <SelectItem value='INFO'>INFO</SelectItem>
        </SelectContent>
      </Select>
    </form>
  );
}

describe('Select', () => {
  it('keeps a value applied right after mounting', () => {
    render(<LevelSelect loaded='DEBUG' />);

    expect(screen.getByRole('combobox', { name: 'Level' })).toHaveTextContent('DEBUG');
  });

  it('ignores an empty value from the native select', () => {
    const onValueChange = vi.fn();
    const { container } = render(<LevelSelect onValueChange={onValueChange} />);
    const nativeSelect = container.querySelector('select')!;

    fireEvent.change(nativeSelect, { target: { value: '' } });
    expect(onValueChange).not.toHaveBeenCalled();
    expect(screen.getByRole('combobox', { name: 'Level' })).toHaveTextContent('INFO');
  });

  it('forwards a selected value from the native select', () => {
    const onValueChange = vi.fn();
    const { container } = render(<LevelSelect onValueChange={onValueChange} />);
    const nativeSelect = container.querySelector('select')!;

    fireEvent.change(nativeSelect, { target: { value: 'DEBUG' } });
    expect(onValueChange).toHaveBeenCalledWith('DEBUG');
    expect(screen.getByRole('combobox', { name: 'Level' })).toHaveTextContent('DEBUG');
  });
});
