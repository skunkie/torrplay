// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { describe, expect, it, vi } from 'vitest';

import {
  cleanAssDialogueText,
  cuesToWebVtt,
  formatVttTimestamp,
  loadEmbeddedSubtitleTrackVtt,
  parseMatroskaSubtitleCues,
  parseMatroskaSubtitleTracks,
  probeEmbeddedSubtitleTracks,
} from '../mkv-subtitles';
import { concatBuffers, createEbmlElement, createStringElement, createUIntElement, rangeFetch, subtitleCluster } from './fixtures/matroska';

describe('mkv-subtitles', () => {
  describe('formatVttTimestamp', () => {
    it('formats timestamps into HH:MM:SS.mmm format', () => {
      expect(formatVttTimestamp(0)).toBe('00:00:00.000');
      expect(formatVttTimestamp(1.5)).toBe('00:00:01.500');
      expect(formatVttTimestamp(65.123)).toBe('00:01:05.123');
      expect(formatVttTimestamp(3661.045)).toBe('01:01:01.045');
    });
  });

  describe('cleanAssDialogueText', () => {
    it('strips ASS dialogue header fields and styling override tags', () => {
      const assLine = '0,0,Default,,0,0,0,,{\\an8}{\\i1}Hello world{\\i0}\\NSecond line';
      expect(cleanAssDialogueText(assLine)).toBe('Hello world\nSecond line');
    });

    it('returns raw text if not formatted as ASS line', () => {
      expect(cleanAssDialogueText('Simple subtitle text')).toBe('Simple subtitle text');
    });
  });

  describe('cuesToWebVtt', () => {
    it('converts subtitle cue array into valid WebVTT string', () => {
      const cues = [
        { startTime: 1.0, endTime: 4.0, text: 'First line' },
        { startTime: 5.5, endTime: 9.0, text: 'Second line' },
      ];
      const vtt = cuesToWebVtt(cues);
      expect(vtt).toContain('WEBVTT');
      expect(vtt).toContain('00:00:01.000 --> 00:00:04.000');
      expect(vtt).toContain('First line');
      expect(vtt).toContain('00:00:05.500 --> 00:00:09.000');
      expect(vtt).toContain('Second line');
    });
  });

  describe('parseMatroskaSubtitleTracks', () => {
    it('parses subtitle tracks from EBML buffer', () => {
      // Build TrackEntry 1 (Video - TrackType 1)
      const videoTrack = createEbmlElement(
        0xae, // TrackEntry
        concatBuffers(
          createUIntElement(0xd7, 1), // TrackNumber 1
          createUIntElement(0x83, 1), // TrackType 1 (Video)
          createStringElement(0x86, 'V_MPEG4/ISO/AVC')
        )
      );

      // Build TrackEntry 2 (Subtitle - TrackType 17)
      const subTrack1 = createEbmlElement(
        0xae, // TrackEntry
        concatBuffers(
          createUIntElement(0xd7, 2), // TrackNumber 2
          createUIntElement(0x83, 17), // TrackType 17 (Subtitle)
          createStringElement(0x86, 'S_TEXT/UTF8'),
          createStringElement(0x22b59c, 'eng'),
          createStringElement(0x536e, 'Full Subtitles'),
          createUIntElement(0x88, 1) // FlagDefault = 1
        )
      );

      // Build TrackEntry 3 (Subtitle - TrackType 17 ASS)
      const subTrack2 = createEbmlElement(
        0xae, // TrackEntry
        concatBuffers(
          createUIntElement(0xd7, 3), // TrackNumber 3
          createUIntElement(0x83, 17), // TrackType 17 (Subtitle)
          createStringElement(0x86, 'S_TEXT/ASS'),
          createStringElement(0x22b59c, 'spa'),
          createUIntElement(0x55aa, 1) // FlagForced = 1
        )
      );

      const tracksElem = createEbmlElement(
        0x1654ae6b, // Tracks
        concatBuffers(videoTrack, subTrack1, subTrack2)
      );

      const segmentElem = createEbmlElement(
        0x18538067, // Segment
        tracksElem
      );

      const ebmlHeader = createEbmlElement(
        0x1a45dfa3, // EBML
        createStringElement(0x4282, 'matroska')
      );

      const fileBuffer = concatBuffers(ebmlHeader, segmentElem);

      const tracks = parseMatroskaSubtitleTracks(fileBuffer);
      expect(tracks).toHaveLength(2);

      expect(tracks[0].trackNumber).toBe(2);
      expect(tracks[0].codecId).toBe('S_TEXT/UTF8');
      expect(tracks[0].language).toBe('eng');
      expect(tracks[0].label).toContain('English');
      expect(tracks[0].label).toContain('Embedded SRT');
      expect(tracks[0].isDefault).toBe(true);
      expect(tracks[0].isForced).toBe(false);

      expect(tracks[1].trackNumber).toBe(3);
      expect(tracks[1].codecId).toBe('S_TEXT/ASS');
      expect(tracks[1].language).toBe('spa');
      expect(tracks[1].label).toContain('Spanish');
      expect(tracks[1].label).toContain('FORCED');
      expect(tracks[1].label).toContain('Embedded ASS');
      expect(tracks[1].isForced).toBe(true);
    });
  });

  describe('parseMatroskaSubtitleCues', () => {
    it('parses subtitle cues from SimpleBlock and Block elements in Clusters', () => {
      // Build a SimpleBlock for Track 2 at relative timestamp 0ms
      // SimpleBlock header: TrackNumber VINT (e.g. 0x82 for 2), signed int16 time (0x00, 0x00), flags (0x00)
      const trackVint = new Uint8Array([0x82]); // track 2
      const timeAndFlags = new Uint8Array([0x00, 0x00, 0x00]);
      const textData = new TextEncoder().encode('Hello from cluster 1');
      const simpleBlockData = concatBuffers(trackVint, timeAndFlags, textData);
      const simpleBlock = createEbmlElement(0xa3, simpleBlockData);

      // Build Cluster 1 at timestamp 1000ms
      const clusterTimestamp = createUIntElement(0xe7, 1000, 2);
      const cluster1 = createEbmlElement(
        0x1f43b675, // Cluster
        concatBuffers(clusterTimestamp, simpleBlock)
      );

      const segmentElem = createEbmlElement(0x18538067, cluster1);
      const fileBuffer = concatBuffers(
        createEbmlElement(0x1a45dfa3, new Uint8Array([0])),
        segmentElem
      );

      const cues = parseMatroskaSubtitleCues(fileBuffer, 2);
      expect(cues).toHaveLength(1);
      expect(cues[0].startTime).toBe(1.0);
      expect(cues[0].text).toBe('Hello from cluster 1');
    });
  });

  describe('probeEmbeddedSubtitleTracks', () => {
    it('fetches stream header range and returns formatted subtitle tracks', async () => {
      const subTrack = createEbmlElement(
        0xae,
        concatBuffers(
          createUIntElement(0xd7, 2),
          createUIntElement(0x83, 17),
          createStringElement(0x86, 'S_TEXT/UTF8'),
          createStringElement(0x22b59c, 'fre')
        )
      );
      const tracksElem = createEbmlElement(0x1654ae6b, subTrack);
      const segmentElem = createEbmlElement(0x18538067, tracksElem);
      const buffer = concatBuffers(createEbmlElement(0x1a45dfa3, new Uint8Array([0])), segmentElem);

      const mockFetch = rangeFetch(buffer);

      const tracks = await probeEmbeddedSubtitleTracks('http://localhost/stream.mkv', mockFetch as unknown as typeof fetch);
      expect(tracks).toHaveLength(1);
      expect(tracks[0].id).toBe('embedded:2:S_TEXT/UTF8');
      expect(tracks[0].language).toBe('fre');
      expect(tracks[0].label).toContain('French');
      expect(tracks[0].label).toContain('Embedded SRT');
    });

    it('returns empty array when network request fails', async () => {
      const mockFetch = vi.fn().mockRejectedValue(new Error('Network error'));
      const tracks = await probeEmbeddedSubtitleTracks('http://localhost/stream.mkv', mockFetch as unknown as typeof fetch);
      expect(tracks).toEqual([]);
    });

    it('returns empty array when network request is aborted', async () => {
      const abortError = new Error('The operation was aborted');
      abortError.name = 'AbortError';
      const mockFetch = vi.fn().mockRejectedValue(abortError);
      const tracks = await probeEmbeddedSubtitleTracks('http://localhost/stream.mkv', mockFetch as unknown as typeof fetch);
      expect(tracks).toEqual([]);
    });
  });

  describe('loadEmbeddedSubtitleTrackVtt', () => {
    it('demuxes cluster subtitle cues and returns WebVTT data URI', async () => {
      const trackVint = new Uint8Array([0x81]); // track 1
      const timeAndFlags = new Uint8Array([0x00, 0x00, 0x00]);
      const textData = new TextEncoder().encode('Test dialogue line');
      const simpleBlock = createEbmlElement(0xa3, concatBuffers(trackVint, timeAndFlags, textData));
      const cluster = createEbmlElement(0x1f43b675, concatBuffers(createUIntElement(0xe7, 2000, 2), simpleBlock));
      const buffer = concatBuffers(
        createEbmlElement(0x1a45dfa3, new Uint8Array([0])),
        createEbmlElement(0x18538067, cluster)
      );

      const mockFetch = rangeFetch(buffer);

      const dataUri = await loadEmbeddedSubtitleTrackVtt('http://localhost/stream.mkv', 1, mockFetch as unknown as typeof fetch);
      expect(dataUri).toContain('data:text/vtt;charset=utf-8');
      expect(decodeURIComponent(dataUri)).toContain('00:00:02.000');
      expect(decodeURIComponent(dataUri)).toContain('Test dialogue line');
    });

    it('propagates cancellation instead of caching a partial track', async () => {
      const abortError = new Error('The operation was aborted');
      abortError.name = 'AbortError';
      const mockFetch = vi.fn().mockRejectedValue(abortError);
      await expect(loadEmbeddedSubtitleTrackVtt('http://localhost/stream.mkv', 1, mockFetch)).rejects.toThrow('The operation was aborted');
    });
  });
});

