// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type MediaPlayerInstance, TimeRange, type useMediaContext } from '@vidstack/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { concatBuffers, createEbmlElement, createStringElement, createUIntElement, indexedSubtitleMovie, rangeFetch, subtitleCluster } from '@/lib/__tests__/fixtures/matroska';
import { loadEmbeddedSubtitleTrackVtt, probeEmbeddedSubtitleTracks, type SubtitleCue } from '@/lib/mkv-subtitles';
import { type SubtitleTrackInfo } from '@/lib/video-utils';

import DemoVideoPlayer from '../demo-video-player';
import VideoPlayer from '../video-player';

const captured = vi.hoisted(() => ({
  player: null as MediaPlayerInstance | null,
  media: null as ReturnType<typeof useMediaContext> | null,
}));
vi.mock('@vidstack/react', async importOriginal => {
  const actual = await importOriginal<typeof import('@vidstack/react')>();
  const React = await import('react');
  function CaptureContext() {
    captured.media = actual.useMediaContext();
    return null;
  }
  return {
    ...actual,
    MediaPlayer: React.forwardRef<MediaPlayerInstance, React.ComponentProps<typeof actual.MediaPlayer>>(function CapturedMediaPlayer(props, ref) {
      return React.createElement(actual.MediaPlayer, {
        ...props,
        ref: (player: MediaPlayerInstance | null) => {
          captured.player = player;
          if (typeof ref === 'function') ref(player);
          else if (ref) ref.current = player;
        },
      }, React.createElement(CaptureContext), props.children);
    }),
  };
});
vi.mock('@/lib/mkv-subtitles', async importOriginal => {
  const actual = await importOriginal<typeof import('@/lib/mkv-subtitles')>();
  return { ...actual, probeEmbeddedSubtitleTracks: vi.fn(), loadEmbeddedSubtitleTrackVtt: vi.fn() };
});

const vtt = 'data:text/vtt,WEBVTT%0A%0A00:00:00.000%20--%3E%2000:00:02.000%0AHello%0A';
const english: SubtitleTrackInfo = { id: 'english', src: vtt, type: 'vtt', label: 'English', kind: 'subtitles' };
const spanish: SubtitleTrackInfo = { ...english, id: 'spanish', src: vtt + '%0A', label: 'Spanish' };
const embedded: SubtitleTrackInfo = { ...english, id: 'embedded:2:S_TEXT/UTF8', src: '', embeddedTrackNumber: 2, default: true };
const src = { src: '/movie.mp4', type: 'video/mp4' as const };
const mkv = { src: '/movie.mkv', type: 'video/mp4' as const };
const cue = { startTime: 1, endTime: 3, text: 'Embedded dialogue' };

async function select(label: string) {
  await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));
  await userEvent.click(screen.getByRole('menuitemradio', { name: new RegExp(label) }));
}

