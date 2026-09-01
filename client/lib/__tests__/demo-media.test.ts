// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import { getDemoVideoSource } from '../demo-media';

describe('demo-media', () => {
  it('uses the real multi-track WebM fixture', () => {
    expect(getDemoVideoSource()).toEqual({
      src: '/demo/torrplay-demo.webm',
      type: 'video/webm',
    });
  });
});
