// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { type MediaPlayerInstance, TextTrack } from '@vidstack/react';
import { type RefObject, useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { loadEmbeddedSubtitleTrackVtt, probeEmbeddedSubtitleTracks, SUBTITLE_SEEK_PREROLL_SECONDS, type SubtitleCue, SubtitleSourceCache } from '@/lib/mkv-subtitles';
import { type SubtitleTrackInfo } from '@/lib/video-utils';

const EMPTY_TRACKS: SubtitleTrackInfo[] = [];

function waitForTimeChange(player: MediaPlayerInstance, signal?: AbortSignal): Promise<void> {
  signal?.throwIfAborted();
  return new Promise((resolve, reject) => {
    const cleanup = () => {
      player.removeEventListener('time-update', wake);
      signal?.removeEventListener('abort', abort);
    };
    const wake = () => { cleanup(); resolve(); };
    const abort = () => { cleanup(); reject(signal?.reason); };
    player.addEventListener('time-update', wake);
    signal?.addEventListener('abort', abort, { once: true });
  });
}

interface SubtitleOptions {
  player: RefObject<MediaPlayerInstance | null>,
  sourceKey?: string,
  tracks?: SubtitleTrackInfo[],
  enabled?: boolean,
  embeddedStreamUrl?: string
}

/** Own track instances and selection together; presentation labels are never identities. */
export function useSubtitleTracks({
  player,
  sourceKey,
  tracks = EMPTY_TRACKS,
  enabled = true,
  embeddedStreamUrl,
}: SubtitleOptions) {
  const [embedded, setEmbedded] = useState<{ source?: string, tracks: SubtitleTrackInfo[] }>({ tracks: [] });
  // undefined means no user choice yet; null is an explicit Off choice.
  const [choice, setChoice] = useState<{ source?: string, id: string | null } | null>(null);
  const registered = useRef(new Map<string, { info: SubtitleTrackInfo, track: TextTrack }>());
  const completed = useRef(new Set<TextTrack>());
  const sourceCache = useMemo(() => embeddedStreamUrl ? new SubtitleSourceCache(embeddedStreamUrl) : undefined, [embeddedStreamUrl]);
  const activeScan = useRef<AbortController | null>(null);
  const [seekVersion, setSeekVersion] = useState(0);

  useEffect(() => {
    if (!enabled || !embeddedStreamUrl) return;
    const controller = new AbortController();
    probeEmbeddedSubtitleTracks(embeddedStreamUrl, fetch, controller.signal, sourceCache).then(result => {
      if (!controller.signal.aborted) setEmbedded({ source: embeddedStreamUrl, tracks: result });
    });
    return () => controller.abort();
  }, [embeddedStreamUrl, enabled, sourceCache]);

  const allTracks = useMemo(() => {
    const embeddedTracks = enabled && embeddedStreamUrl && embedded.source === embeddedStreamUrl
      ? embedded.tracks : EMPTY_TRACKS;
    return [...tracks, ...embeddedTracks.filter(track => !tracks.some(external => external.id === track.id))];
  }, [tracks, embedded, embeddedStreamUrl, enabled]);

  const selectedTrackId = choice && choice.source === sourceKey
    ? (allTracks.some(track => track.id === choice.id && !track.unavailableReason) ? choice.id : null)
    : allTracks.find(track => track.default && !track.unavailableReason)?.id ?? null;

  const selectTrack = useCallback((id: string | null) => {
    setChoice({ source: sourceKey, id });
  }, [sourceKey]);

  // Clear all player-owned state on source changes, disabling, or unmount.
  useEffect(() => {
    if (!enabled || !player.current) return;
    const list = player.current.textTracks;
    const entries = registered.current;
    const loaded = completed.current;
    return () => {
      for (const { track } of entries.values()) list.remove(track);
      entries.clear();
      loaded.clear();
    };
  }, [player, sourceKey, enabled]);

  useEffect(() => {
    if (!enabled || !player.current) return;
    const list = player.current.textTracks;
    const entries = registered.current;
    for (const [id, entry] of entries) {
      const info = allTracks.find(track => track.id === id && !track.unavailableReason);
      if (!info || info.src !== entry.info.src || info.embeddedTrackNumber !== entry.info.embeddedTrackNumber) {
        list.remove(entry.track);
        completed.current.delete(entry.track);
        entries.delete(id);
      }
    }
    for (const info of allTracks) {
      if (info.unavailableReason) continue;
      if (!entries.has(info.id)) {
        const track = new TextTrack({
          id: info.id,
          src: info.embeddedTrackNumber === undefined ? info.src : undefined,
          kind: info.kind ?? 'subtitles',
          type: info.type,
          label: info.label,
          language: info.language,
          // Defaults are resolved above; do not let Vidstack auto-select a removed track.
          default: false,
        });
        entries.set(info.id, { info, track });
        list.add(track);
      }
    }
    for (const { track } of entries.values()) {
      track.mode = track.id === selectedTrackId ? 'showing' : 'disabled';
    }
  }, [allTracks, selectedTrackId, enabled, player, sourceKey]);

  const selectedTrackNumber = allTracks.find(track => track.id === selectedTrackId)?.embeddedTrackNumber;

  useEffect(() => {
    const instance = player.current;
    if (!enabled || !instance || selectedTrackNumber === undefined) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const onSeek = () => {
      activeScan.current?.abort();
      clearTimeout(timer);
      // Coalesce slider scrubbing, and abort a blocked old range immediately.
      timer = setTimeout(() => setSeekVersion(version => version + 1), 150);
    };
    instance.addEventListener('seeking', onSeek);
    instance.addEventListener('seeked', onSeek);
    return () => {
      clearTimeout(timer);
      instance.removeEventListener('seeking', onSeek);
      instance.removeEventListener('seeked', onSeek);
    };
  }, [enabled, player, sourceKey, selectedTrackNumber]);

  useEffect(() => {
    const instance = player.current;
    if (!enabled || !embeddedStreamUrl || !sourceCache || !instance || selectedTrackId === null) return;
    const entry = registered.current.get(selectedTrackId);
    if (!entry || entry.info.embeddedTrackNumber === undefined) return;
    const { track, info } = entry;
    // Keep usable cues when retrying an interrupted scan. Native text tracks may
    // not contain every Vidstack cue, so removing them can throw NotFoundError.
    const cueKey = (cue: SubtitleCue) => JSON.stringify([cue.startTime, cue.endTime, cue.text]);
    const loadedCues = new Set(track.cues.map(cueKey));
    if (completed.current.has(track)) return;
    const controller = new AbortController();
    activeScan.current = controller;
    const startedAt = instance.currentTime;
    loadEmbeddedSubtitleTrackVtt(embeddedStreamUrl, info.embeddedTrackNumber!, fetch, controller.signal, cues => {
      if (controller.signal.aborted) return;
      for (const cue of cues) {
        const key = cueKey(cue);
        if (loadedCues.has(key)) continue;
        const vttCue = new VTTCue(cue.startTime, cue.endTime, cue.text);
        vttCue.align = 'center';
        vttCue.position = 50;
        vttCue.positionAlign = 'center';
        vttCue.size = 100;
        vttCue.vertical = '';
        vttCue.line = 'auto';
        vttCue.snapToLines = true;
        track.addCue(vttCue);
        // Vidstack does not forward native VTTCue instances added after the
        // <track> element is attached. Native controls need those cues too.
        const native = Array.from(player.current?.el?.querySelectorAll('track') ?? [])
          .find(element => element.id === track.id)?.track;
        if (native && !Array.from(native.cues ?? []).includes(vttCue)) native.addCue(vttCue);
        loadedCues.add(key);
      }
    }, {
      cache: sourceCache,
      currentTime: () => instance.currentTime,
      waitForTimeChange: signal => waitForTimeChange(instance, signal),
    }).then(() => {
      if (!controller.signal.aborted) {
        // A scan started near a seek target has not loaded the earlier movie.
        if (startedAt <= SUBTITLE_SEEK_PREROLL_SECONDS) completed.current.add(track);
      }
    }).catch(error => {
      if (!controller.signal.aborted) {
        console.warn('Failed to extract embedded subtitles:', error);
      }
    });
    return () => {
      controller.abort();
      if (activeScan.current === controller) activeScan.current = null;
    };
  }, [embeddedStreamUrl, selectedTrackId, selectedTrackNumber, enabled, sourceKey, player, sourceCache, seekVersion]);

  return { tracks: allTracks, selectedTrackId, selectTrack };
}
