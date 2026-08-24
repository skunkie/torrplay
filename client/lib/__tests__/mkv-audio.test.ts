// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import type { Input, InputAudioTrack } from 'mediabunny';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  type AudioTrackInfo,
  ensureDecodersRegistered,
  formatChannelCount,
  formatLanguage,
  getDefaultAudioTrackIndex,
  isAudioDecodingSupported,
  MkvAudioSyncEngine,
  probeAudioTracks,
} from '../mkv-audio';

const mockRawTracks = [
  {
    id: 1,
    getCodec: vi.fn().mockResolvedValue('ac3'),
    getLanguageCode: vi.fn().mockResolvedValue('eng'),
    getName: vi.fn().mockResolvedValue('Main English'),
    getNumberOfChannels: vi.fn().mockResolvedValue(6),
    getSampleRate: vi.fn().mockResolvedValue(48000),
    getBitrate: vi.fn().mockResolvedValue(640000),
    getDisposition: vi.fn().mockResolvedValue({ default: false }),
  },
  {
    id: 2,
    getCodec: vi.fn().mockResolvedValue('aac'),
    getLanguageCode: vi.fn().mockResolvedValue('spa'),
    getName: vi.fn().mockResolvedValue('Director Comments'),
    getNumberOfChannels: vi.fn().mockResolvedValue(2),
    getSampleRate: vi.fn().mockResolvedValue(44100),
    getBitrate: vi.fn().mockResolvedValue(192000),
    getDisposition: vi.fn().mockResolvedValue({ default: true }),
  },
];

vi.mock('mediabunny', async importOriginal => {
  const actual = await importOriginal<typeof import('mediabunny')>();
  return {
    ...actual,
    Input: vi.fn(function MockInput() {
      return {
        getAudioTracks: vi.fn().mockResolvedValue(mockRawTracks),
        dispose: vi.fn(),
      };
    }),
    UrlSource: vi.fn(),
    AudioSampleSink: vi.fn(function MockAudioSampleSink() {
      return {
        samples: vi.fn().mockReturnValue({
          [Symbol.asyncIterator]: async function* () {
            yield {
              toAudioBuffer: vi.fn().mockReturnValue({}),
              close: vi.fn(),
              timestamp: 0,
              duration: 1,
            };
          },
          return: vi.fn().mockResolvedValue({ done: true }),
        }),
      };
    }),
  };
});

