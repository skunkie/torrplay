// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { type AudioTrackInfo } from './mkv-audio';

export const DEMO_TORRENT_AUDIO_TRACKS: Record<string, AudioTrackInfo[]> = {
  // Sintel
  '08ada5a7a6183aae1e09d831df6748d566095a10': [
    {
      id: 1,
      index: 0,
      name: 'Original 5.1',
      language: 'eng',
      codec: 'ac3',
      channels: 6,
      sampleRate: 48000,
      bitrate: 640000,
      isDefault: true,
      isNativelySupported: false,
      label: 'Original 5.1 - English (AC3, 5.1)',
    },
    {
      id: 2,
      index: 1,
      name: 'Stereo Mix',
      language: 'eng',
      codec: 'aac',
      channels: 2,
      sampleRate: 48000,
      bitrate: 192000,
      isDefault: false,
      isNativelySupported: true,
      label: 'Stereo Mix - English (AAC, Stereo)',
    },
    {
      id: 3,
      index: 2,
      name: 'Director Commentary',
      language: 'eng',
      codec: 'aac',
      channels: 2,
      sampleRate: 44100,
      bitrate: 128000,
      isDefault: false,
      isNativelySupported: true,
      label: 'Director Commentary - English (AAC, Stereo)',
    },
  ],

  // Big Buck Bunny
  'dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c': [
    {
      id: 1,
      index: 0,
      name: 'Surround 5.1',
      language: 'und',
      codec: 'ac3',
      channels: 6,
      sampleRate: 48000,
      bitrate: 640000,
      isDefault: true,
      isNativelySupported: false,
      label: 'Surround 5.1 - Unknown (AC3, 5.1)',
    },
    {
      id: 2,
      index: 1,
      name: 'Stereo Soundtrack',
      language: 'und',
      codec: 'aac',
      channels: 2,
      sampleRate: 48000,
      bitrate: 192000,
      isDefault: false,
      isNativelySupported: true,
      label: 'Stereo Soundtrack - Unknown (AAC, Stereo)',
    },
    {
      id: 3,
      index: 2,
      name: 'Isolated Score',
      language: 'und',
      codec: 'flac',
      channels: 2,
      sampleRate: 48000,
      bitrate: 512000,
      isDefault: false,
      isNativelySupported: true,
      label: 'Isolated Score - Unknown (FLAC, Stereo)',
    },
  ],

  // Cosmos Laundromat
  'c9e15763f722f23e98a29decdfae341b98d53056': [
    {
      id: 1,
      index: 0,
      name: 'Surround 5.1',
      language: 'eng',
      codec: 'ac3',
      channels: 6,
      sampleRate: 48000,
      bitrate: 640000,
      isDefault: true,
      isNativelySupported: false,
      label: 'Surround 5.1 - English (AC3, 5.1)',
    },
    {
      id: 2,
      index: 1,
      name: 'Stereo Mix',
      language: 'eng',
      codec: 'aac',
      channels: 2,
      sampleRate: 48000,
      bitrate: 192000,
      isDefault: false,
      isNativelySupported: true,
      label: 'Stereo Mix - English (AAC, Stereo)',
    },
    {
      id: 3,
      index: 2,
      name: 'French Dub',
      language: 'fra',
      codec: 'aac',
      channels: 2,
      sampleRate: 48000,
      bitrate: 192000,
      isDefault: false,
      isNativelySupported: true,
      label: 'French Dub - French (AAC, Stereo)',
    },
    {
      id: 4,
      index: 3,
      name: 'Director Commentary',
      language: 'eng',
      codec: 'aac',
      channels: 2,
      sampleRate: 44100,
      bitrate: 128000,
      isDefault: false,
      isNativelySupported: true,
      label: 'Director Commentary - English (AAC, Stereo)',
    },
  ],

  // Tears of Steel
  '209c8226b299b308beaf2b9cd3fb49212dbd13ec': [
    {
      id: 1,
      index: 0,
      name: 'Surround 5.1',
      language: 'eng',
      codec: 'dts',
      channels: 6,
      sampleRate: 48000,
      bitrate: 1536000,
      isDefault: true,
      isNativelySupported: false,
      label: 'Surround 5.1 - English (DTS, 5.1)',
    },
    {
      id: 2,
      index: 1,
      name: 'Stereo Mix',
      language: 'eng',
      codec: 'opus',
      channels: 2,
      sampleRate: 48000,
      bitrate: 160000,
      isDefault: false,
      isNativelySupported: true,
      label: 'Stereo Mix - English (OPUS, Stereo)',
    },
    {
      id: 3,
      index: 2,
      name: 'VFX & Director Commentary',
      language: 'eng',
      codec: 'vorbis',
      channels: 2,
      sampleRate: 48000,
      bitrate: 128000,
      isDefault: false,
      isNativelySupported: true,
      label: 'VFX & Director Commentary - English (VORBIS, Stereo)',
    },
  ],
};

/**
 * Gets real audio tracks for demo torrents by hash, title or filename.
 */
export function getDemoAudioTracks(identifier?: string): AudioTrackInfo[] {
  if (!identifier) return DEMO_TORRENT_AUDIO_TRACKS['08ada5a7a6183aae1e09d831df6748d566095a10'];

  const lower = identifier.toLowerCase();

  // Match by hash
  if (DEMO_TORRENT_AUDIO_TRACKS[lower]) {
    return DEMO_TORRENT_AUDIO_TRACKS[lower];
  }

  // Match by title/filename
  if (lower.includes('sintel')) {
    return DEMO_TORRENT_AUDIO_TRACKS['08ada5a7a6183aae1e09d831df6748d566095a10'];
  }
  if (lower.includes('bunny') || lower.includes('big buck')) {
    return DEMO_TORRENT_AUDIO_TRACKS['dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c'];
  }
  if (lower.includes('cosmos') || lower.includes('laundromat')) {
    return DEMO_TORRENT_AUDIO_TRACKS['c9e15763f722f23e98a29decdfae341b98d53056'];
  }
  if (lower.includes('steel') || lower.includes('tears')) {
    return DEMO_TORRENT_AUDIO_TRACKS['209c8226b299b308beaf2b9cd3fb49212dbd13ec'];
  }

  return DEMO_TORRENT_AUDIO_TRACKS['08ada5a7a6183aae1e09d831df6748d566095a10'];
}