describe('subtitle extraction regressions', () => {
  it.each(['S_HDMV/PGS', 'S_VOBSUB', 'S_TEXT/USF'])(
    'marks %s subtitles unavailable instead of offering an empty text track', async codecId => {
      const buffer = createEbmlElement(0x1654ae6b, createEbmlElement(0xae, concatBuffers(
        createUIntElement(0xd7, 1), createUIntElement(0x83, 17), createStringElement(0x86, codecId)
      )));
      const [track] = await probeEmbeddedSubtitleTracks('/movie.mkv', rangeFetch(buffer));
      expect(track.unavailableReason).toContain('not supported');
      expect(track.default).toBe(false);
      await expect(loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, rangeFetch(buffer))).rejects.toThrow('not supported');
    }
  );

  it('rejects encoded text tracks rather than decoding compressed or encrypted bytes as UTF-8', async () => {
    const buffer = createEbmlElement(0x1654ae6b, createEbmlElement(0xae, concatBuffers(
      createUIntElement(0xd7, 1), createUIntElement(0x83, 17), createStringElement(0x86, 'S_TEXT/UTF8'),
      createEbmlElement(0x6d80, createEbmlElement(0x6240, createEbmlElement(0x5034, createUIntElement(0x4254, 0))))
    )));
    const [track] = await probeEmbeddedSubtitleTracks('/movie.mkv', rangeFetch(buffer));
    expect(track.unavailableReason).toContain('Encoded subtitle tracks');
    expect(track.default).toBe(false);
    await expect(loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, rangeFetch(buffer))).rejects.toThrow('Encoded subtitle tracks');
  });

  it('preserves plain text containing commas, braces, and literal backslashes', () => {
    const text = 'one,two,three,four,five,six,seven,eight, {hello} \\N';
    expect(parseMatroskaSubtitleCues(subtitleCluster(1000, text), 1)[0].text).toBe(text);
  });

  it('keeps explicit durations of overlapping subtitles within a cluster', async () => {
    const block = (time: number, text: string) => createEbmlElement(0xa0, concatBuffers(
      createEbmlElement(0xa1, concatBuffers(new Uint8Array([0x81, time >> 8, time & 0xff, 0]), new TextEncoder().encode(text))),
      createUIntElement(0x9b, 3000, 2)
    ));
    const buffer = createEbmlElement(0x1f43b675, concatBuffers(
      createUIntElement(0xe7, 1000, 4), block(0, 'First speaker'), block(1000, 'Second speaker')
    ));
    const onCues = vi.fn();
    await loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, rangeFetch(buffer), undefined, onCues);
    const expected = [
      { startTime: 1, endTime: 4, text: 'First speaker' },
      { startTime: 2, endTime: 5, text: 'Second speaker' },
    ];
    expect(onCues.mock.calls.flatMap(([cues]) => cues)).toEqual(expected);
    expect(parseMatroskaSubtitleCues(buffer, 1)).toEqual(expected);
  });

  it('waits for the timestamp if a subtitle block precedes it', async () => {
    const buffer = createEbmlElement(0x1f43b675, concatBuffers(
      createEbmlElement(0xa3, concatBuffers(new Uint8Array([0x81, 0, 0, 0]), new TextEncoder().encode('Dialogue'))),
      createUIntElement(0xe7, 1000, 4)
    ));
    const onCues = vi.fn();
    await loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, rangeFetch(buffer), undefined, onCues);
    expect(onCues).toHaveBeenCalledExactlyOnceWith([{ startTime: 1, endTime: 4, text: 'Dialogue' }]);
  });

  it('rejects subtitle blocks with no cluster timestamp', async () => {
    const buffer = createEbmlElement(0x1f43b675,
      createEbmlElement(0xa3, concatBuffers(new Uint8Array([0x81, 0, 0, 0]), new TextEncoder().encode('Dialogue')))
    );
    await expect(loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, rangeFetch(buffer))).rejects.toThrow('Missing Matroska cluster timestamp');
  });

  it('delivers a subtitle before waiting for the rest of its cluster to download', async () => {
    const video = createEbmlElement(0xa3, concatBuffers(new Uint8Array([0x82, 0, 0, 0]), new Uint8Array(100000)));
    const buffer = createEbmlElement(0x18538067, concatBuffers(
      createEbmlElement(0x1f43b675, concatBuffers(
        createUIntElement(0xe7, 1000, 4),
        createEbmlElement(0xa0, concatBuffers(
          createEbmlElement(0xa1, concatBuffers(new Uint8Array([0x81, 0, 0, 0]), new TextEncoder().encode('Available now'))),
          createUIntElement(0x9b, 2000, 2)
        )),
        video
      )),
      subtitleCluster(5000, 'Available later')
    ));
    const respond = rangeFetch(buffer);
    let release!: () => void;
    const pendingDownload = new Promise<void>(resolve => { release = resolve; });
    const fetchFn = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      if (new Headers(init?.headers).get('Range') !== 'bytes=0-65535') await pendingDownload;
      return respond(url, init);
    });
    const onCues = vi.fn();
    const extraction = loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, fetchFn, undefined, onCues);
    try {
      await vi.waitFor(() => expect(fetchFn).toHaveBeenCalledTimes(2));
      expect(onCues).toHaveBeenCalledWith([{ startTime: 1, endTime: 3, text: 'Available now' }]);
    } finally {
      release();
      await extraction;
    }
  });

  it.each([false, true])('uses browser fetch without an invalid receiver (injected: %s)', async injected => {
    const tracks = createEbmlElement(0x1654ae6b, createEbmlElement(0xae, concatBuffers(
      createUIntElement(0xd7, 1), createUIntElement(0x83, 17),
      createStringElement(0x86, 'S_TEXT/UTF8')
    )));
    const buffer = createEbmlElement(0x18538067, concatBuffers(
      tracks, subtitleCluster(0, 'First'),
      createEbmlElement(0xec, new Uint8Array(100000)), subtitleCluster(1000, 'Last')
    ));
    const respond = rangeFetch(buffer);
    const browserFetch = vi.spyOn(globalThis, 'fetch').mockImplementation(function (this: unknown, input, init) {
      // Native Window.fetch rejects a class instance as its receiver; arrow mocks hide this.
      if (this !== undefined && this !== window) throw new TypeError('Illegal invocation');
      return respond(input, init);
    });
    try {
      const fetchFn = injected ? fetch : undefined;
      expect(await probeEmbeddedSubtitleTracks('/movie.mkv', fetchFn)).toHaveLength(1);
      const vtt = await loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, fetchFn);
      expect(decodeURIComponent(vtt)).toContain('First');
      expect(decodeURIComponent(vtt)).toContain('Last');
      expect(respond.mock.calls.length).toBeGreaterThan(2);
    } finally {
      browserFetch.mockRestore();
    }
  });

  it('uses the container timestamp scale for cue positions and durations', () => {
    const buffer = createEbmlElement(0x18538067, concatBuffers(
      createEbmlElement(0x1549a966, createUIntElement(0x2ad7b1, 10000000, 4)),
      subtitleCluster(100, 'Scaled cue')
    ));
    expect(parseMatroskaSubtitleCues(buffer, 1)).toEqual([{ startTime: 1, endTime: 3, text: 'Scaled cue' }]);
  });

  it('reads 64-bit track UIDs without bigint literal syntax', () => {
    const buffer = createEbmlElement(0x1654ae6b, createEbmlElement(0xae, concatBuffers(
      createUIntElement(0xd7, 1), createUIntElement(0x83, 17),
      createStringElement(0x86, 'S_TEXT/UTF8'),
      createEbmlElement(0x73c5, new Uint8Array([0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff]))
    )));
    expect(parseMatroskaSubtitleTracks(buffer)[0].uid).toBe(Number.MAX_SAFE_INTEGER);
  });

  it('extracts late cues, skips large video blocks, and delivers progress before EOF', async () => {
    const video = createEbmlElement(0xa3, concatBuffers(new Uint8Array([0x82, 0, 0, 0]), new Uint8Array(6 * 1024 * 1024)));
    const buffer = createEbmlElement(0x18538067, concatBuffers(
      createEbmlElement(0x1549a966, createUIntElement(0x2ad7b1, 10000000, 4)),
      subtitleCluster(100, 'Early cue'),
      createEbmlElement(0x1f43b675, concatBuffers(createUIntElement(0xe7, 1000, 4), video)),
      subtitleCluster(6000, 'Late cue')
    ));
    const fetchFn = rangeFetch(buffer);
    const batches: string[][] = [];
    const result = await loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, fetchFn, undefined, cues => {
      batches.push(cues.map(cue => cue.text));
      if (batches.length === 1) expect(fetchFn.mock.calls).toHaveLength(1);
    });
    expect(decodeURIComponent(result)).toContain('00:01:00.000 --> 00:01:02.000');
    expect(batches).toEqual([['Early cue'], ['Late cue']]);
    expect(fetchFn.mock.calls.length).toBeLessThan(5);
    expect(fetchFn.mock.calls.some(([, init]) => Number(new Headers(init?.headers).get('Range')!.split('=')[1].split('-')[0]) > 5 * 1024 * 1024)).toBe(true);
  });

  it('handles unknown-size segments and clusters and headers spanning ranges', async () => {
    const header = new Uint8Array([0x18, 0x53, 0x80, 0x67, 0xff]);
    const first = subtitleCluster(1000, 'First');
    // Put the next cluster header across the end of the first 64 KiB range.
    const padding = createEbmlElement(0xec, new Uint8Array(65534 - header.length - first.length - 4));
    const second = concatBuffers(new Uint8Array([0x1f, 0x43, 0xb6, 0x75, 0xff]), createUIntElement(0xe7, 2000, 4),
      createEbmlElement(0xa3, concatBuffers(new Uint8Array([0x81, 0, 0, 0]), new TextEncoder().encode('Second'))));
    const result = await loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, rangeFetch(concatBuffers(header, first, padding, second)));
    expect(decodeURIComponent(result)).toContain('First');
    expect(decodeURIComponent(result)).toContain('Second');
  });

  it('rejects servers that ignore Range without reading the movie body', async () => {
    const response = new Response('whole movie', { status: 200 });
    const cancel = vi.spyOn(response.body!, 'cancel');
    const read = vi.spyOn(response, 'arrayBuffer');
    await expect(loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, vi.fn().mockResolvedValue(response))).rejects.toThrow('requires byte ranges');
    expect(cancel).toHaveBeenCalled();
    expect(read).not.toHaveBeenCalled();
  });

  it('does not report success after a later network failure', async () => {
    const buffer = createEbmlElement(0x18538067, concatBuffers(subtitleCluster(0, 'First'),
      createEbmlElement(0xec, new Uint8Array(100000)), subtitleCluster(1000, 'Last')));
    const fetchFn = rangeFetch(buffer);
    const firstResponse = await fetchFn('/movie.mkv', { headers: { Range: 'bytes=0-65535' } });
    const failingFetch = vi.fn().mockResolvedValueOnce(firstResponse).mockRejectedValue(new Error('offline'));
    await expect(loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, failingFetch)).rejects.toThrow('offline');
  });

  it('stops range traversal immediately when cancelled', async () => {
    const controller = new AbortController();
    const fetchFn = rangeFetch(concatBuffers(subtitleCluster(0, 'First'), subtitleCluster(1000, 'Second')));
    await expect(loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, fetchFn, controller.signal, () => controller.abort())).rejects.toMatchObject({ name: 'AbortError' });
    expect(fetchFn).toHaveBeenCalledTimes(1);
  });
});