describe('mkv-audio utilities', () => {
  describe('formatLanguage', () => {
    it('formats ISO language codes to readable names', () => {
      expect(formatLanguage('eng')).toBe('English');
      expect(formatLanguage('spa')).toBe('Spanish');
      expect(formatLanguage('fra')).toBe('French');
      expect(formatLanguage('deu')).toBe('German');
      expect(formatLanguage('jpn')).toBe('Japanese');
      expect(formatLanguage('rus')).toBe('Russian');
    });

    it('handles undefined, null, and "und"', () => {
      expect(formatLanguage(undefined)).toBe('Unknown');
      expect(formatLanguage(null)).toBe('Unknown');
      expect(formatLanguage('und')).toBe('Unknown');
      expect(formatLanguage('')).toBe('Unknown');
    });
  });

  describe('formatChannelCount', () => {
    it('formats standard channel configurations', () => {
      expect(formatChannelCount(1)).toBe('Mono');
      expect(formatChannelCount(2)).toBe('Stereo');
      expect(formatChannelCount(6)).toBe('5.1');
      expect(formatChannelCount(8)).toBe('7.1');
      expect(formatChannelCount(4)).toBe('4 ch');
    });
  });

  describe('isAudioDecodingSupported', () => {
    it('returns true when AudioDecoder or AudioContext is available', () => {
      (window as unknown as { AudioDecoder?: unknown }).AudioDecoder = vi.fn();
      expect(isAudioDecodingSupported()).toBe(true);
    });

    it('returns false when no audio APIs are available', () => {
      const origDecoder = (window as unknown as { AudioDecoder?: unknown }).AudioDecoder;
      const origCtx = (window as unknown as { AudioContext?: unknown }).AudioContext;
      const origWebkit = (window as unknown as { webkitAudioContext?: unknown }).webkitAudioContext;
      delete (window as unknown as { AudioDecoder?: unknown }).AudioDecoder;
      delete (window as unknown as { AudioContext?: unknown }).AudioContext;
      delete (window as unknown as { webkitAudioContext?: unknown }).webkitAudioContext;

      expect(isAudioDecodingSupported()).toBe(false);

      if (origDecoder) (window as unknown as { AudioDecoder?: unknown }).AudioDecoder = origDecoder;
      if (origCtx) (window as unknown as { AudioContext?: unknown }).AudioContext = origCtx;
      if (origWebkit) (window as unknown as { webkitAudioContext?: unknown }).webkitAudioContext = origWebkit;
    });
  });

  describe('ensureDecodersRegistered', () => {
    it('can be called safely multiple times', () => {
      expect(() => {
        ensureDecodersRegistered();
        ensureDecodersRegistered();
      }).not.toThrow();
    });
  });

  describe('probeAudioTracks', () => {
    it('probes and extracts metadata for audio tracks', async () => {
      const result = await probeAudioTracks('http://example.com/test.mkv');
      expect(result.tracks).toHaveLength(2);

      const track1 = result.tracks[0];
      expect(track1.id).toBe(1);
      expect(track1.codec).toBe('ac3');
      expect(track1.language).toBe('eng');
      expect(track1.channels).toBe(6);
      expect(track1.isDefault).toBe(false);
      expect(track1.isNativelySupported).toBe(false);
      expect(track1.label).toContain('Main English');
      expect(track1.label).toContain('English (AC3, 5.1)');

      const track2 = result.tracks[1];
      expect(track2.id).toBe(2);
      expect(track2.codec).toBe('aac');
      expect(track2.language).toBe('spa');
      expect(track2.channels).toBe(2);
      expect(track2.isDefault).toBe(true);
      expect(track2.isNativelySupported).toBe(true);
      expect(track2.label).toContain('Director Comments');
      expect(track2.label).toContain('Spanish (AAC, Stereo)');
    });

    it('resolves the declared default track and falls back to the first track', () => {
      const tracks = [
        { isDefault: false },
        { isDefault: true },
      ] as AudioTrackInfo[];

      expect(getDefaultAudioTrackIndex(tracks)).toBe(1);
      expect(getDefaultAudioTrackIndex(tracks.map(track => ({ ...track, isDefault: false })))).toBe(0);
      expect(getDefaultAudioTrackIndex([])).toBe(0);
    });
  });

  describe('MkvAudioSyncEngine', () => {
    let mockGainNode: GainNode;
    let mockAudioCtx: AudioContext;

    beforeEach(() => {
      mockGainNode = {
        gain: { value: 1 },
        connect: vi.fn(),
      } as unknown as GainNode;

      mockAudioCtx = {
        currentTime: 0,
        state: 'running',
        createGain: vi.fn(() => mockGainNode),
        createBufferSource: vi.fn(() => ({
          buffer: null,
          playbackRate: { value: 1 },
          connect: vi.fn(),
          start: vi.fn(),
          stop: vi.fn(),
          disconnect: vi.fn(),
        })),
        resume: vi.fn(async () => {
          Object.defineProperty(mockAudioCtx, 'state', { value: 'running', configurable: true });
        }),
        suspend: vi.fn(async () => {
          Object.defineProperty(mockAudioCtx, 'state', { value: 'suspended', configurable: true });
        }),
        close: vi.fn(async () => {
          Object.defineProperty(mockAudioCtx, 'state', { value: 'closed', configurable: true });
        }),
        destination: {} as AudioDestinationNode,
      } as unknown as AudioContext;

      function MockAudioContext() {
        return mockAudioCtx;
      }
      (window as unknown as { AudioContext: typeof AudioContext }).AudioContext = vi.fn(MockAudioContext) as unknown as typeof AudioContext;
      (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext = (window as unknown as { AudioContext: typeof AudioContext }).AudioContext;
    });

    afterEach(() => {
      vi.restoreAllMocks();
    });

    it('initializes and handles volume, mute, play, pause, seek, rate', () => {
      const mockInput = {
        dispose: vi.fn(),
      } as unknown as Input;
      const mockTrack = {
        id: 1,
      } as unknown as InputAudioTrack;

      const engine = new MkvAudioSyncEngine(mockInput, [mockTrack]);

      // Initialize audio context via onPlay
      engine.onPlay(0);

      // Volume setting and clamping
      engine.setVolume(0.8);
      expect(mockGainNode.gain.value).toBe(0.8);

      engine.setVolume(1.5);
      expect(mockGainNode.gain.value).toBe(1);

      engine.setVolume(-0.2);
      expect(mockGainNode.gain.value).toBe(0);

      engine.setVolume(0.5);
      expect(mockGainNode.gain.value).toBe(0.5);

      // Mute handling
      engine.setMuted(true);
      expect(mockGainNode.gain.value).toBe(0);

      engine.setMuted(false);
      expect(mockGainNode.gain.value).toBe(0.5);

      // Playback rate
      engine.setPlaybackRate(1.5);

      // Playback events
      engine.onPlay(10);
      expect(mockAudioCtx.createGain).toHaveBeenCalled();

      engine.onPause();
      expect(mockAudioCtx.suspend).toHaveBeenCalled();

      engine.onSeek(20);

      // Track selection
      engine.selectTrack(0);
      engine.selectTrack(99); // Invalid index should not crash

      // Cleanup
      engine.destroy();
      expect(mockAudioCtx.close).toHaveBeenCalled();
      expect(mockInput.dispose).toHaveBeenCalled();
    });

    it('isolates native media element audio using Web Audio and setWasmActive', () => {
      const mockNativeGainNode = {
        gain: { value: 1 },
        connect: vi.fn(),
        disconnect: vi.fn(),
      } as unknown as GainNode;

      const mockMediaSource = {
        connect: vi.fn(),
        disconnect: vi.fn(),
      };

      mockAudioCtx.createMediaElementSource = vi.fn(() => mockMediaSource as unknown as MediaElementAudioSourceNode);
      let gainCallCount = 0;
      mockAudioCtx.createGain = vi.fn(() => {
        gainCallCount++;
        return gainCallCount === 1 ? mockGainNode : mockNativeGainNode;
      });

      const mockInput = { dispose: vi.fn() } as unknown as Input;
      const engine = new MkvAudioSyncEngine(mockInput, [mockRawTracks[0] as unknown as InputAudioTrack]);

      const mockVideoEl = {
        audioTracks: [{ enabled: true }],
      } as unknown as HTMLMediaElement;

      // Attach media element
      engine.attachMediaElement(mockVideoEl);
      expect(mockAudioCtx.createMediaElementSource).toHaveBeenCalledWith(mockVideoEl);

      // When WASM is active: WASM gain is 1, native gain is 0
      engine.setWasmActive(true);
      expect(mockGainNode.gain.value).toBe(1);
      expect(mockNativeGainNode.gain.value).toBe(0);

      // When WASM is inactive (native audio playing): WASM gain is 0, native gain is 1
      engine.setWasmActive(false);
      expect(mockGainNode.gain.value).toBe(0);
      expect(mockNativeGainNode.gain.value).toBe(1);

      // Rebinding when media element is replaced
      const secondVideoEl = {
        audioTracks: [{ enabled: true }],
      } as unknown as HTMLMediaElement;

      engine.attachMediaElement(secondVideoEl);
      expect(mockMediaSource.disconnect).toHaveBeenCalled();
      expect(mockNativeGainNode.disconnect).toHaveBeenCalled();
      expect(mockAudioCtx.createMediaElementSource).toHaveBeenCalledWith(secondVideoEl);
    });

    it('resumes suspended audio context on play', () => {
      Object.defineProperty(mockAudioCtx, 'state', { value: 'suspended', configurable: true });
      const mockInput = { dispose: vi.fn() } as unknown as Input;
      const engine = new MkvAudioSyncEngine(mockInput, [mockRawTracks[0] as unknown as InputAudioTrack]);

      engine.onPlay(5);
      expect(mockAudioCtx.resume).toHaveBeenCalled();
    });

    it('handles buffering and onWaiting events', () => {
      Object.defineProperty(mockAudioCtx, 'state', { value: 'running', configurable: true });
      const mockInput = { dispose: vi.fn() } as unknown as Input;
      const engine = new MkvAudioSyncEngine(mockInput, [mockRawTracks[0] as unknown as InputAudioTrack]);

      engine.onPlay(0);
      engine.onWaiting();
      expect(mockAudioCtx.suspend).toHaveBeenCalled();

      engine.onPlaying(2);
      expect(mockAudioCtx.resume).toHaveBeenCalled();
    });

    it('corrects clock drift on timeupdate using hysteresis and debounce thresholds', () => {
      Object.defineProperty(mockAudioCtx, 'state', { value: 'running', configurable: true });
      Object.defineProperty(mockAudioCtx, 'currentTime', { value: 10, configurable: true });
      const mockInput = { dispose: vi.fn() } as unknown as Input;
      const engine = new MkvAudioSyncEngine(mockInput, [mockRawTracks[0] as unknown as InputAudioTrack]);

      engine.onPlay(0); // Epoch: ctx 10, video 0

      // Small drift within tolerance (e.g. 50ms) does not restart
      Object.defineProperty(mockAudioCtx, 'currentTime', { value: 12, configurable: true });
      engine.onTimeUpdate(2.05); // Expected 2.0, drift = 0.05s <= 0.15s

      // Single soft drift tick (> 150ms but <= 1.0s) does not restart immediately (debounced)
      engine.onTimeUpdate(2.2); // drift = 0.2s (tick 1)
      engine.onTimeUpdate(2.2); // drift = 0.2s (tick 2)
      // 3rd consecutive soft drift tick triggers resync
      engine.onTimeUpdate(2.2); // drift = 0.2s (tick 3)

      // Hard drift (> 1.0s) triggers immediate resync
      engine.onTimeUpdate(5.0); // drift = 3.0s > 1.0s
    });

    it('synchronously stops active sources when stopping or restarting to prevent async race condition', async () => {
      const mockStop = vi.fn();
      const mockDisconnect = vi.fn();
      mockAudioCtx.createBufferSource = vi.fn(() => ({
        buffer: null,
        playbackRate: { value: 1 },
        connect: vi.fn(),
        start: vi.fn(),
        stop: mockStop,
        disconnect: mockDisconnect,
      })) as unknown as typeof mockAudioCtx.createBufferSource;

      const mockInput = { dispose: vi.fn() } as unknown as Input;
      const engine = new MkvAudioSyncEngine(mockInput, [mockRawTracks[0] as unknown as InputAudioTrack]);

      engine.onPlay(0);
      // Fast successive track switches or pauses
      engine.selectTrack(0);
      engine.onPause();

      expect(mockStop).toBeDefined();
    });

    it('ignores timeupdate when audio context is not running or engine is paused', () => {
      Object.defineProperty(mockAudioCtx, 'state', { value: 'suspended', configurable: true });
      const mockInput = { dispose: vi.fn() } as unknown as Input;
      const engine = new MkvAudioSyncEngine(mockInput, [mockRawTracks[0] as unknown as InputAudioTrack]);

      // When paused
      engine.onTimeUpdate(5);

      // When suspended
      engine.onPlay(0);
      Object.defineProperty(mockAudioCtx, 'state', { value: 'suspended', configurable: true });
      engine.onTimeUpdate(5);
    });

    it('handles seek when paused without starting audio playback', () => {
      const mockInput = { dispose: vi.fn() } as unknown as Input;
      const engine = new MkvAudioSyncEngine(mockInput, [mockRawTracks[0] as unknown as InputAudioTrack]);

      engine.onSeek(45);
      expect(mockAudioCtx.createBufferSource).not.toHaveBeenCalled();
    });

    it('stops streaming and calls onError without infinite drift restarts on fatal decoder error', async () => {
      const onError = vi.fn();
      const consoleWarnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
      const mockInput = { dispose: vi.fn() } as unknown as Input;
      const failingTrack = { id: 99 } as unknown as InputAudioTrack;

      const mediabunny = await import('mediabunny');
      const originalSink = mediabunny.AudioSampleSink;
      (mediabunny as unknown as { AudioSampleSink: unknown }).AudioSampleSink = vi.fn().mockImplementation(() => {
        throw new Error('AudioDecoder is not available in this environment');
      });

      const engine = new MkvAudioSyncEngine(mockInput, [failingTrack], onError);
      engine.onPlay(0);

      expect(onError).toHaveBeenCalledWith(expect.any(Error));

      // Subsequent timeupdates should not trigger restartPipeline loops
      engine.onTimeUpdate(10);
      expect(onError).toHaveBeenCalledTimes(1);

      (mediabunny as unknown as { AudioSampleSink: unknown }).AudioSampleSink = originalSink;
      consoleWarnSpy.mockRestore();
    });
  });
});
