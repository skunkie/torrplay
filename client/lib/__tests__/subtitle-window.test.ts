// SPDX-FileCopyrightText: 2026 TorrPlay
// SPDX-License-Identifier: MIT

import { describe, expect, it, vi } from 'vitest';

import { loadEmbeddedSubtitleTrackVtt, probeEmbeddedSubtitleTracks, type SubtitleCue, SubtitleSourceCache } from '../mkv-subtitles';
import { indexedSubtitleMovie, rangeFetch } from './fixtures/matroska';

function playbackClock(time = 0) {
  let wake = () => {};
  return {
    currentTime: () => time,
    advance(next: number) { time = next; wake(); },
    waitForTimeChange: vi.fn((signal?: AbortSignal) => new Promise<void>((resolve, reject) => {
      const abort = () => { signal?.removeEventListener('abort', abort); reject(signal?.reason); };
      wake = () => { signal?.removeEventListener('abort', abort); resolve(); };
      signal?.addEventListener('abort', abort, { once: true });
    })),
  };
}

function requestedStarts(fetchFn: ReturnType<typeof rangeFetch>) {
  return fetchFn.mock.calls.map(([, init]) => Number(/bytes=(\d+)-/.exec(new Headers(init?.headers).get('Range')!)![1]));
}

describe('playback-bounded subtitle extraction', () => {
  it('coalesces packet reads and stops requests at the look-ahead boundary until playback advances', async () => {
    const movie = indexedSubtitleMovie();
    const fetchFn = rangeFetch(movie.buffer);
    const cache = new SubtitleSourceCache('/movie.mkv');
    const clock = playbackClock();
    const controller = new AbortController();
    const cues: SubtitleCue[] = [];
    await probeEmbeddedSubtitleTracks('/movie.mkv', fetchFn, undefined, cache);
    const loading = loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, fetchFn, controller.signal, batch => cues.push(...batch), { cache, ...clock });
    await vi.waitFor(() => expect(clock.waitForTimeChange).toHaveBeenCalledTimes(1));
    expect(cues.map(cue => cue.startTime)).toEqual([0, 10, 20, 30]);
    expect(fetchFn.mock.calls.length).toBeLessThanOrEqual(4);
    expect(requestedStarts(fetchFn).every(offset => offset < movie.clusterOffsets[5])).toBe(true);
    const before = fetchFn.mock.calls.length;
    clock.advance(0); // A paused timeupdate must not trigger another range request.
    await vi.waitFor(() => expect(clock.waitForTimeChange).toHaveBeenCalledTimes(2));
    expect(fetchFn).toHaveBeenCalledTimes(before);
    clock.advance(35);
    await vi.waitFor(() => expect(clock.waitForTimeChange).toHaveBeenCalledTimes(3));
    expect(cues.at(-1)?.startTime).toBe(60);
    expect(fetchFn.mock.calls.length - before).toBeLessThanOrEqual(3);
    const stopped = expect(loading).rejects.toMatchObject({ name: 'AbortError' });
    controller.abort();
    await stopped;
  });

  it.each([1000000, 10000000])('seeks via cached video CuePoints with timestamp scale %s', async scale => {
    const movie = indexedSubtitleMovie(scale);
    const fetchFn = rangeFetch(movie.buffer);
    const cache = new SubtitleSourceCache('/movie.mkv');
    const clock = playbackClock(105);
    const cues: SubtitleCue[] = [];
    await loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, fetchFn, undefined, batch => cues.push(...batch), { cache, ...clock });
    expect(cues.map(cue => cue.startTime)).toEqual([70, 80, 90, 100, 110, 120]);
    expect(fetchFn.mock.calls.length).toBeLessThanOrEqual(5);
    expect(requestedStarts(fetchFn).some(offset => offset === movie.cuesOffset)).toBe(true);
    expect(requestedStarts(fetchFn).some(offset => offset >= movie.clusterOffsets[1] && offset < movie.clusterOffsets[7])).toBe(false);
    const before = fetchFn.mock.calls.length;
    clock.advance(115);
    await loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, fetchFn, undefined, undefined, { cache, ...clock });
    const repeatedStarts = requestedStarts(fetchFn).slice(before);
    expect(repeatedStarts).not.toContain(0);
    expect(repeatedStarts).not.toContain(movie.cuesOffset);
    expect(repeatedStarts).toHaveLength(0);
  });

  it('skips old clusters when no seek index is available', async () => {
    const movie = indexedSubtitleMovie(1000000, false);
    const cues: SubtitleCue[] = [];
    const fetchFn = rangeFetch(movie.buffer);
    await loadEmbeddedSubtitleTrackVtt('/movie.mkv', 1, fetchFn, undefined, batch => cues.push(...batch), {
      cache: new SubtitleSourceCache('/movie.mkv'), ...playbackClock(105),
    });
    expect(cues[0].startTime).toBe(80);
    expect(cues.at(-1)?.startTime).toBe(120);
    expect(fetchFn.mock.calls.length).toBeLessThanOrEqual(7);
  });

  it('does not reuse bytes from another stream', async () => {
    const movie = indexedSubtitleMovie();
    const fetchFn = rangeFetch(movie.buffer);
    await expect(loadEmbeddedSubtitleTrackVtt('/other.mkv', 1, fetchFn, undefined, undefined, {
      cache: new SubtitleSourceCache('/movie.mkv'), ...playbackClock(),
    })).rejects.toThrow('different source');
    expect(fetchFn).not.toHaveBeenCalled();
  });
});