// jsdom does not implement native text tracks or video playback. Keep Vidstack's
// actual HTMLVideo provider and renderer, supplying only the missing DOM APIs.
function enableNativeVideo() {
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('probably');
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {});
  vi.spyOn(HTMLMediaElement.prototype, 'duration', 'get').mockReturnValue(60);
  vi.spyOn(HTMLMediaElement.prototype, 'seekable', 'get').mockReturnValue(new TimeRange(0, 60));
  const createElement = document.createElement.bind(document);
  vi.spyOn(document, 'createElement').mockImplementation((tag, options) => {
    const el = createElement(tag, options);
    if (tag === 'track') {
      const cues: VTTCue[] = [];
      let mode = 'disabled';
      Object.defineProperty(el, 'track', { value: {
        get cues() { return mode === 'disabled' ? null : cues; },
        get mode() { return mode; },
        set mode(value: string) {
          if (mode === value) return;
          mode = value;
          queueMicrotask(() => {
            const list = el.closest('video')?.textTracks;
            list?.onchange?.call(list, new Event('change'));
          });
        },
        addCue(cue: VTTCue) { if (!cues.includes(cue)) cues.push(cue); },
        removeCue(cue: VTTCue) {
          const index = cues.indexOf(cue);
          if (index < 0) throw new DOMException('Cue is not in this track', 'NotFoundError');
          cues.splice(index, 1);
        },
      } });
    }
    return el;
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('fetch', vi.fn(async () => new Response('WEBVTT\n\n')));
  vi.mocked(probeEmbeddedSubtitleTracks).mockResolvedValue([]);
  vi.mocked(loadEmbeddedSubtitleTrackVtt).mockImplementation(async (_url, _number, _fetch, _signal, onCues) => {
    onCues?.([cue]);
    return vtt;
  });
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

for (const [name, Player] of [['live', VideoPlayer], ['demo', DemoVideoPlayer]] as const) {
  describe(`${name} subtitle playback`, () => {
    it('explains unsupported tracks without selecting them or starting extraction', async () => {
      const unsupported = { ...embedded, unavailableReason: 'Use an external player for this subtitle format.' };
      render(<Player options={{ src, tracks: [unsupported, spanish] }} />);
      expect(captured.player!.textTracks.selected).toBeNull();
      expect(captured.player!.textTracks.getById(unsupported.id)).toBeNull();
      await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));
      const item = screen.getByRole('menuitemradio', { name: /English/ });
      expect(item).toBeDisabled();
      expect(item).toHaveTextContent(unsupported.unavailableReason);
      await userEvent.click(item);
      expect(loadEmbeddedSubtitleTrackVtt).not.toHaveBeenCalled();
      await userEvent.click(screen.getByRole('menuitemradio', { name: /Spanish/ }));
      expect(captured.player!.textTracks.selected?.id).toBe(spanish.id);
    });

    it('switches tracks without replacing their instances and preserves Off', async () => {
      render(<Player options={{ src, tracks: [{ ...english, default: true }, spanish] }} />);
      act(() => captured.player!.startLoading());
      const list = captured.player!.textTracks;
      const first = list.getById(english.id);
      const second = list.getById(spanish.id);
      expect(list.selected).toBe(first);
      await select('Spanish');
      expect(list.selected).toBe(second);
      await select('Off');
      // Wait past Vidstack's debounced default-track selection.
      await act(async () => { await new Promise(resolve => setTimeout(resolve, 350)); });
      expect(list.selected).toBeNull();
      await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));
      expect(screen.getByRole('menuitemradio', { name: 'Off' })).toHaveAttribute('aria-checked', 'true');
      await userEvent.click(screen.getByRole('menuitemradio', { name: /English/ }));
      expect(list.selected).toBe(first);
      expect(list.getById(spanish.id)).toBe(second);
    });

    it('selects by identity when two tracks have identical labels', async () => {
      render(<Player options={{ src, tracks: [english, { ...spanish, label: 'English' }] }} />);
      await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));
      await userEvent.click(screen.getAllByRole('menuitemradio', { name: /English/ })[0]);
      expect(captured.player!.textTracks.selected?.id).toBe(english.id);
      await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));
      await userEvent.click(screen.getAllByRole('menuitemradio', { name: /English/ })[1]);
      expect(captured.player!.textTracks.selected?.id).toBe(spanish.id);
    });

    it('resets selection when the media source changes', async () => {
      const { rerender } = render(<Player options={{ src, tracks: [english] }} />);
      await select('English');
      rerender(<Player options={{ src: { ...src, src: '/second.mp4' }, tracks: [spanish] }} />);
      expect(captured.player!.textTracks.selected).toBeNull();
      expect(captured.player!.textTracks.getById(english.id)).toBeNull();
    });
  });
}

it('extracts a default embedded track without exposing a binary subtitle source', async () => {
  vi.mocked(probeEmbeddedSubtitleTracks).mockResolvedValue([embedded]);
  render(<VideoPlayer options={{ src: mkv }} />);
  await waitFor(() => expect(loadEmbeddedSubtitleTrackVtt).toHaveBeenCalledTimes(1));
  const track = captured.player!.textTracks.selected!;
  expect(track.id).toBe(embedded.id);
  expect(track.src).toBeUndefined();
  expect(track.cues.map(cue => cue.text)).toEqual(['Embedded dialogue']);
  expect(track.cues[0]).toMatchObject({ align: 'center', position: 50, positionAlign: 'center', size: 100, vertical: '' });
  await select('Off');
  await select('English');
  expect(loadEmbeddedSubtitleTrackVtt).toHaveBeenCalledTimes(1);
});

it('renders selected embedded dialogue in the caption overlay at the playback time', async () => {
  vi.mocked(probeEmbeddedSubtitleTracks).mockResolvedValue([embedded]);
  const { container } = render(<VideoPlayer options={{ src: mkv }} />);
  await waitFor(() => expect(captured.player!.textTracks.selected?.cues).toHaveLength(1));
  act(() => captured.media!.notify('time-change', 2));
  await waitFor(() => expect(container.querySelector('.vds-captions')).toHaveTextContent('Embedded dialogue'));
  expect(container.querySelector('.vds-captions')).toHaveAttribute('aria-hidden', 'false');
});

