// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';

import VideoPlayer from '@/components/video-player';

const { launch } = vi.hoisted(() => ({
  launch: vi.fn().mockRejectedValue(new Error('No compatible player')),
}));

vi.mock('@capacitor/core', () => ({
  Capacitor: { isNativePlatform: () => true },
}));

vi.mock('@capgo/capacitor-intent-launcher', () => ({
  IntentLauncher: { startActivityAsync: launch },
  ActivityAction: { VIEW: 'view' },
}));

it('keeps the internal player open when the Android activity launch is rejected', async () => {
  const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
  const onExit = vi.fn();
  render(<VideoPlayer
    options={{ src: 'http://test-server/movie.mp4' }}
    onExit={onExit} />);

  await waitFor(() => expect(launch).toHaveBeenCalledOnce());
  await waitFor(() => expect(screen.getByRole('button', { name: 'Play or pause' })).toBeInTheDocument());
  expect(onExit).not.toHaveBeenCalled();
  expect(consoleError).toHaveBeenCalledWith(
    'Failed to open URL with IntentLauncher',
    expect.any(Error),
  );
  consoleError.mockRestore();
});
