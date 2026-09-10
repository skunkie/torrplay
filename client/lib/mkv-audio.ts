// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { registerAc3Decoder } from '@mediabunny/ac3';
import { registerDtsDecoder } from '@mediabunny/dts';
import {
  ALL_FORMATS,
  type AudioSample,
  AudioSampleSink,
  Input,
  type InputAudioTrack,
  UrlSource,
} from 'mediabunny';

let decodersRegistered = false;

export function ensureDecodersRegistered() {
  if (typeof window === 'undefined' || decodersRegistered) return;
  try {
    registerAc3Decoder();
    registerDtsDecoder();
    decodersRegistered = true;
  } catch (err) {
    console.warn('Failed to register Mediabunny audio decoders:', err);
  }
}

if (typeof window !== 'undefined') {
  ensureDecodersRegistered();
}

export interface AudioTrackInfo {
  id: number,
  index: number,
  name: string,
  language: string,
  codec: string,
  channels: number,
  sampleRate: number,
  bitrate?: number,
  isDefault: boolean,
  isNativelySupported: boolean,
  label: string
}

const NATIVE_CODECS = new Set(['aac', 'mp3', 'opus', 'vorbis', 'flac']);

export function isAudioDecodingSupported(): boolean {
  if (typeof window === 'undefined') return false;
  return (
    typeof (window as unknown as { AudioDecoder?: unknown }).AudioDecoder !== 'undefined' ||
    typeof window.AudioContext !== 'undefined' ||
    typeof (window as unknown as { webkitAudioContext?: unknown }).webkitAudioContext !== 'undefined'
  );
}

export function formatLanguage(lang: string | null | undefined): string {
  if (!lang || lang === 'und') return 'Unknown';
  try {
    const languageNames = new Intl.DisplayNames(['en'], { type: 'language' });
    return languageNames.of(lang) || lang.toUpperCase();
  } catch {
    return lang.toUpperCase();
  }
}

export function formatChannelCount(channels: number): string {
  if (channels === 6) return '5.1';
  if (channels === 8) return '7.1';
  if (channels === 2) return 'Stereo';
  if (channels === 1) return 'Mono';
  return `${channels} ch`;
}

export function getDefaultAudioTrackIndex(tracks: AudioTrackInfo[]): number {
  const defaultIndex = tracks.findIndex(track => track.isDefault);
  return defaultIndex >= 0 ? defaultIndex : 0;
}

/**
 * Inspects a media stream URL and extracts metadata for all available audio tracks.
 */
export async function probeAudioTracks(streamUrl: string): Promise<{
  input: Input,
  tracks: AudioTrackInfo[],
  audioTrackObjects: InputAudioTrack[]
}> {
  ensureDecodersRegistered();

  const input = new Input({
    formats: ALL_FORMATS,
    source: new UrlSource(streamUrl),
  });

  const rawTracks = await input.getAudioTracks();

  const tracks: AudioTrackInfo[] = await Promise.all(
    rawTracks.map(async (track, index) => {
      const rawCodec = await track.getCodec();
      const codec = rawCodec ? rawCodec.toLowerCase() : 'unknown';
      const language = (await track.getLanguageCode()) || 'und';
      const name = (await track.getName()) || '';
      const channels = await track.getNumberOfChannels();
      const sampleRate = await track.getSampleRate();
      const bitrate = (await track.getBitrate()) || undefined;
      const disposition = await track.getDisposition();

      const isNativelySupported = NATIVE_CODECS.has(codec);

      const langDisplay = formatLanguage(language);
      const chDisplay = formatChannelCount(channels);
      const codecDisplay = codec.toUpperCase();

      let label = `${langDisplay} (${codecDisplay}, ${chDisplay})`;
      if (name) {
        label = `${name} - ${label}`;
      }

      return {
        id: track.id,
        index,
        name,
        language,
        codec,
        channels,
        sampleRate,
        bitrate,
        isDefault: disposition.default,
        isNativelySupported,
        label,
      };
    })
  );

  return { input, tracks, audioTrackObjects: rawTracks };
}

/**
 * Web Audio synchronization engine that decodes audio with Mediabunny
 * and dynamically keeps it in sync with the video playback clock.
 */