it.each([false, true])('renders late cues with native video and retries without duplicates (default: %s)', async isDefault => {
  enableNativeVideo();
  vi.mocked(probeEmbeddedSubtitleTracks).mockResolvedValue([{ ...embedded, default: isDefault }]);
  let emit!: (cues: SubtitleCue[]) => void;
  vi.mocked(loadEmbeddedSubtitleTrackVtt).mockImplementation((_url, _number, _fetch, _signal, onCues) => {
    emit = onCues!;
    return new Promise(() => {});
  });
  const { container } = render(<VideoPlayer options={{ src: mkv }} />);
  act(() => captured.player!.startLoading());
  await waitFor(() => expect(captured.player!.provider?.type).toBe('video'));
  const video = container.querySelector('video')!;
  act(() => {
    fireEvent.loadStart(video);
    fireEvent.loadedMetadata(video);
    fireEvent.canPlay(video);
  });
  await waitFor(() => expect(captured.player!.state.canPlay).toBe(true));
  await waitFor(() => expect(video.querySelector('track')).not.toBeNull());
  if (!isDefault) await select('English');
  const nativeTrack = video.querySelector('track')!.track;
  act(() => { captured.player!.controls = true; });
  await waitFor(() => expect(nativeTrack.mode).toBe('showing'));
  act(() => { video.currentTime = 2; fireEvent.timeUpdate(video); });
  await waitFor(() => expect(captured.player!.currentTime).toBe(2));
  await act(async () => emit([cue]));
  expect(Array.from(nativeTrack.cues ?? []).map(cue => (cue as VTTCue).text)).toEqual(['Embedded dialogue']);
  act(() => { captured.player!.controls = false; });
  await waitFor(() => expect(container.querySelector('.vds-captions')).toHaveTextContent('Embedded dialogue'));
  await select('Off');
  await select('English');
  expect(loadEmbeddedSubtitleTrackVtt).toHaveBeenCalledTimes(2);
  await act(async () => emit([cue]));
  await waitFor(() => expect(container.querySelector('.vds-captions')).toHaveTextContent('Embedded dialogue'));
  expect(captured.player!.textTracks.selected!.cues).toHaveLength(1);
  act(() => { captured.player!.controls = true; });
  await waitFor(() => expect(nativeTrack.mode).toBe('showing'));
  expect(nativeTrack.cues).toHaveLength(1);
});

it.each(['S_TEXT/UTF8', 'S_TEXT/ASS', 'S_TEXT/SSA'])(
  'renders real MKV %s subtitles while later torrent ranges are still downloading', async codecId => {
    const actual = await vi.importActual<typeof import('@/lib/mkv-subtitles')>('@/lib/mkv-subtitles');
    vi.mocked(probeEmbeddedSubtitleTracks).mockImplementation(actual.probeEmbeddedSubtitleTracks);
    vi.mocked(loadEmbeddedSubtitleTrackVtt).mockImplementation(actual.loadEmbeddedSubtitleTrackVtt);
    const tracks = createEbmlElement(0x1654ae6b, createEbmlElement(0xae, concatBuffers(
      createUIntElement(0xd7, 1), createUIntElement(0x83, 17),
      createStringElement(0x86, codecId), createStringElement(0x22b59c, 'eng')
    )));
    const dialogue = codecId === 'S_TEXT/UTF8'
      ? 'Real embedded dialogue' : '0,0,Default,,0,0,0,,{\\i1}Real embedded dialogue{\\i0}';
    const video = createEbmlElement(0xa3, concatBuffers(new Uint8Array([0x82, 0, 0, 0]), new Uint8Array(300000)));
    const buffer = createEbmlElement(0x18538067, concatBuffers(
      tracks,
      createEbmlElement(0x1f43b675, concatBuffers(
        createUIntElement(0xe7, 1000, 4),
        createEbmlElement(0xa0, concatBuffers(
          createEbmlElement(0xa1, concatBuffers(new Uint8Array([0x81, 0, 0, 0]), new TextEncoder().encode(dialogue))),
          createUIntElement(0x9b, 2000, 2)
        )), video
      )),
      subtitleCluster(10000, 'Later dialogue')
    ));
    const respond = rangeFetch(buffer);
    let release!: () => void;
    const pendingDownload = new Promise<void>(resolve => { release = resolve; });
    vi.mocked(fetch).mockImplementation(async (url, init) => {
      if (!new Headers(init?.headers).get('Range')?.startsWith('bytes=0-')) await pendingDownload;
      return respond(url, init);
    });
    const { container, unmount } = render(<VideoPlayer options={{ src: mkv }} />);
    try {
      await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
      act(() => captured.media!.notify('time-change', 2));
      const overlay = container.querySelector('.vds-captions');
      await waitFor(() => expect(overlay).toHaveTextContent('Real embedded dialogue'));
      expect(overlay).toHaveAttribute('aria-hidden', 'false');
      expect(overlay).toHaveAttribute('data-embedded', 'true');
      const display = overlay!.querySelector<HTMLElement>('[data-part="cue-display"]')!;
      expect(display.style.getPropertyValue('--cue-text-align')).toBe('center');
      expect(display.style.getPropertyValue('--cue-width')).toBe('100%');
      act(() => captured.media!.notify('time-change', 4));
      await waitFor(() => expect(overlay).not.toHaveTextContent('Real embedded dialogue'));
      act(() => captured.media!.notify('time-change', 2));
      await waitFor(() => expect(overlay).toHaveTextContent('Real embedded dialogue'));
      await select('Off');
      expect(overlay).toHaveAttribute('aria-hidden', 'true');
    } finally {
      unmount();
      await act(async () => { release(); });
    }
  }
);

