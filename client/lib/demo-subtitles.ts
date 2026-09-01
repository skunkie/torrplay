// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import type { TorrentFile } from './types/api';
import {
  formatSubtitleLabel,
  getSubtitleFiles,
  getSubtitleFilesForVideo,
  type SubtitleTrackInfo,
} from './video-utils';

const EXTERNAL_SUBTITLE_FIXTURES = {
  en: '/demo/subtitles/torrplay-demo.en-sdh.vtt',
  es: '/demo/subtitles/torrplay-demo.es.vtt',
} as const;

function getLanguage(filename: string): keyof typeof EXTERNAL_SUBTITLE_FIXTURES {
  return /(?:^|[._ -])es(?:[._ -]|$)/i.test(filename) ? 'es' : 'en';
}

function toTrack(file: TorrentFile, index: number): SubtitleTrackInfo {
  const language = getLanguage(file.name);
  return {
    id: file.path,
    src: EXTERNAL_SUBTITLE_FIXTURES[language],
    label: formatSubtitleLabel(file.name),
    language,
    type: 'vtt',
    kind: 'subtitles',
    default: index === 0,
  };
}

function getFallbackFiles(identifierOrFile?: string | TorrentFile): TorrentFile[] {
  const name = typeof identifierOrFile === 'string'
    ? identifierOrFile
    : identifierOrFile?.name ?? 'TorrPlay Demo';
  const stem = name.replace(/\.[^/.]+$/, '') || 'TorrPlay Demo';
  return [
    { name: `${stem}.en-sdh.vtt`, path: `${stem}.en-sdh.vtt`, length: 200 },
    { name: `${stem}.es.vtt`, path: `${stem}.es.vtt`, length: 272 },
  ];
}

/** Returns actual, synchronized external subtitle fixtures for the demo reel. */
export function getDemoSubtitleTracks(
  identifierOrFile?: string | TorrentFile,
  torrentFiles?: TorrentFile[]
): SubtitleTrackInfo[] {
  if (torrentFiles && torrentFiles.length > 0) {
    const selectedFile = typeof identifierOrFile === 'string' ? null : identifierOrFile ?? null;
    const subtitleFiles = selectedFile
      ? getSubtitleFilesForVideo(selectedFile, torrentFiles)
      : getSubtitleFiles(torrentFiles);
    if (subtitleFiles.length > 0) return subtitleFiles.map(toTrack);
  }

  return getFallbackFiles(identifierOrFile).map(toTrack);
}