export class MkvAudioSyncEngine {
  private input: Input | null = null;
  private rawTracks: InputAudioTrack[] = [];
  private selectedTrackIndex: number = 0;
  private audioCtx: AudioContext | null = null;
  private gainNode: GainNode | null = null;
  private nativeSourceNode: MediaElementAudioSourceNode | null = null;
  private nativeGainNode: GainNode | null = null;
  private boundElement: HTMLMediaElement | null = null;
  private nativeIsolationFailed: boolean = false;
  private videoFrameCallbackId: number | null = null;
  private videoFrameElement: HTMLVideoElement | null = null;
  private lastVideoFrameWallTime: number | null = null;
  private isWasmActive: boolean = true;
  private currentSink: AudioSampleSink | null = null;
  private currentGenerator: AsyncGenerator<AudioSample, void, unknown> | null = null;
  private activeSources: Set<AudioBufferSourceNode> = new Set();
  private abortController: AbortController | null = null;
  private pipelineId: number = 0;
  private hasFatalError: boolean = false;
  private onError?: (err: unknown) => void;

  private isMuted: boolean = false;
  private volume: number = 1;
  private isPaused: boolean = true;
  private playbackRate: number = 1;

  // Sync state
  private lastKnownVideoTime: number = 0;
  private audioEpochCtxTime: number = 0;
  private audioEpochVideoTime: number = 0;
  private nextScheduledTime: number = 0;
  // Web Audio ctx-time at which the current run of soft drift was first observed, or
  // null when the last observation was within tolerance. Using elapsed ctx-time rather
  // than a tick counter keeps the debounce window meaningful regardless of how often
  // observeVideoTime is sampled (per-video-frame via rVFC vs. per-timeupdate fallback).
  private softDriftSinceCtxTime: number | null = null;

  // Sync parameters
  private readonly LOOKAHEAD_SECONDS = 0.25;
  private readonly SCHEDULE_INTERVAL_MS = 100;
  private readonly DRIFT_TOLERANCE_SECONDS = 0.05; // sustained drift above 50ms is perceptible
  private readonly SUSTAINED_DRIFT_SECONDS = 0.15; // soft drift must persist this long before resync
  private readonly HARD_DRIFT_THRESHOLD = 0.25; // large drift needs an immediate resync
  private readonly FRAME_CALLBACK_STALE_MS = 500;
  private readonly MAX_OUTPUT_LATENCY_SECONDS = 0.25;
  private readonly MAX_FRAME_DISPLAY_DELAY_SECONDS = 0.25;

  constructor(
    input: Input,
    rawTracks: InputAudioTrack[],
    onError?: (err: unknown) => void
  ) {
    this.input = input;
    this.rawTracks = rawTracks;
    this.onError = onError;
  }