it('honors Off when a default embedded track arrives after probing', async () => {
  let resolveProbe!: (tracks: SubtitleTrackInfo[]) => void;
  vi.mocked(probeEmbeddedSubtitleTracks).mockReturnValue(new Promise(resolve => { resolveProbe = resolve; }));
  render(<VideoPlayer options={{ src: mkv, tracks: [spanish] }} />);
  await select('Off');
  await act(async () => resolveProbe([embedded]));
  expect(captured.player!.textTracks.selected).toBeNull();
  expect(loadEmbeddedSubtitleTrackVtt).not.toHaveBeenCalled();
});

it('cancels pending extraction and ignores late cues after selecting Off', async () => {
  let emit!: (cues: SubtitleCue[]) => void;
  let finish!: (vtt: string) => void;
  let signal!: AbortSignal;
  vi.mocked(probeEmbeddedSubtitleTracks).mockResolvedValue([embedded]);
  vi.mocked(loadEmbeddedSubtitleTrackVtt).mockImplementation((_url, _track, _fetch, abortSignal, onCues) => {
    signal = abortSignal!;
    emit = onCues!;
    return new Promise(resolve => { finish = resolve; });
  });
  const { unmount } = render(<VideoPlayer options={{ src: mkv }} />);
  await waitFor(() => expect(loadEmbeddedSubtitleTrackVtt).toHaveBeenCalledTimes(1));
  const track = captured.player!.textTracks.selected!;
  await select('Off');
  expect(signal.aborted).toBe(true);
  await act(async () => { emit([cue]); finish(vtt); });
  expect(track.cues).toHaveLength(0);
  expect(captured.player!.textTracks.selected).toBeNull();
  await select('English');
  expect(loadEmbeddedSubtitleTrackVtt).toHaveBeenCalledTimes(2);
  unmount();
  expect(signal.aborted).toBe(true);
});

it('isolates scans for two sources that use the same embedded track number', async () => {
  const scans: { signal: AbortSignal, emit: (cues: SubtitleCue[]) => void, finish: (vtt: string) => void }[] = [];
  vi.mocked(probeEmbeddedSubtitleTracks).mockResolvedValue([embedded]);
  vi.mocked(loadEmbeddedSubtitleTrackVtt).mockImplementation((_url, _track, _fetch, signal, emit) => new Promise(finish => {
    scans.push({ signal: signal!, emit: emit!, finish });
  }));
  const { rerender } = render(<VideoPlayer options={{ src: mkv }} />);
  await waitFor(() => expect(scans).toHaveLength(1));
  rerender(<VideoPlayer options={{ src: { ...mkv, src: '/second.mkv' } }} />);
  await waitFor(() => expect(scans).toHaveLength(2));
  expect(scans[0].signal.aborted).toBe(true);
  await act(async () => {
    scans[0].emit([{ ...cue, text: 'Old movie' }]);
    scans[0].finish(vtt);
    scans[1].emit([{ ...cue, text: 'New movie' }]);
    scans[1].finish(vtt);
  });
  expect(captured.player!.textTracks.selected!.cues.map(cue => cue.text)).toEqual(['New movie']);
});

