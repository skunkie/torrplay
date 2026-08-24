// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import type { VideoMimeType } from '@vidstack/react';

import { VIDEO_EXTENSIONS } from '@/lib/constants';
import type { TorrentFile } from '@/lib/types/api';

export const getVideoFiles = (files: TorrentFile[]): TorrentFile[] => {
  return files.filter(f => VIDEO_EXTENSIONS.some(ext => f.name.toLowerCase().endsWith(ext)));
};

export const getVideoType = (filename?: string): VideoMimeType => {
  if (filename) {
    const lower = filename.toLowerCase();
    if (lower.endsWith('.webm')) return 'video/webm';
    if (lower.endsWith('.ogg') || lower.endsWith('.ogv')) return 'video/ogg';
    if (lower.endsWith('.avi')) return 'video/avi';
    if (lower.endsWith('.3gp')) return 'video/3gp';
    if (lower.endsWith('.mpeg') || lower.endsWith('.mpg')) return 'video/mpeg';
  }
  return 'video/mp4';
};

export const getInitialVideoFile = (videoFiles: TorrentFile[]): TorrentFile | null => {
  if (videoFiles.length === 1) {
    return videoFiles[0];
  }
  return null;
};