  private initAudioContext(): AudioContext {
    if (!this.audioCtx || this.audioCtx.state === 'closed') {
      const AudioCtxClass =
        window.AudioContext ||
        (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
      this.audioCtx = new AudioCtxClass();
      this.gainNode = this.audioCtx.createGain();
      this.gainNode.connect(this.audioCtx.destination);
      this.updateGains();
    }
    return this.audioCtx;
  }

  private updateGains() {
    const effectiveVolume = this.isMuted ? 0 : this.volume;
    if (this.gainNode) {
      this.gainNode.gain.value = this.isWasmActive ? effectiveVolume : 0;
    }
    if (this.nativeGainNode) {
      this.nativeGainNode.gain.value = this.isWasmActive ? 0 : effectiveVolume;
    }
  }

  /**
   * Best-effort defense-in-depth: attempts to disable native audio tracks via the
   * HTMLMediaElement.audioTracks API on supported browsers (e.g. Safari), complementing
   * the primary Web Audio MediaElementAudioSourceNode gain isolation.
   */
  private syncAudioTracks() {
    if (this.boundElement && 'audioTracks' in this.boundElement) {
      try {
        const at = (this.boundElement as unknown as { audioTracks?: { length: number, [index: number]: { enabled: boolean } } }).audioTracks;
        if (at) {
          for (let i = 0; i < at.length; i++) {
            at[i].enabled = !this.isWasmActive && i === this.selectedTrackIndex;
          }
        }
      } catch {
        // audioTracks API might be read-only or unsupported in some browsers
      }
    }
  }

  private stopVideoFrameSync() {
    const video = this.videoFrameElement;
    if (video && this.videoFrameCallbackId !== null &&
      typeof video.cancelVideoFrameCallback === 'function') {
      video.cancelVideoFrameCallback(this.videoFrameCallbackId);
    }
    this.videoFrameCallbackId = null;
    this.videoFrameElement = null;
    this.lastVideoFrameWallTime = null;
  }

  private startVideoFrameSync(videoEl: HTMLMediaElement) {
    if (!(videoEl instanceof HTMLVideoElement) ||
      typeof videoEl.requestVideoFrameCallback !== 'function') return;

    this.videoFrameElement = videoEl;
    const observeFrame = (now: number, metadata: VideoFrameCallbackMetadata) => {
      this.videoFrameCallbackId = null;
      if (this.videoFrameElement !== videoEl || this.boundElement !== videoEl) return;

      this.lastVideoFrameWallTime = now;
      const displayDelay = Math.max(0, Math.min(
        (metadata.expectedDisplayTime - now) / 1000,
        this.MAX_FRAME_DISPLAY_DELAY_SECONDS,
      ));
      const sampleCtxTime = (this.audioCtx?.currentTime ?? 0) + displayDelay;
      this.observeVideoTime(metadata.mediaTime, sampleCtxTime);
      this.videoFrameCallbackId = videoEl.requestVideoFrameCallback(observeFrame);
    };

    this.videoFrameCallbackId = videoEl.requestVideoFrameCallback(observeFrame);
  }

  public setWasmActive(active: boolean): boolean {
    if (active && this.boundElement && !this.nativeGainNode) {
      this.isWasmActive = false;
      this.updateGains();
      this.syncAudioTracks();
      return false;
    }
    this.isWasmActive = active;
    if (!active) {
      this.stopAllSources();
    }
    this.updateGains();
    this.syncAudioTracks();
    return true;
  }

  public setNativeTrackIndex(trackIndex: number) {
    if (trackIndex < 0 || trackIndex >= this.rawTracks.length) return;
    this.selectedTrackIndex = trackIndex;
    if (!this.isWasmActive) this.syncAudioTracks();
  }

  /**
   * Attaches the native HTMLMediaElement to the Web Audio graph to isolate and silence
   * its native audio output when WASM audio decoding is active.
   * If the underlying element changes (e.g. provider re-mount), it cleans up and re-attaches.
   */
  public attachMediaElement(videoEl: HTMLMediaElement): boolean {
    if (typeof window === 'undefined') return false;

    if (this.boundElement !== videoEl) {
      this.stopVideoFrameSync();
      // Disconnect previous element nodes if re-binding
      if (this.nativeSourceNode) {
        try {
          this.nativeSourceNode.disconnect();
        } catch {
          // Ignore
        }
        this.nativeSourceNode = null;
      }
      if (this.nativeGainNode) {
        try {
          this.nativeGainNode.disconnect();
        } catch {
          // Ignore
        }
        this.nativeGainNode = null;
      }

      this.boundElement = videoEl;
      this.nativeIsolationFailed = false;

      try {
        const ctx = this.initAudioContext();
        if (typeof ctx.createMediaElementSource === 'function') {
          this.nativeSourceNode = ctx.createMediaElementSource(videoEl);
          this.nativeGainNode = ctx.createGain();
          this.nativeSourceNode.connect(this.nativeGainNode);
          this.nativeGainNode.connect(ctx.destination);
          this.updateGains();
        }
      } catch (err) {
        // A media element can only be associated with one source node for its
        // lifetime. Never run decoded audio when native audio cannot be muted.
        this.nativeIsolationFailed = true;
        this.isWasmActive = false;
        this.updateGains();
        this.onError?.(err);
      }

      this.startVideoFrameSync(videoEl);
    }

    this.syncAudioTracks();
    return !this.nativeIsolationFailed && this.nativeGainNode !== null;
  }

  public selectTrack(trackIndex: number): boolean {
    if (trackIndex < 0 || trackIndex >= this.rawTracks.length) return false;
    if (!this.setWasmActive(true)) return false;
    this.hasFatalError = false;
    this.selectedTrackIndex = trackIndex;
    this.restartPipeline(this.lastKnownVideoTime);
    return true;
  }

  public setVolume(volume: number) {
    this.volume = Math.max(0, Math.min(1, volume));
    this.updateGains();
  }

  public setMuted(muted: boolean) {
    this.isMuted = muted;
    this.updateGains();
  }

  public setPlaybackRate(rate: number) {
    this.playbackRate = rate;
    this.restartPipeline(this.lastKnownVideoTime);
  }

  public onPlay(currentTime: number) {
    this.isPaused = false;
    const ctx = this.initAudioContext();
    if (ctx.state === 'suspended') {
      ctx.resume();
    }
    if (this.isWasmActive) {
      this.restartPipeline(currentTime);
    }
  }

  public onPlaying(currentTime: number) {
    this.isPaused = false;
    const ctx = this.initAudioContext();
    if (ctx.state === 'suspended') {
      ctx.resume();
    }
    if (this.isWasmActive) {
      this.restartPipeline(currentTime);
    }
  }

  public onWaiting() {
    this.stopAllSources();
    if (this.audioCtx && this.audioCtx.state === 'running' && this.isWasmActive) {
      this.audioCtx.suspend();
    }
  }

  public onPause() {
    this.isPaused = true;
    this.stopAllSources();
    if (this.audioCtx && this.audioCtx.state === 'running' && this.isWasmActive) {
      this.audioCtx.suspend();
    }
  }

  public onSeek(currentTime: number) {
    this.lastKnownVideoTime = currentTime;
    if (!this.isPaused) {
      this.restartPipeline(currentTime);
    }
  }

  /**
   * Called on video timeupdate to measure and correct any clock drift between video and Web Audio.
   */
  public onTimeUpdate(videoTime: number) {
    if (this.isPaused || !this.audioCtx || this.hasFatalError) return;
    this.lastKnownVideoTime = videoTime;

    const ctx = this.audioCtx;
    if (ctx.state !== 'running') return;

    const now = typeof performance === 'undefined' ? 0 : performance.now();
    if (this.lastVideoFrameWallTime !== null &&
      now - this.lastVideoFrameWallTime < this.FRAME_CALLBACK_STALE_MS) return;

    this.observeVideoTime(videoTime, ctx.currentTime);
  }

  private observeVideoTime(videoTime: number, sampleCtxTime: number) {
    if (this.isPaused || !this.audioCtx || this.audioCtx.state !== 'running' ||
      this.hasFatalError || !this.isWasmActive) return;
    this.lastKnownVideoTime = videoTime;

    // Expected video playback position based on Web Audio clock
    const expectedVideoTime =
      this.audioEpochVideoTime +
      (sampleCtxTime - this.audioEpochCtxTime) * this.playbackRate;

    const drift = Math.abs(videoTime - expectedVideoTime);

    if (drift > this.HARD_DRIFT_THRESHOLD) {
      this.softDriftSinceCtxTime = null;
      this.restartPipeline(videoTime, sampleCtxTime);
    } else if (drift > this.DRIFT_TOLERANCE_SECONDS) {
      if (this.softDriftSinceCtxTime === null) {
        this.softDriftSinceCtxTime = sampleCtxTime;
      } else if (sampleCtxTime - this.softDriftSinceCtxTime >= this.SUSTAINED_DRIFT_SECONDS) {
        this.softDriftSinceCtxTime = null;
        this.restartPipeline(videoTime, sampleCtxTime);
      }
    } else {
      this.softDriftSinceCtxTime = null;
    }
  }

  private stopAllSources() {
    this.pipelineId++;
    if (this.abortController) {
      this.abortController.abort();
      this.abortController = null;
    }

    const generator = this.currentGenerator;
    this.currentGenerator = null;
    if (generator) {
      generator.return().catch(() => {});
    }

    // Synchronously stop, disconnect, and clear all active sources
    const sources = Array.from(this.activeSources);
    this.activeSources.clear();
    for (const source of sources) {
      try {
        source.stop();
        source.disconnect();
      } catch {
        // Source might have already ended
      }
    }
  }

  private restartPipeline(startTime: number, epochCtxTime?: number) {
    this.stopAllSources();
    if (this.isPaused || this.hasFatalError) return;

    const track = this.rawTracks[this.selectedTrackIndex];
    if (!track) return;

    try {
      this.currentSink = new AudioSampleSink(track);
    } catch (err: unknown) {
      console.warn('MkvAudioSyncEngine failed to create AudioSampleSink:', err);
      this.hasFatalError = true;
      this.onError?.(err);
      return;
    }

    this.abortController = new AbortController();
    const signal = this.abortController.signal;
    const currentPipelineId = this.pipelineId;

    const ctx = this.initAudioContext();
    this.audioEpochCtxTime = epochCtxTime ?? ctx.currentTime;
    this.audioEpochVideoTime = startTime;
    this.lastKnownVideoTime = startTime;
    this.nextScheduledTime = ctx.currentTime;

    this.startStreaming(signal, startTime, currentPipelineId);
  }

  private async startStreaming(signal: AbortSignal, startTime: number, currentPipelineId: number) {
    if (!this.currentSink || !this.audioCtx || !this.gainNode) return;
    const ctx = this.audioCtx;
    const gain = this.gainNode;

    const generator = this.currentSink.samples(startTime, Infinity);
    this.currentGenerator = generator;

    try {
      while (!signal.aborted && !this.isPaused && this.pipelineId === currentPipelineId) {
        // Only schedule up to lookahead window to prevent unbounded pre-scheduling
        if (this.nextScheduledTime > ctx.currentTime + this.LOOKAHEAD_SECONDS) {
          await new Promise(resolve => setTimeout(resolve, this.SCHEDULE_INTERVAL_MS));
          if (signal.aborted || this.isPaused || this.pipelineId !== currentPipelineId) break;
          continue;
        }

        const nextResult = await generator.next();
        if (nextResult.done || signal.aborted || this.isPaused || this.pipelineId !== currentPipelineId) break;

        const sample = nextResult.value;
        let buffer: AudioBuffer;
        const timestamp = sample.timestamp;
        const duration = sample.duration;

        try {
          buffer = sample.toAudioBuffer();
        } finally {
          sample.close();
        }

        if (signal.aborted || this.isPaused || this.pipelineId !== currentPipelineId) break;

        // Schedule early enough for the audio sample to reach the output device
        // when the corresponding video timestamp is displayed.
        const targetCtxTime =
          this.audioEpochCtxTime +
          (timestamp - this.audioEpochVideoTime) / this.playbackRate -
          this.getOutputLatencySeconds(ctx);

        // Skip if buffer is completely in the past
        const bufferDuration = duration / this.playbackRate;
        if (targetCtxTime + bufferDuration < ctx.currentTime) {
          continue;
        }

        const source = ctx.createBufferSource();
        source.buffer = buffer;
        source.playbackRate.value = this.playbackRate;
        source.connect(gain);

        if (targetCtxTime >= ctx.currentTime) {
          source.start(targetCtxTime);
        } else {
          const offset = (ctx.currentTime - targetCtxTime) * this.playbackRate;
          source.start(ctx.currentTime, offset);
        }

        this.nextScheduledTime = Math.max(this.nextScheduledTime, targetCtxTime + bufferDuration);
        if (this.pipelineId === currentPipelineId) {
          this.activeSources.add(source);
          source.onended = () => {
            this.activeSources.delete(source);
          };
        } else {
          try {
            source.stop();
            source.disconnect();
          } catch {
            // Ignore
          }
        }
      }
    } catch (err: unknown) {
      if (!signal.aborted && this.pipelineId === currentPipelineId) {
        console.warn('MkvAudioSyncEngine streaming error:', err);
        this.hasFatalError = true;
        this.stopAllSources();
        this.onError?.(err);
      }
    } finally {
      try {
        await generator.return();
      } catch {
        // Ignore
      }
      if (this.currentGenerator === generator) {
        this.currentGenerator = null;
      }
    }
  }

  private getOutputLatencySeconds(ctx: AudioContext): number {
    const outputLatency = Number.isFinite(ctx.outputLatency) ? ctx.outputLatency : undefined;
    const baseLatency = Number.isFinite(ctx.baseLatency) ? ctx.baseLatency : undefined;
    return Math.max(0, Math.min(
      outputLatency ?? baseLatency ?? 0,
      this.MAX_OUTPUT_LATENCY_SECONDS,
    ));
  }

  public destroy() {
    this.stopAllSources();
    this.stopVideoFrameSync();
    if (this.nativeSourceNode) {
      try {
        this.nativeSourceNode.disconnect();
      } catch {
        // Ignore
      }
      this.nativeSourceNode = null;
    }
    if (this.nativeGainNode) {
      try {
        this.nativeGainNode.disconnect();
      } catch {
        // Ignore
      }
      this.nativeGainNode = null;
    }
    if (this.gainNode) {
      try {
        this.gainNode.disconnect();
      } catch {
        // Ignore
      }
      this.gainNode = null;
    }
    if (this.audioCtx && this.audioCtx.state !== 'closed') {
      try {
        this.audioCtx.close();
      } catch {
        // Ignore
      }
    }
    this.boundElement = null;
    this.audioCtx = null;
    if (this.input) {
      try {
        this.input.dispose();
      } catch {
        // Ignore
      }
      this.input = null;
    }
  }
}