it('allows retry after an extraction error', async () => {
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  vi.mocked(probeEmbeddedSubtitleTracks).mockResolvedValue([embedded]);
  vi.mocked(loadEmbeddedSubtitleTrackVtt).mockRejectedValueOnce(new Error('offline'));
  render(<VideoPlayer options={{ src: mkv }} />);
  await waitFor(() => expect(console.warn).toHaveBeenCalled());
  await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole('menuitemradio', { name: 'Off' }));
  await select('English');
  await waitFor(() => expect(captured.player!.textTracks.selected!.cues).toHaveLength(1));
  expect(loadEmbeddedSubtitleTrackVtt).toHaveBeenCalledTimes(2);
});

it('does not expose embedded extraction progress in the subtitle menu', async () => {
  let finish!: (vtt: string) => void;
  vi.mocked(probeEmbeddedSubtitleTracks).mockResolvedValue([embedded]);
  vi.mocked(loadEmbeddedSubtitleTrackVtt).mockReturnValue(new Promise(resolve => { finish = resolve; }));
  render(<VideoPlayer options={{ src: mkv }} />);
  await waitFor(() => expect(loadEmbeddedSubtitleTrackVtt).toHaveBeenCalledTimes(1));
  await userEvent.click(screen.getByRole('button', { name: /select subtitle track/i }));
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
  await act(async () => finish('data:text/vtt,WEBVTT'));
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});

it('pauses real extraction ahead of playback, reuses cached cues, and seeks in both directions', async () => {
  const actual = await vi.importActual<typeof import('@/lib/mkv-subtitles')>('@/lib/mkv-subtitles');
  vi.mocked(probeEmbeddedSubtitleTracks).mockImplementation(actual.probeEmbeddedSubtitleTracks);
  vi.mocked(loadEmbeddedSubtitleTrackVtt).mockImplementation(actual.loadEmbeddedSubtitleTrackVtt);
  const { buffer } = indexedSubtitleMovie();
  const respond = rangeFetch(buffer);
  vi.mocked(fetch).mockImplementation(respond);
  const { container } = render(<VideoPlayer options={{ src: mkv }} />);
  await waitFor(() => expect(captured.player!.textTracks.selected?.cues).toHaveLength(4));
  const before = respond.mock.calls.length;
  expect(before).toBeLessThanOrEqual(4);
  await select('Off');
  await select('Unknown');
  await waitFor(() => expect(loadEmbeddedSubtitleTrackVtt).toHaveBeenCalledTimes(2));
  expect(respond).toHaveBeenCalledTimes(before);
  act(() => {
    captured.media!.notify('seeking', 100);
    captured.media!.notify('time-change', 100);
    captured.media!.notify('seeked', 100);
  });
  await waitFor(() => expect(container.querySelector('.vds-captions')).toHaveTextContent('Cue 100'));
  expect(captured.player!.textTracks.selected!.cues.filter(cue => cue.text === 'Cue 0')).toHaveLength(1);
  expect(respond.mock.calls.length - before).toBeLessThanOrEqual(4);
  act(() => {
    captured.media!.notify('seeking', 10);
    captured.media!.notify('time-change', 10);
    captured.media!.notify('seeked', 10);
  });
  await waitFor(() => expect(loadEmbeddedSubtitleTrackVtt).toHaveBeenCalledTimes(4));
  await waitFor(() => expect(container.querySelector('.vds-captions')).toHaveTextContent('Cue 10'));
});

it('aborts a pending range immediately and coalesces repeated seek events', async () => {
  const signals: AbortSignal[] = [];
  vi.mocked(probeEmbeddedSubtitleTracks).mockResolvedValue([embedded]);
  vi.mocked(loadEmbeddedSubtitleTrackVtt).mockImplementation((_url, _number, _fetch, signal) => {
    signals.push(signal!);
    return new Promise(() => {});
  });
  const { unmount } = render(<VideoPlayer options={{ src: mkv }} />);
  await waitFor(() => expect(signals).toHaveLength(1));
  act(() => {
    captured.media!.notify('seeking', 60);
    captured.media!.notify('seeking', 80);
    captured.media!.notify('time-change', 80);
    captured.media!.notify('seeked', 80);
  });
  expect(signals[0].aborted).toBe(true);
  expect(signals).toHaveLength(1);
  await waitFor(() => expect(signals).toHaveLength(2));
  const calls = vi.mocked(loadEmbeddedSubtitleTrackVtt).mock.calls;
  expect(calls[0][5]?.cache).toBe(calls[1][5]?.cache);
  expect(calls[1][5]?.currentTime()).toBe(80);
  unmount();
  expect(signals[1].aborted).toBe(true);
});
