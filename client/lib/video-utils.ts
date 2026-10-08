// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import type { VideoMimeType } from '@vidstack/react';

import { VIDEO_EXTENSIONS } from '@/lib/constants';
import type { TorrentFile } from '@/lib/types/api';

export const getVideoFiles = (files: TorrentFile[]): TorrentFile[] => {
  return files.filter(f => VIDEO_EXTENSIONS.some(ext => f.name.toLowerCase().endsWith(ext)));
};

export const getVideoType = (filename?: string): VideoMimeType | undefined => {
  if (filename) {
    const lower = filename.toLowerCase();
    if (lower.endsWith('.mp4') || lower.endsWith('.m4v')) return 'video/mp4';
    if (lower.endsWith('.webm')) return 'video/webm';
    if (lower.endsWith('.ogg') || lower.endsWith('.ogv')) return 'video/ogg';
    if (lower.endsWith('.avi')) return 'video/avi';
    if (lower.endsWith('.3gp')) return 'video/3gp';
    if (lower.endsWith('.mpeg') || lower.endsWith('.mpg')) return 'video/mpeg';
    // Vidstack only recognizes mp4/webm/ogg/avi/3gp/mpeg (and mov/m4v by extension)
    // when picking a provider; without a type hint it refuses to select the video
    // provider at all for these containers, even though the native <video> element
    // can often still decode them. Tag them as MP4 purely to get Vidstack to hand
    // the source to <video> - the real container/codecs are decoded independently
    // of this hint.
    if (lower.endsWith('.mkv') || lower.endsWith('.flv') || lower.endsWith('.wmv') || lower.endsWith('.vob')) {
      return 'video/mp4';
    }
  }
  return undefined;
};

export const getInitialVideoFile = (videoFiles: TorrentFile[]): TorrentFile | null => {
  if (videoFiles.length === 1) {
    return videoFiles[0];
  }
  return null;
};
