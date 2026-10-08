// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import { getInitialVideoFile, getVideoFiles, getVideoType } from '../video-utils';

describe('video-utils', () => {
  describe('getVideoType', () => {
    it('maps webm extensions to video/webm', () => {
      expect(getVideoType('movie.webm')).toBe('video/webm');
      expect(getVideoType('movie.WEBM')).toBe('video/webm');
    });

    it('maps ogg and ogv extensions to video/ogg', () => {
      expect(getVideoType('video.ogg')).toBe('video/ogg');
      expect(getVideoType('video.ogv')).toBe('video/ogg');
    });

    it('maps avi extensions to video/avi', () => {
      expect(getVideoType('video.avi')).toBe('video/avi');
    });

    it('maps 3gp extensions to video/3gp', () => {
      expect(getVideoType('video.3gp')).toBe('video/3gp');
    });

    it('maps mpeg extensions to video/mpeg', () => {
      expect(getVideoType('video.mpeg')).toBe('video/mpeg');
      expect(getVideoType('video.mpg')).toBe('video/mpeg');
    });

    it('uses MP4 for MP4-family extensions and for containers Vidstack cannot describe on its own', () => {
      expect(getVideoType('movie.mp4')).toBe('video/mp4');
      expect(getVideoType('movie.m4v')).toBe('video/mp4');
      // Vidstack's own provider-selection can't recognize these extensions or infer
      // a matching type from the server's Content-Type, so without a hint it refuses
      // to hand the source to a provider at all. Tagging them as MP4 gets Vidstack to
      // select the native <video> provider, which decodes the real container itself.
      expect(getVideoType('movie.mkv')).toBe('video/mp4');
      expect(getVideoType('movie.flv')).toBe('video/mp4');
      expect(getVideoType('movie.wmv')).toBe('video/mp4');
      expect(getVideoType('movie.vob')).toBe('video/mp4');
    });

    it('leaves genuinely unrecognized filenames untyped so Vidstack can sniff them', () => {
      // '.mov' is in Vidstack's own extension regex, so it resolves a provider
      // without needing an explicit type hint.
      expect(getVideoType('movie.mov')).toBeUndefined();
      expect(getVideoType(undefined)).toBeUndefined();
      expect(getVideoType('')).toBeUndefined();
    });
  });

  describe('getVideoFiles', () => {
    it('filters out non-video files', () => {
      const files = [
        { name: 'movie.mkv', path: 'movie.mkv', length: 1000 },
        { name: 'subtitles.srt', path: 'subtitles.srt', length: 50 },
        { name: 'sample.mp4', path: 'sample.mp4', length: 200 },
        { name: 'readme.txt', path: 'readme.txt', length: 10 },
      ];
      const result = getVideoFiles(files);
      expect(result).toHaveLength(2);
      expect(result.map(f => f.name)).toEqual(['movie.mkv', 'sample.mp4']);
    });
  });

  describe('getInitialVideoFile', () => {
    it('returns the single file when only one video file exists', () => {
      const file = { name: 'single.mkv', path: 'single.mkv', length: 500 };
      expect(getInitialVideoFile([file])).toEqual(file);
    });

    it('returns null when multiple video files exist', () => {
      const files = [
        { name: 'ep1.mkv', path: 'ep1.mkv', length: 500 },
        { name: 'ep2.mkv', path: 'ep2.mkv', length: 500 },
      ];
      expect(getInitialVideoFile(files)).toBeNull();
    });

    it('returns null when no video files exist', () => {
      expect(getInitialVideoFile([])).toBeNull();
    });
  });
});
