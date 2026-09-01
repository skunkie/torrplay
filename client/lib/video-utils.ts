// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import type { VideoMimeType } from '@vidstack/react';

import { SUBTITLE_EXTENSIONS, VIDEO_EXTENSIONS } from '@/lib/constants';
import type { TorrentFile } from '@/lib/types/api';

import { getTorrentStreamUrl } from './api/torrents';

export interface SubtitleTrackInfo {
  id: string,
  src: string,
  embeddedTrackNumber?: number,
  unavailableReason?: string,
  label: string,
  language?: string,
  type?: 'vtt' | 'srt' | 'ssa' | 'ass' | 'json',
  kind?: 'subtitles' | 'captions',
  default?: boolean
}

export const getVideoFiles = (files: TorrentFile[]): TorrentFile[] => {
  return files.filter(f => VIDEO_EXTENSIONS.some(ext => f.name.toLowerCase().endsWith(ext)));
};

export const getSubtitleFiles = (files: TorrentFile[]): TorrentFile[] => {
  return files.filter(f => SUBTITLE_EXTENSIONS.some(ext => f.name.toLowerCase().endsWith(ext)));
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

export const getSubtitleType = (filename?: string): 'vtt' | 'srt' | 'ssa' | 'ass' | undefined => {
  if (!filename) return undefined;
  const lower = filename.toLowerCase();
  if (lower.endsWith('.vtt')) return 'vtt';
  if (lower.endsWith('.srt')) return 'srt';
  if (lower.endsWith('.ass')) return 'ass';
  if (lower.endsWith('.ssa')) return 'ssa';
  return undefined;
};

const KNOWN_LANGUAGES: Record<string, string> = {
  ar: 'Arabic',
  ara: 'Arabic',
  arabic: 'Arabic',
  chi: 'Chinese',
  chinese: 'Chinese',
  cs: 'Czech',
  ces: 'Czech',
  cze: 'Czech',
  czech: 'Czech',
  da: 'Danish',
  dan: 'Danish',
  danish: 'Danish',
  de: 'German',
  deu: 'German',
  dutch: 'Dutch',
  dut: 'Dutch',
  el: 'Greek',
  ell: 'Greek',
  en: 'English',
  eng: 'English',
  english: 'English',
  es: 'Spanish',
  fi: 'Finnish',
  fin: 'Finnish',
  finnish: 'Finnish',
  fr: 'French',
  fra: 'French',
  fre: 'French',
  french: 'French',
  ger: 'German',
  german: 'German',
  gre: 'Greek',
  greek: 'Greek',
  he: 'Hebrew',
  heb: 'Hebrew',
  hebrew: 'Hebrew',
  hi: 'Hindi',
  hin: 'Hindi',
  hindi: 'Hindi',
  hu: 'Hungarian',
  hun: 'Hungarian',
  hungarian: 'Hungarian',
  id: 'Indonesian',
  ind: 'Indonesian',
  indonesian: 'Indonesian',
  it: 'Italian',
  ita: 'Italian',
  italian: 'Italian',
  ja: 'Japanese',
  japanese: 'Japanese',
  jpn: 'Japanese',
  ko: 'Korean',
  kor: 'Korean',
  korean: 'Korean',
  nl: 'Dutch',
  nld: 'Dutch',
  no: 'Norwegian',
  nor: 'Norwegian',
  norwegian: 'Norwegian',
  pl: 'Polish',
  pol: 'Polish',
  polish: 'Polish',
  por: 'Portuguese',
  portuguese: 'Portuguese',
  pt: 'Portuguese',
  ro: 'Romanian',
  romanian: 'Romanian',
  ron: 'Romanian',
  ru: 'Russian',
  rum: 'Romanian',
  rus: 'Russian',
  russian: 'Russian',
  spa: 'Spanish',
  spanish: 'Spanish',
  sv: 'Swedish',
  swe: 'Swedish',
  swedish: 'Swedish',
  th: 'Thai',
  tha: 'Thai',
  thai: 'Thai',
  tr: 'Turkish',
  tur: 'Turkish',
  turkish: 'Turkish',
  uk: 'Ukrainian',
  ukr: 'Ukrainian',
  ukrainian: 'Ukrainian',
  vi: 'Vietnamese',
  vie: 'Vietnamese',
  vietnamese: 'Vietnamese',
  zh: 'Chinese',
  zho: 'Chinese',
};

export const formatSubtitleLabel = (filename: string): string => {
  const base = filename.split('/').pop() || filename;
  const extMatch = base.match(/\.([a-z0-9]+)$/i);
  const ext = extMatch ? extMatch[1].toUpperCase() : '';
  const nameWithoutExt = extMatch ? base.slice(0, -extMatch[0].length) : base;

  const tokens = nameWithoutExt.split(/[\._\-\s\[\]\(\)]+/).filter(Boolean);
  const modifiers: string[] = [];
  const isModifier = (token: string) => ['forced', 'sdh', 'cc', 'commentary'].includes(token.toLowerCase());

  for (const token of tokens) {
    if (isModifier(token)) {
      modifiers.push(token.toUpperCase());
    }
  }

  // Subtitle language tags conventionally sit next to the extension (before
  // optional modifiers). Inspect only that token so title words never shadow it.
  const languageToken = [...tokens]
    .reverse()
    .find(token => !isModifier(token));
  const detectedLang = languageToken ? KNOWN_LANGUAGES[languageToken.toLowerCase()] ?? null : null;

  const modifierSuffix = modifiers.length > 0 ? ` [${modifiers.join(', ')}]` : '';
  const extSuffix = ext ? ` (${ext})` : '';

  if (detectedLang) {
    return `${detectedLang}${modifierSuffix}${extSuffix}`;
  }

  return `${nameWithoutExt}${modifierSuffix}${extSuffix}`;
};

const getDirectory = (filePath: string): string => {
  const separator = filePath.lastIndexOf('/');
  return separator === -1 ? '' : filePath.slice(0, separator).toLowerCase();
};

const getFileStem = (filename: string): string => {
  return filename.replace(/\.[^/.]+$/, '').toLowerCase();
};

const subtitleNameMatchesVideo = (subtitleName: string, videoBase: string): boolean => {
  const subtitleBase = getFileStem(subtitleName);
  if (subtitleBase === videoBase) return true;
  const separator = subtitleBase[videoBase.length];
  return subtitleBase.startsWith(videoBase) && ['.', '_', '-', ' ', '[', '('].includes(separator);
};

const isWithinDirectory = (childDirectory: string, parentDirectory: string): boolean => {
  return parentDirectory === '' || childDirectory === parentDirectory || childDirectory.startsWith(`${parentDirectory}/`);
};

const getNamedSubtitleCandidates = (subtitle: TorrentFile, videoFiles: TorrentFile[]): TorrentFile[] => {
  return videoFiles.filter(video => subtitleNameMatchesVideo(subtitle.name, getFileStem(video.name)));
};

const getNamedSubtitleOwner = (subtitle: TorrentFile, candidates: TorrentFile[]): TorrentFile | null => {
  const subtitleDirectory = getDirectory(subtitle.path);
  if (candidates.length === 0) return null;

  const contextualCandidates = candidates.filter(video => isWithinDirectory(subtitleDirectory, getDirectory(video.path)));
  const relevantCandidates = contextualCandidates.length > 0 ? contextualCandidates : candidates;

  const longestBase = Math.max(...relevantCandidates.map(video => getFileStem(video.name).length));
  const mostSpecificCandidates = relevantCandidates.filter(video => getFileStem(video.name).length === longestBase);
  return mostSpecificCandidates.length === 1 ? mostSpecificCandidates[0] : null;
};

const getDirectorySubtitleCandidates = (subtitle: TorrentFile, videoFiles: TorrentFile[]): TorrentFile[] => {
  const subtitleDirectory = getDirectory(subtitle.path);
  const candidates = videoFiles.filter(video => isWithinDirectory(subtitleDirectory, getDirectory(video.path)));
  if (candidates.length === 0) return [];

  const deepestDirectory = Math.max(...candidates.map(video => getDirectory(video.path).length));
  return candidates.filter(video => getDirectory(video.path).length === deepestDirectory);
};

export const getSubtitleFilesForVideo = (
  videoFile: TorrentFile | null,
  files: TorrentFile[]
): TorrentFile[] => {
  if (!videoFile || !files || files.length === 0) return [];

  const subFiles = getSubtitleFiles(files);
  if (subFiles.length === 0) return [];

  const videoFiles = getVideoFiles(files);
  let matchingSubFiles: TorrentFile[] = [];

  if (videoFiles.length <= 1) {
    matchingSubFiles = subFiles;
  } else {
    matchingSubFiles = subFiles.filter(sub => {
      const namedCandidates = getNamedSubtitleCandidates(sub, videoFiles);
      if (namedCandidates.length > 0) {
        return getNamedSubtitleOwner(sub, namedCandidates)?.path === videoFile.path;
      }
      return getDirectorySubtitleCandidates(sub, videoFiles).some(video => video.path === videoFile.path);
    });

    if (matchingSubFiles.length === 0) {
      matchingSubFiles = subFiles.filter(sub => {
        return getNamedSubtitleCandidates(sub, videoFiles).length === 0 &&
          getDirectorySubtitleCandidates(sub, videoFiles).length === 0;
      });
    }
  }

  return matchingSubFiles;
};

export const getSubtitleTracksForVideo = (
  videoFile: TorrentFile | null,
  files: TorrentFile[],
  hash: string
): SubtitleTrackInfo[] => {
  return getSubtitleFilesForVideo(videoFile, files).map(file => ({
    id: file.path,
    src: getTorrentStreamUrl(hash, file.path),
    label: formatSubtitleLabel(file.name),
    type: getSubtitleType(file.name),
    kind: 'subtitles',
  }));
};

export const getInitialVideoFile = (videoFiles: TorrentFile[]): TorrentFile | null => {
  if (videoFiles.length === 1) {
    return videoFiles[0];
  }
  return null;
};
