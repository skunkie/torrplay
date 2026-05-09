// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import type { VideoMimeType } from '@vidstack/react';

import { VIDEO_EXTENSIONS } from '@/lib/constants';
import type { TorrentFile } from '@/lib/types/api';

export interface VideoFileData {
  videoFiles: TorrentFile[],
  selectedFile: TorrentFile | null
}

export const getVideoFiles = (files: TorrentFile[]): TorrentFile[] => {
  return files.filter(f => VIDEO_EXTENSIONS.some(ext => f.name.toLowerCase().endsWith(ext)));
};

export const getVideoType = (filename: string): VideoMimeType => {
  if (filename.toLowerCase().endsWith('.mkv')) return 'video/mp4' as VideoMimeType;
  return '' as VideoMimeType;
};

export const getInitialVideoFile = (videoFiles: TorrentFile[]): TorrentFile | null => {
  if (videoFiles.length === 1) {
    return videoFiles[0];
  }
  return null;
};
