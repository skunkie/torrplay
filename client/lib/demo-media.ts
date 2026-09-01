// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { type VideoMimeType } from '@vidstack/react';

interface DemoVideoSource {
  src: string,
  type: VideoMimeType
}

const DEMO_VIDEO_SOURCE: DemoVideoSource = {
  src: '/demo/torrplay-demo.webm',
  type: 'video/webm',
};

export function getDemoVideoSource(): DemoVideoSource {
  return DEMO_VIDEO_SOURCE;
}
