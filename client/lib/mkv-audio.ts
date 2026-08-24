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
  private consecutiveDriftCount: number = 0;

  // Sync parameters
  private readonly LOOKAHEAD_SECONDS = 1.0;
  private readonly SCHEDULE_INTERVAL_MS = 100;
  private readonly DRIFT_TOLERANCE_SECONDS = 0.15; // 150ms drift threshold for soft drift
  private readonly CONSECUTIVE_DRIFT_TICKS = 3; // require 3 consecutive drifted ticks before resync
  private readonly HARD_DRIFT_THRESHOLD = 1.0; // 1s drift threshold for immediate resync

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
            at[i].enabled = !this.isWasmActive;
          }
        }
      } catch {
        // audioTracks API might be read-only or unsupported in some browsers
      }
    }
  }

  public setWasmActive(active: boolean) {
    this.isWasmActive = active;
    if (!active) {
      this.stopAllSources();
    }
    this.updateGains();
    this.syncAudioTracks();
  }

  /**
   * Attaches the native HTMLMediaElement to the Web Audio graph to isolate and silence
   * its native audio output when WASM audio decoding is active.
   * If the underlying element changes (e.g. provider re-mount), it cleans up and re-attaches.
   */
  public attachMediaElement(videoEl: HTMLMediaElement) {
    if (typeof window === 'undefined') return;

    if (this.boundElement !== videoEl) {
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

      try {
        const ctx = this.initAudioContext();
        if (typeof ctx.createMediaElementSource === 'function') {
          this.nativeSourceNode = ctx.createMediaElementSource(videoEl);
          this.nativeGainNode = ctx.createGain();
          this.nativeSourceNode.connect(this.nativeGainNode);
          this.nativeGainNode.connect(ctx.destination);
          this.updateGains();
        }
      } catch {
        // createMediaElementSource can throw if element is already connected elsewhere
      }
    }

    this.syncAudioTracks();
  }

  public selectTrack(trackIndex: number) {
    if (trackIndex < 0 || trackIndex >= this.rawTracks.length) return;
    this.hasFatalError = false;
    this.selectedTrackIndex = trackIndex;
    this.isWasmActive = true;
    this.updateGains();
    this.restartPipeline(this.lastKnownVideoTime);
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

    // Expected video playback position based on Web Audio clock
    const expectedVideoTime =
      this.audioEpochVideoTime +
      (ctx.currentTime - this.audioEpochCtxTime) * this.playbackRate;

    const drift = Math.abs(videoTime - expectedVideoTime);

    if (drift > this.HARD_DRIFT_THRESHOLD) {
      this.consecutiveDriftCount = 0;
      this.restartPipeline(videoTime);
    } else if (drift > this.DRIFT_TOLERANCE_SECONDS) {
      this.consecutiveDriftCount++;
      if (this.consecutiveDriftCount >= this.CONSECUTIVE_DRIFT_TICKS) {
        this.consecutiveDriftCount = 0;
        this.restartPipeline(videoTime);
      }
    } else {
      this.consecutiveDriftCount = 0;
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

  private restartPipeline(startTime: number) {
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
    this.audioEpochCtxTime = ctx.currentTime;
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

        const targetCtxTime =
          this.audioEpochCtxTime +
          (timestamp - this.audioEpochVideoTime) / this.playbackRate;

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

  public destroy() {
    this.stopAllSources();
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
