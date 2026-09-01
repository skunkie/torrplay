// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { describe, expect, it } from 'vitest';

import {
  formatSubtitleLabel,
  getInitialVideoFile,
  getSubtitleFiles,
  getSubtitleFilesForVideo,
  getSubtitleTracksForVideo,
  getSubtitleType,
  getVideoFiles,
  getVideoType,
} from '../video-utils';

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

    it('defaults to video/mp4 for mkv, mp4, and unknown/undefined formats', () => {
      expect(getVideoType('movie.mkv')).toBe('video/mp4');
      expect(getVideoType('movie.mp4')).toBe('video/mp4');
      expect(getVideoType('movie.m4v')).toBe('video/mp4');
      expect(getVideoType('movie.mov')).toBe('video/mp4');
      expect(getVideoType(undefined)).toBe('video/mp4');
      expect(getVideoType('')).toBe('video/mp4');
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

  describe('getSubtitleFiles', () => {
    it('filters out non-subtitle files', () => {
      const files = [
        { name: 'movie.mkv', path: 'movie.mkv', length: 1000 },
        { name: 'movie.srt', path: 'movie.srt', length: 50 },
        { name: 'movie.vtt', path: 'movie.vtt', length: 40 },
        { name: 'movie.ass', path: 'movie.ass', length: 60 },
        { name: 'movie.ssa', path: 'movie.ssa', length: 55 },
        { name: 'movie.sub', path: 'movie.sub', length: 70 },
        { name: 'readme.txt', path: 'readme.txt', length: 10 },
      ];
      const result = getSubtitleFiles(files);
      expect(result).toHaveLength(4);
      expect(result.map(f => f.name)).toEqual(['movie.srt', 'movie.vtt', 'movie.ass', 'movie.ssa']);
    });
  });

  describe('getSubtitleType', () => {
    it('detects subtitle format correctly', () => {
      expect(getSubtitleType('movie.srt')).toBe('srt');
      expect(getSubtitleType('movie.SRT')).toBe('srt');
      expect(getSubtitleType('movie.vtt')).toBe('vtt');
      expect(getSubtitleType('movie.ass')).toBe('ass');
      expect(getSubtitleType('movie.ssa')).toBe('ssa');
      expect(getSubtitleType('movie.mp4')).toBeUndefined();
      expect(getSubtitleType(undefined)).toBeUndefined();
    });
  });

  describe('formatSubtitleLabel', () => {
    it('formats subtitle label with recognized language and format', () => {
      expect(formatSubtitleLabel('movie.en.srt')).toBe('English (SRT)');
      expect(formatSubtitleLabel('movie.eng.vtt')).toBe('English (VTT)');
      expect(formatSubtitleLabel('movie_spa.srt')).toBe('Spanish (SRT)');
      expect(formatSubtitleLabel('movie.forced.en.srt')).toBe('English [FORCED] (SRT)');
      expect(formatSubtitleLabel('subtitles.srt')).toBe('subtitles (SRT)');
    });

    it('prefers a language suffix over ISO-like words in the title', () => {
      expect(formatSubtitleLabel('War.en.srt')).toBe('English (SRT)');
      expect(formatSubtitleLabel('The.War.forced.fr.srt')).toBe('French [FORCED] (SRT)');
      expect(formatSubtitleLabel('War.srt')).toBe('War (SRT)');
      expect(formatSubtitleLabel('English.Patient.srt')).toBe('English.Patient (SRT)');
    });
  });

  describe('getSubtitleTracksForVideo', () => {
    const files = [
      { name: 'Episode.S01E01.mkv', path: 'Episode.S01E01.mkv', length: 1000 },
      { name: 'Episode.S01E01.en.srt', path: 'Episode.S01E01.en.srt', length: 50 },
      { name: 'Episode.S01E01.es.srt', path: 'Episode.S01E01.es.srt', length: 50 },
      { name: 'Episode.S01E02.mkv', path: 'Episode.S01E02.mkv', length: 1000 },
      { name: 'Episode.S01E02.en.srt', path: 'Episode.S01E02.en.srt', length: 50 },
    ];

    it('matches subtitles for a specific episode in multi-video torrents', () => {
      const video = { name: 'Episode.S01E01.mkv', path: 'Episode.S01E01.mkv', length: 1000 };
      const tracks = getSubtitleTracksForVideo(video, files, 'fakehash');
      expect(tracks).toHaveLength(2);
      expect(tracks[0].src).toContain('/api/v1/stream/fakehash?path=Episode.S01E01.en.srt');
      expect(tracks[0].label).toBe('English (SRT)');
      expect(tracks[1].label).toBe('Spanish (SRT)');
    });

    it('returns all subtitles when only one video file exists', () => {
      const singleVideoFiles = [
        { name: 'Movie.mkv', path: 'Movie.mkv', length: 1000 },
        { name: 'English.srt', path: 'subs/English.srt', length: 50 },
        { name: 'Spanish.vtt', path: 'subs/Spanish.vtt', length: 45 },
      ];
      const video = singleVideoFiles[0];
      const tracks = getSubtitleTracksForVideo(video, singleVideoFiles, 'fakehash');
      expect(tracks).toHaveLength(2);
      expect(tracks.map(t => t.label)).toEqual(['English (SRT)', 'Spanish (VTT)']);
    });

    it('does not confuse video names that share a prefix', () => {
      const prefixFiles = [
        { name: 'Episode 1.mkv', path: 'Episode 1.mkv', length: 1000 },
        { name: 'Episode 10.mkv', path: 'Episode 10.mkv', length: 1000 },
        { name: 'Episode 1.en.srt', path: 'Episode 1.en.srt', length: 50 },
        { name: 'Episode 10.en.srt', path: 'Episode 10.en.srt', length: 50 },
      ];

      expect(getSubtitleTracksForVideo(prefixFiles[0], prefixFiles, 'fakehash').map(track => track.id))
        .toEqual(['Episode 1.en.srt']);
      expect(getSubtitleTracksForVideo(prefixFiles[1], prefixFiles, 'fakehash').map(track => track.id))
        .toEqual(['Episode 10.en.srt']);
    });

    it('uses directories to distinguish videos with identical names', () => {
      const directoryFiles = [
        { name: 'Episode.mkv', path: 'Season 1/Episode.mkv', length: 1000 },
        { name: 'Episode.en.srt', path: 'Season 1/Subs/Episode.en.srt', length: 50 },
        { name: 'Episode.mkv', path: 'Season 2/Episode.mkv', length: 1000 },
        { name: 'Episode.en.srt', path: 'Season 2/Subs/Episode.en.srt', length: 50 },
      ];

      expect(getSubtitleTracksForVideo(directoryFiles[0], directoryFiles, 'fakehash').map(track => track.id))
        .toEqual(['Season 1/Subs/Episode.en.srt']);
      expect(getSubtitleTracksForVideo(directoryFiles[2], directoryFiles, 'fakehash').map(track => track.id))
        .toEqual(['Season 2/Subs/Episode.en.srt']);
    });

    it('exposes the same matched files for demo adapters', () => {
      expect(getSubtitleFilesForVideo(files[0], files).map(file => file.path))
        .toEqual(['Episode.S01E01.en.srt', 'Episode.S01E01.es.srt']);
    });

    it('returns empty array when video or files is null/empty', () => {
      expect(getSubtitleTracksForVideo(null, files, 'fakehash')).toEqual([]);
      expect(getSubtitleTracksForVideo({ name: 'm.mkv', path: 'm.mkv', length: 100 }, [], 'fakehash')).toEqual([]);
    });
  });
});
