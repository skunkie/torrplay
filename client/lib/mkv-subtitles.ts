// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { type SubtitleTrackInfo } from './video-utils';

export interface EmbeddedSubtitleTrack {
  trackNumber: number,
  uid?: number,
  codecId: string,
  language: string,
  name: string,
  isDefault: boolean,
  isForced: boolean,
  unavailableReason?: string,
  label: string
}

export interface SubtitleCue {
  startTime: number, // in seconds
  endTime: number, // in seconds
  text: string
}

// Matroska / EBML Element IDs
const ID_EBML = 0x1a45dfa3;
const ID_SEGMENT = 0x18538067;
const ID_INFO = 0x1549a966;
const ID_TIMESTAMP_SCALE = 0x2ad7b1;
const ID_TRACKS = 0x1654ae6b;
const ID_TRACK_ENTRY = 0xae;
const ID_TRACK_NUMBER = 0xd7;
const ID_TRACK_UID = 0x73c5;
const ID_TRACK_TYPE = 0x83;
const ID_FLAG_DEFAULT = 0x88;
const ID_FLAG_FORCED = 0x55aa;
const ID_CODEC_ID = 0x86;
const ID_NAME = 0x536e;
const ID_LANGUAGE = 0x22b59c; // Language (ISO 639-2)
const ID_LANGUAGE_IETF = 0x63a5; // LanguageBCP47
const ID_CONTENT_ENCODINGS = 0x6d80;

const ID_CLUSTER = 0x1f43b675;
const ID_CLUSTER_TIMESTAMP = 0xe7;
const ID_SIMPLE_BLOCK = 0xa3;
const ID_BLOCK_GROUP = 0xa0;
const ID_BLOCK = 0xa1;
const ID_BLOCK_DURATION = 0x9b;
const ID_SEEK_HEAD = 0x114d9b74;
const ID_SEEK = 0x4dbb;
const ID_SEEK_ID = 0x53ab;
const ID_SEEK_POSITION = 0x53ac;
const ID_CUES = 0x1c53bb6b;
const ID_CUE_POINT = 0xbb;
const ID_CUE_TIME = 0xb3;
const ID_CUE_TRACK_POSITIONS = 0xb7;
const ID_CUE_CLUSTER_POSITION = 0xf1;

const TRACK_TYPE_SUBTITLE = 17; // 0x11
const TEXT_SUBTITLE_CODECS = new Set([
  'S_TEXT/UTF8',
  'S_TEXT/ASS',
  'S_TEXT/SSA',
  'S_TEXT/WEBVTT',
  'D_WEBVTT/SUBTITLES',
  'D_WEBVTT/CAPTIONS',
  'D_WEBVTT/DESCRIPTIONS',
]);

/**
 * Reads an EBML variable-length integer (VINT) element ID.
 */
function readElementId(
  buf: Uint8Array,
  offset: number,
  limit: number
): { id: number, length: number } | null {
  if (offset >= limit) return null;
  const first = buf[offset];
  if (first === 0) return null;

  let length = 1;
  let mask = 0x80;
  while ((first & mask) === 0 && length <= 4) {
    length++;
    mask >>= 1;
  }

  if (length > 4 || offset + length > limit) return null;

  let id = 0;
  for (let i = 0; i < length; i++) {
    id = (id << 8) | buf[offset + i];
  }

  return { id: id >>> 0, length };
}

/**
 * Reads an EBML variable-length integer (VINT) element data size.
 */
function readElementSize(
  buf: Uint8Array,
  offset: number,
  limit: number
): { size: number, length: number } | null {
  if (offset >= limit) return null;
  const first = buf[offset];
  if (first === 0) return null;

  let length = 1;
  let mask = 0x80;
  while ((first & mask) === 0 && length <= 8) {
    length++;
    mask >>= 1;
  }

  if (length > 8 || offset + length > limit) return null;

  let size = first & (mask - 1);
  for (let i = 1; i < length; i++) {
    size = size * 256 + buf[offset + i];
  }

  return { size, length };
}

/**
 * Reads an unsigned integer of specified byte length.
 */
function readUInt(buf: Uint8Array, offset: number, length: number): number {
  if (length <= 6) {
    let val = 0;
    for (let i = 0; i < length; i++) {
      val = val * 256 + buf[offset + i];
    }
    return val;
  }
  let bigVal = BigInt(0);
  for (let i = 0; i < length; i++) {
    bigVal = (bigVal << BigInt(8)) | BigInt(buf[offset + i]);
  }
  return Number(bigVal & BigInt('0x1fffffffffffff'));
}

/**
 * Reads a UTF-8 or ASCII string.
 */
function readString(buf: Uint8Array, offset: number, length: number): string {
  const bytes = buf.subarray(offset, offset + length);
  const text = new TextDecoder('utf-8').decode(bytes);
  return text.replace(/\0+$/, '').trim();
}

/**
 * Format language code into human readable language name.
 */
function formatLanguage(langCode: string): string {
  if (!langCode || langCode === 'und') return 'Unknown';
  try {
    const displayNames = new Intl.DisplayNames(['en'], { type: 'language' });
    const name = displayNames.of(langCode);
    if (name && name.toLowerCase() !== langCode.toLowerCase()) {
      return name;
    }
  } catch {
    // ignore
  }
  return langCode.toUpperCase();
}

/**
 * Parses Matroska TrackEntry element for subtitle metadata.
 */
function parseTrackEntry(
  buf: Uint8Array,
  offset: number,
  limit: number
): EmbeddedSubtitleTrack | null {
  let pos = offset;
  let trackNumber = 0;
  let trackType = 0;
  let trackUid: number | undefined;
  let codecId = '';
  let language = 'und';
  let name = '';
  let isDefault = true;
  let isForced = false;
  let hasContentEncodings = false;

  while (pos < limit) {
    const idInfo = readElementId(buf, pos, limit);
    if (!idInfo) break;
    pos += idInfo.length;

    const sizeInfo = readElementSize(buf, pos, limit);
    if (!sizeInfo) break;
    pos += sizeInfo.length;

    const elemSize = sizeInfo.size;
    const elemEnd = sizeInfo.size === 2 ** (7 * sizeInfo.length) - 1 ? limit : Math.min(pos + elemSize, limit);

    switch (idInfo.id) {
      case ID_TRACK_NUMBER:
        trackNumber = readUInt(buf, pos, elemSize);
        break;
      case ID_TRACK_UID:
        trackUid = readUInt(buf, pos, elemSize);
        break;
      case ID_TRACK_TYPE:
        trackType = readUInt(buf, pos, elemSize);
        break;
      case ID_CODEC_ID:
        codecId = readString(buf, pos, elemSize);
        break;
      case ID_NAME:
        name = readString(buf, pos, elemSize);
        break;
      case ID_LANGUAGE:
      case ID_LANGUAGE_IETF:
        language = readString(buf, pos, elemSize);
        break;
      case ID_FLAG_DEFAULT:
        isDefault = readUInt(buf, pos, elemSize) === 1;
        break;
      case ID_FLAG_FORCED:
        isForced = readUInt(buf, pos, elemSize) === 1;
        break;
      case ID_CONTENT_ENCODINGS:
        hasContentEncodings = true;
        break;
    }

    pos = elemEnd;
  }

  if (trackType !== TRACK_TYPE_SUBTITLE || !trackNumber) {
    return null;
  }

  let formatName = 'Embedded';
  const codecLower = codecId.toLowerCase();
  if (codecLower.includes('utf8') || codecLower.includes('srt')) {
    formatName = 'Embedded SRT';
  } else if (codecLower.includes('ass')) {
    formatName = 'Embedded ASS';
  } else if (codecLower.includes('ssa')) {
    formatName = 'Embedded SSA';
  } else if (codecLower.includes('vtt') || codecLower.includes('webvtt')) {
    formatName = 'Embedded VTT';
  } else if (codecLower.includes('pgs') || codecLower.includes('hdmv')) {
    formatName = 'Embedded PGS';
  } else if (codecLower.includes('vobsub')) {
    formatName = 'Embedded VobSub';
  }

  const langFormatted = formatLanguage(language);
  const modifiers: string[] = [];
  if (isForced) modifiers.push('FORCED');
  if (name && name.toLowerCase() !== langFormatted.toLowerCase()) {
    modifiers.push(name);
  }

  const modifierSuffix = modifiers.length > 0 ? ` [${modifiers.join(', ')}]` : '';
  const label = `${langFormatted}${modifierSuffix} (${formatName})`;

  return {
    trackNumber,
    uid: trackUid,
    codecId,
    language,
    name,
    isDefault,
    isForced,
    unavailableReason: !TEXT_SUBTITLE_CODECS.has(codecId)
      ? 'This subtitle format is not supported in this player. Use an external player or an SRT/VTT file.'
      : hasContentEncodings
        ? 'Encoded subtitle tracks are not supported in this player. Use an external player or an SRT/VTT file.'
        : undefined,
    label,
  };
}

/**
 * Parses EBML tracks from buffer.
 */
export function parseMatroskaSubtitleTracks(buffer: Uint8Array): EmbeddedSubtitleTrack[] {
  let pos = 0;
  const limit = buffer.length;
  const tracks: EmbeddedSubtitleTrack[] = [];

  while (pos < limit) {
    const idInfo = readElementId(buffer, pos, limit);
    if (!idInfo) {
      pos++;
      continue;
    }
    pos += idInfo.length;

    const sizeInfo = readElementSize(buffer, pos, limit);
    if (!sizeInfo) break;
    pos += sizeInfo.length;

    const elemSize = sizeInfo.size;
    const elemEnd = sizeInfo.size === 2 ** (7 * sizeInfo.length) - 1 ? limit : Math.min(pos + elemSize, limit);

    if (idInfo.id === ID_EBML || idInfo.id === ID_SEGMENT) {
      // Container elements - descend into them
      continue;
    } else if (idInfo.id === ID_TRACKS) {
      // Tracks container - parse TrackEntry elements
      let trackPos = pos;
      while (trackPos < elemEnd) {
        const entryId = readElementId(buffer, trackPos, elemEnd);
        if (!entryId) break;
        trackPos += entryId.length;

        const entrySize = readElementSize(buffer, trackPos, elemEnd);
        if (!entrySize) break;
        trackPos += entrySize.length;

        const entryEnd = Math.min(trackPos + entrySize.size, elemEnd);

        if (entryId.id === ID_TRACK_ENTRY) {
          const track = parseTrackEntry(buffer, trackPos, entryEnd);
          if (track) {
            tracks.push(track);
          }
        }
        trackPos = entryEnd;
      }
      break; // Found tracks, we can stop
    } else if (idInfo.id === ID_CLUSTER) {
      // Clusters reached, tracks header was already passed
      break;
    }

    pos = elemEnd;
  }

  return tracks;
}

/**
 * Format seconds to WebVTT timestamp: 00:00:00.000
 */
export function formatVttTimestamp(seconds: number): string {
  const safeSeconds = Math.max(0, seconds);
  const hrs = Math.floor(safeSeconds / 3600);
  const mins = Math.floor((safeSeconds % 3600) / 60);
  const secs = Math.floor(safeSeconds % 60);
  const ms = Math.floor((safeSeconds % 1) * 1000);

  const pad = (n: number, z = 2) => String(n).padStart(z, '0');
  return `${pad(hrs)}:${pad(mins)}:${pad(secs)}.${pad(ms, 3)}`;
}

/**
 * Cleans ASS / SSA dialogue text to clean readable subtitle text.
 */
export function cleanAssDialogueText(rawText: string): string {
  let text = rawText;
  // ASS dialogue line is usually: ReadOrder, Layer, Style, Name, MarginL, MarginR, MarginV, Effect, Text
  // e.g. 0,0,Default,,0,0,0,,Hello world!
  const parts = rawText.split(',');
  if (parts.length >= 9) {
    text = parts.slice(8).join(',');
  }

  // Strip ASS override tags {\...}
  text = text.replace(/\{[^\}]*\}/g, '');
  // Replace ASS hard linebreaks \N, \n, \h
  text = text.replace(/\\N/gi, '\n').replace(/\\n/gi, '\n').replace(/\\h/gi, ' ');
  return text.trim();
}

/**
 * Converts a list of SubtitleCue objects to a standard WebVTT string.
 */
export function cuesToWebVtt(cues: SubtitleCue[]): string {
  let vtt = 'WEBVTT\n\n';
  const sorted = [...cues].sort((a, b) => a.startTime - b.startTime);

  for (let i = 0; i < sorted.length; i++) {
    const cue = sorted[i];
    const start = formatVttTimestamp(cue.startTime);
    const end = formatVttTimestamp(cue.endTime);
    vtt += `${i + 1}\n${start} --> ${end}\n${cue.text}\n\n`;
  }

  return vtt;
}

/**
 * Parses Clusters and Block elements from a Matroska stream buffer for a specific track.
 */
export function parseMatroskaSubtitleCues(
  buffer: Uint8Array,
  targetTrackNumber: number,
  timecodeScaleNs = 1000000, // default 1ms
  codecId = 'S_TEXT/UTF8'
): SubtitleCue[] {
  let pos = 0;
  const limit = buffer.length;
  const cues: SubtitleCue[] = [];
  let currentClusterTimestamp = 0;
  let timecodeScaleSec = timecodeScaleNs / 1000000000;
  const decodeText = (text: string) => codecId === 'S_TEXT/ASS' || codecId === 'S_TEXT/SSA'
    ? cleanAssDialogueText(text) : text;

  while (pos < limit) {
    const idInfo = readElementId(buffer, pos, limit);
    if (!idInfo) {
      pos++;
      continue;
    }
    pos += idInfo.length;

    const sizeInfo = readElementSize(buffer, pos, limit);
    if (!sizeInfo) break;
    pos += sizeInfo.length;

    const elemSize = sizeInfo.size;
    const elemEnd = sizeInfo.size === 2 ** (7 * sizeInfo.length) - 1 ? limit : Math.min(pos + elemSize, limit);

    if (idInfo.id === ID_EBML || idInfo.id === ID_SEGMENT) {
      continue;
    } else if (idInfo.id === ID_INFO) {
      // Check for timestamp scale
      let infoPos = pos;
      while (infoPos < elemEnd) {
        const subId = readElementId(buffer, infoPos, elemEnd);
        if (!subId) break;
        infoPos += subId.length;
        const subSize = readElementSize(buffer, infoPos, elemEnd);
        if (!subSize) break;
        infoPos += subSize.length;
        if (subId.id === ID_TIMESTAMP_SCALE) {
          timecodeScaleNs = readUInt(buffer, infoPos, subSize.size);
          timecodeScaleSec = timecodeScaleNs / 1000000000;
        }
        infoPos += subSize.size;
      }
    } else if (idInfo.id === ID_CLUSTER) {
      let clusterPos = pos;
      while (clusterPos < elemEnd) {
        const clusterElemId = readElementId(buffer, clusterPos, elemEnd);
        if (!clusterElemId) break;
        clusterPos += clusterElemId.length;

        const clusterElemSize = readElementSize(buffer, clusterPos, elemEnd);
        if (!clusterElemSize) break;
        clusterPos += clusterElemSize.length;

        const clusterElemEnd = Math.min(clusterPos + clusterElemSize.size, elemEnd);

        if (clusterElemId.id === ID_CLUSTER_TIMESTAMP) {
          currentClusterTimestamp = readUInt(buffer, clusterPos, clusterElemSize.size);
        } else if (clusterElemId.id === ID_SIMPLE_BLOCK || clusterElemId.id === ID_BLOCK) {
          // Parse Block Header
          // Track number is VINT
          const trackVint = readElementSize(buffer, clusterPos, clusterElemEnd);
          if (trackVint) {
            const trackNum = trackVint.size;
            let blockOffset = clusterPos + trackVint.length;
            if (blockOffset + 3 <= clusterElemEnd && trackNum === targetTrackNumber) {
              // Read signed int16 relative timestamp (big endian)
              const relTime = (buffer[blockOffset] << 8) | buffer[blockOffset + 1];
              const signedRelTime = relTime > 0x7fff ? relTime - 0x10000 : relTime;
              blockOffset += 3; // skip timestamp (2) + flags (1)

              const textPayload = readString(buffer, blockOffset, clusterElemEnd - blockOffset);
              const cleanedText = decodeText(textPayload);

              if (cleanedText) {
                const startSec = (currentClusterTimestamp + signedRelTime) * timecodeScaleSec;
                // Default duration of 3 seconds if not explicit
                const endSec = startSec + 3.0;
                cues.push({
                  startTime: Math.max(0, startSec),
                  endTime: endSec,
                  text: cleanedText,
                });
              }
            }
          }
        } else if (clusterElemId.id === ID_BLOCK_GROUP) {
          // Parse BlockGroup for Block + BlockDuration
          let groupPos = clusterPos;
          let blockData: { trackNum: number, relTime: number, text: string } | null = null;
          let durationMs: number | null = null;

          while (groupPos < clusterElemEnd) {
            const groupElemId = readElementId(buffer, groupPos, clusterElemEnd);
            if (!groupElemId) break;
            groupPos += groupElemId.length;
            const groupElemSize = readElementSize(buffer, groupPos, clusterElemEnd);
            if (!groupElemSize) break;
            groupPos += groupElemSize.length;
            const groupElemEnd = Math.min(groupPos + groupElemSize.size, clusterElemEnd);

            if (groupElemId.id === ID_BLOCK) {
              const trackVint = readElementSize(buffer, groupPos, groupElemEnd);
              if (trackVint && trackVint.size === targetTrackNumber) {
                const blockOff = groupPos + trackVint.length;
                if (blockOff + 3 <= groupElemEnd) {
                  const relTime = (buffer[blockOff] << 8) | buffer[blockOff + 1];
                  const signedRelTime = relTime > 0x7fff ? relTime - 0x10000 : relTime;
                  const textPayload = readString(buffer, blockOff + 3, groupElemEnd - (blockOff + 3));
                  blockData = {
                    trackNum: trackVint.size,
                    relTime: signedRelTime,
                    text: decodeText(textPayload),
                  };
                }
              }
            } else if (groupElemId.id === ID_BLOCK_DURATION) {
              durationMs = readUInt(buffer, groupPos, groupElemSize.size);
            }
            groupPos = groupElemEnd;
          }

          if (blockData && blockData.text) {
            const startSec = (currentClusterTimestamp + blockData.relTime) * timecodeScaleSec;
            const durationSec = durationMs !== null ? durationMs * timecodeScaleSec : 3.0;
            cues.push({
              startTime: Math.max(0, startSec),
              endTime: startSec + durationSec,
              text: blockData.text,
            });
          }
        }

        clusterPos = clusterElemEnd;
      }
    }

    pos = elemEnd;
  }

  return cues;
}

/**
 * Inspects a media stream URL by fetching the container header and extracts embedded subtitle track metadata.
 */
export async function probeEmbeddedSubtitleTracks(
  streamUrl: string,
  fetchFn: typeof fetch = fetch,
  signal?: AbortSignal,
  cache?: SubtitleSourceCache
): Promise<SubtitleTrackInfo[]> {
  try {
    const reader = new SubtitleRangeReader(streamUrl, fetchFn, signal, cache);
    const bytes = await reader.read(0, 256 * 1024);
    const embeddedTracks = parseMatroskaSubtitleTracks(bytes);

    return embeddedTracks.map(track => ({
      id: `embedded:${track.trackNumber}:${track.codecId}`,
      // Embedded tracks have no fetchable text source until they are demuxed.
      src: '',
      embeddedTrackNumber: track.trackNumber,
      unavailableReason: track.unavailableReason,
      label: track.label,
      language: track.language,
      type: 'vtt',
      kind: 'subtitles',
      default: track.isDefault && !track.isForced && !track.unavailableReason,
    }));
  } catch (err) {
    if (err instanceof Error && err.name === 'AbortError') {
      return [];
    }
    console.warn('Failed to probe embedded subtitle tracks:', err);
    return [];
  }
}

interface RemoteElement {
  id: number,
  offset: number,
  dataOffset: number,
  end: number,
  unknownSize: boolean
}

// Keep media reads bounded, including when a server ignores Range requests.
const RANGE_BYTES = 64 * 1024;
const MAX_SUBTITLE_ELEMENT_BYTES = 1024 * 1024;
const PLAYBACK_RANGE_BYTES = 512 * 1024;
export const SUBTITLE_LOOK_AHEAD_SECONDS = 30;
export const SUBTITLE_SEEK_PREROLL_SECONDS = 30;

interface SubtitleMetadata {
  segmentOffset: number,
  firstCluster: number,
  timecodeScaleNs: number,
  tracks: EmbeddedSubtitleTrack[],
  cuesOffset?: number,
  seekHeads: number[],
  index?: { time: number, offset: number }[]
}

/** Per-source metadata and a bounded 2 MiB range cache, reused across selections and seeks. */
export class SubtitleSourceCache {
  fileSize = Infinity;
  metadata?: SubtitleMetadata;
  private ranges: { offset: number, bytes: Uint8Array }[] = [];

  constructor(readonly url: string) {}

  get(offset: number, end: number) {
    const range = this.ranges.find(range => offset >= range.offset && end <= range.offset + range.bytes.length);
    return range?.bytes.subarray(offset - range.offset, end - range.offset);
  }

  put(offset: number, bytes: Uint8Array) {
    if (bytes.length > 4 * PLAYBACK_RANGE_BYTES) return;
    this.ranges.unshift({ offset, bytes });
    let size = this.ranges.reduce((size, range) => size + range.bytes.length, 0);
    while (size > 4 * PLAYBACK_RANGE_BYTES) size -= this.ranges.pop()!.bytes.length;
  }
}

export interface SubtitlePlaybackOptions {
  cache: SubtitleSourceCache,
  currentTime: () => number,
  waitForTimeChange: (signal?: AbortSignal) => Promise<void>,
  onWait?: () => void
}

class SubtitleRangeReader {
  private buffer: Uint8Array = new Uint8Array();
  private bufferOffset = 0;
  private fileSize = Infinity;

  constructor(
    private url: string,
    private fetchFn: typeof fetch,
    private signal?: AbortSignal,
    private cache?: SubtitleSourceCache,
    private rangeBytes = RANGE_BYTES
  ) {
    if (cache && cache.url !== url) throw new Error('Subtitle cache belongs to a different source');
    this.fileSize = cache?.fileSize ?? Infinity;
  }

  async read(offset: number, length: number): Promise<Uint8Array> {
    this.signal?.throwIfAborted();
    if (offset >= this.fileSize) return new Uint8Array();
    const end = Math.min(offset + length, this.fileSize);
    const cached = this.cache?.get(offset, end);
    if (cached) return cached;
    if (offset >= this.bufferOffset && end <= this.bufferOffset + this.buffer.length) {
      return this.buffer.subarray(offset - this.bufferOffset, end - this.bufferOffset);
    }
    const requestedEnd = Math.min(offset + Math.max(length, this.rangeBytes), this.fileSize) - 1;
    // Call fetch as a function, not a reader method: native fetch rejects this receiver.
    const fetchFn = this.fetchFn;
    const response = await fetchFn(this.url, {
      headers: { Range: `bytes=${offset}-${requestedEnd}` },
      signal: this.signal,
    });
    if (response.status === 416) {
      const size = response.headers.get('Content-Range')?.match(/^bytes \*\/(\d+)$/);
      if (size && offset >= Number(size[1])) {
        this.fileSize = Number(size[1]);
        if (this.cache) this.cache.fileSize = this.fileSize;
        return new Uint8Array();
      }
    }
    if (response.status !== 206) {
      await response.body?.cancel();
      throw new Error(`Subtitle extraction requires byte ranges (HTTP ${response.status})`);
    }
    const range = response.headers.get('Content-Range')?.match(/^bytes (\d+)-(\d+)\/(\d+|\*)$/);
    if (range && Number(range[1]) !== offset) {
      await response.body?.cancel();
      throw new Error('Unexpected subtitle byte range');
    }
    this.buffer = new Uint8Array(await response.arrayBuffer());
    this.signal?.throwIfAborted();
    this.bufferOffset = offset;
    if (range && range[3] !== '*') this.fileSize = Number(range[3]);
    else if (this.buffer.length < requestedEnd - offset + 1) this.fileSize = offset + this.buffer.length;
    if (!this.buffer.length || this.buffer.length > requestedEnd - offset + 1) {
      throw new Error('Invalid subtitle byte range length');
    }
    if (this.cache) {
      this.cache.fileSize = this.fileSize;
      this.cache.put(offset, this.buffer);
    }
    return this.buffer.subarray(0, length);
  }

  async element(offset: number): Promise<RemoteElement | null> {
    const bytes = await this.read(offset, 12);
    if (!bytes.length) return null;
    const id = readElementId(bytes, 0, bytes.length);
    const size = id && readElementSize(bytes, id.length, bytes.length);
    if (!id || !size) throw new Error('Truncated Matroska element header');
    const dataOffset = offset + id.length + size.length;
    const unknownSize = size.size === 2 ** (7 * size.length) - 1;
    const end = unknownSize ? this.fileSize : dataOffset + size.size;
    if (!unknownSize && (!Number.isSafeInteger(end) || end > this.fileSize)) {
      throw new Error('Invalid Matroska element size');
    }
    if (unknownSize && id.id !== ID_SEGMENT && id.id !== ID_CLUSTER) {
      throw new Error('Unsupported unknown-size Matroska element');
    }
    return { id: id.id, offset, dataOffset, end, unknownSize };
  }

  async payload(element: RemoteElement, includeHeader = false, maxBytes = MAX_SUBTITLE_ELEMENT_BYTES): Promise<Uint8Array> {
    const offset = includeHeader ? element.offset : element.dataOffset;
    const length = element.end - offset;
    if (length > maxBytes) throw new Error('Subtitle element is too large');
    const bytes = await this.read(offset, length);
    if (bytes.length !== length) throw new Error('Truncated subtitle element');
    return bytes;
  }

  async isTargetBlock(element: RemoteElement, trackNumber: number): Promise<boolean> {
    const bytes = await this.read(element.dataOffset, Math.min(8, element.end - element.dataOffset));
    return readElementSize(bytes, 0, bytes.length)?.size === trackNumber;
  }
}

function* childElements(bytes: Uint8Array) {
  let offset = 0;
  while (offset < bytes.length) {
    const id = readElementId(bytes, offset, bytes.length);
    const size = id && readElementSize(bytes, offset + id.length, bytes.length);
    if (!id || !size) throw new Error('Invalid Matroska metadata');
    const dataOffset = offset + id.length + size.length;
    const end = dataOffset + size.size;
    if (!Number.isSafeInteger(end) || end > bytes.length) throw new Error('Truncated Matroska metadata');
    yield { id: id.id, data: bytes.subarray(dataOffset, end) };
    offset = end;
  }
}

function readSeekHead(bytes: Uint8Array, metadata: SubtitleMetadata) {
  for (const entry of childElements(bytes)) {
    if (entry.id !== ID_SEEK) continue;
    let id = 0;
    let position: number | undefined;
    for (const child of childElements(entry.data)) {
      if (child.id === ID_SEEK_ID) id = readUInt(child.data, 0, child.data.length);
      if (child.id === ID_SEEK_POSITION) position = readUInt(child.data, 0, child.data.length);
    }
    if (position === undefined) continue;
    const offset = metadata.segmentOffset + position;
    if (!Number.isSafeInteger(offset)) continue;
    if (id === ID_CUES) metadata.cuesOffset = offset;
    if (id === ID_SEEK_HEAD && !metadata.seekHeads.includes(offset)) metadata.seekHeads.push(offset);
  }
}

async function readSubtitleMetadata(reader: SubtitleRangeReader): Promise<SubtitleMetadata> {
  const metadata: SubtitleMetadata = { segmentOffset: 0, firstCluster: 0, timecodeScaleNs: 1000000, tracks: [], seekHeads: [] };
  let offset = 0;
  while (true) {
    const element = await reader.element(offset);
    if (!element) throw new Error('No Matroska clusters found');
    if (element.id === ID_SEGMENT) {
      metadata.segmentOffset = element.dataOffset;
      offset = element.dataOffset;
      continue;
    }
    if (element.id === ID_CLUSTER) {
      metadata.firstCluster = element.offset;
      return metadata;
    }
    if (element.id === ID_SEEK_HEAD) readSeekHead(await reader.payload(element), metadata);
    if (element.id === ID_CUES) metadata.cuesOffset = element.offset;
    if (element.id === ID_TRACKS) metadata.tracks = parseMatroskaSubtitleTracks(await reader.payload(element, true));
    if (element.id === ID_INFO) {
      for (const child of childElements(await reader.payload(element))) {
        if (child.id === ID_TIMESTAMP_SCALE) metadata.timecodeScaleNs = readUInt(child.data, 0, child.data.length);
      }
    }
    offset = element.end;
  }
}

async function readSubtitleIndex(reader: SubtitleRangeReader, metadata: SubtitleMetadata) {
  if (metadata.index) return metadata.index;
  // Some muxers chain SeekHeads. Bound traversal and remember visited offsets.
  const visited = new Set<number>();
  for (let i = 0; metadata.cuesOffset === undefined && i < metadata.seekHeads.length && i < 8; i++) {
    const offset = metadata.seekHeads[i];
    if (visited.has(offset)) continue;
    visited.add(offset);
    const head = await reader.element(offset);
    if (head?.id === ID_SEEK_HEAD) readSeekHead(await reader.payload(head), metadata);
  }
  const positions = new Map<number, number>();
  if (metadata.cuesOffset !== undefined) {
    const cues = await reader.element(metadata.cuesOffset);
    // An oversized or absent index falls back to skipping whole clusters.
    if (cues?.id === ID_CUES && cues.end - cues.dataOffset <= 8 * 1024 * 1024) {
      for (const point of childElements(await reader.payload(cues, false, 8 * 1024 * 1024))) {
        if (point.id !== ID_CUE_POINT) continue;
        let time: number | undefined;
        const offsets: number[] = [];
        for (const child of childElements(point.data)) {
          if (child.id === ID_CUE_TIME) time = readUInt(child.data, 0, child.data.length) * metadata.timecodeScaleNs / 1e9;
          if (child.id !== ID_CUE_TRACK_POSITIONS) continue;
          for (const position of childElements(child.data)) {
            if (position.id !== ID_CUE_CLUSTER_POSITION) continue;
            const offset = metadata.segmentOffset + readUInt(position.data, 0, position.data.length);
            if (Number.isSafeInteger(offset) && offset >= metadata.firstCluster) offsets.push(offset);
          }
        }
        if (time !== undefined && Number.isFinite(time)) {
          for (const offset of offsets) positions.set(offset, Math.min(time, positions.get(offset) ?? Infinity));
        }
      }
    }
  }
  metadata.index = Array.from(positions, ([offset, time]) => ({ offset, time })).sort((a, b) => a.time - b.time);
  return metadata.index;
}

/**
 * Scan with bounded range reads, skipping media payloads. With playback options,
 * use the seek index and stop reading when subtitles are 30 seconds ahead.
 * Deliver each subtitle block immediately so playback never waits for later media.
 * Errors and cancellation propagate: partial results must never be cached as complete.
 */
export async function loadEmbeddedSubtitleTrackVtt(
  streamUrl: string,
  trackNumber: number,
  fetchFn: typeof fetch = fetch,
  signal?: AbortSignal,
  onCues?: (cues: SubtitleCue[]) => void,
  playback?: SubtitlePlaybackOptions
): Promise<string> {
  const reader = new SubtitleRangeReader(streamUrl, fetchFn, signal, playback?.cache, playback ? PLAYBACK_RANGE_BYTES : RANGE_BYTES);
  const cues: SubtitleCue[] = [];
  let position = 0;
  let timecodeScaleNs = 1000000;
  let codecId = 'S_TEXT/UTF8';
  let inCluster = false;
  let clusterTimestamp: Uint8Array | undefined;
  let pendingBlocks: Uint8Array[] = [];
  let clusterEnd = Infinity;
  let clusterHasKnownSize = false;
  let skipOldClusters = false;
  const startTime = playback?.currentTime() ?? 0;

  if (playback) {
    const metadata = playback.cache.metadata ?? await readSubtitleMetadata(reader);
    playback.cache.metadata = metadata;
    position = metadata.firstCluster;
    timecodeScaleNs = metadata.timecodeScaleNs;
    const track = metadata.tracks.find(track => track.trackNumber === trackNumber);
    if (track?.unavailableReason) throw new Error(track.unavailableReason);
    codecId = track?.codecId ?? codecId;
    if (startTime > SUBTITLE_SEEK_PREROLL_SECONDS) {
      const index = await readSubtitleIndex(reader, metadata);
      skipOldClusters = index.length === 0;
      const target = startTime - SUBTITLE_SEEK_PREROLL_SECONDS;
      for (const entry of index) {
        if (entry.time > target) break;
        position = entry.offset;
      }
      const cluster = await reader.element(position);
      // Ignore a bad index entry rather than parsing arbitrary bytes as a cluster.
      if (cluster?.id !== ID_CLUSTER) position = metadata.firstCluster;
    }
  }

  const flushBlocks = () => {
    if (!clusterTimestamp || !pendingBlocks.length) return;
    // Reassemble only subtitle elements into a valid unknown-size Cluster.
    const clusterParts = [new Uint8Array([0x1f, 0x43, 0xb6, 0x75, 0xff]), clusterTimestamp, ...pendingBlocks];
    const buffer = new Uint8Array(clusterParts.reduce((sum, part) => sum + part.length, 0));
    let offset = 0;
    for (const part of clusterParts) {
      buffer.set(part, offset);
      offset += part.length;
    }
    const batch = parseMatroskaSubtitleCues(buffer, trackNumber, timecodeScaleNs, codecId);
    pendingBlocks = [];
    cues.push(...batch);
    if (batch.length) onCues?.(batch);
  };

  while (true) {
    const element = await reader.element(position);
    if (!element) break;
    if (element.id === ID_SEGMENT) {
      position = element.dataOffset;
      continue;
    }
    if (element.id === ID_CLUSTER) {
      if (pendingBlocks.length) throw new Error('Missing Matroska cluster timestamp');
      inCluster = true;
      clusterTimestamp = undefined;
      clusterEnd = element.end;
      clusterHasKnownSize = !element.unknownSize;
      position = element.dataOffset;
      continue;
    }
    if (element.id === ID_TRACKS) {
      const track = parseMatroskaSubtitleTracks(await reader.payload(element, true))
        .find(track => track.trackNumber === trackNumber);
      if (track?.unavailableReason) throw new Error(track.unavailableReason);
      if (track) codecId = track.codecId;
    } else if (element.id === ID_INFO) {
      const info = await reader.payload(element);
      let offset = 0;
      while (offset < info.length) {
        const id = readElementId(info, offset, info.length);
        if (!id) break;
        offset += id.length;
        const size = readElementSize(info, offset, info.length);
        if (!size) break;
        offset += size.length;
        if (offset + size.size > info.length) throw new Error('Truncated Matroska info');
        if (id.id === ID_TIMESTAMP_SCALE) timecodeScaleNs = readUInt(info, offset, size.size);
        offset += size.size;
      }
    } else if (inCluster) {
      let include = false;
      if (element.id === ID_CLUSTER_TIMESTAMP) {
        clusterTimestamp = await reader.payload(element, true);
        if (playback) {
          const timestamp = readUInt(clusterTimestamp, element.dataOffset - element.offset, element.end - element.dataOffset) * timecodeScaleNs / 1e9;
          if (skipOldClusters && clusterHasKnownSize && timestamp < startTime - SUBTITLE_SEEK_PREROLL_SECONDS) {
            position = clusterEnd;
            inCluster = false;
            pendingBlocks = [];
            continue;
          }
          if (timestamp > playback.currentTime() + SUBTITLE_LOOK_AHEAD_SECONDS) playback.onWait?.();
          while (timestamp > playback.currentTime() + SUBTITLE_LOOK_AHEAD_SECONDS) {
            signal?.throwIfAborted();
            await playback.waitForTimeChange(signal);
          }
          signal?.throwIfAborted();
        }
      }
      if (element.id === ID_SIMPLE_BLOCK || element.id === ID_BLOCK) {
        include = await reader.isTargetBlock(element, trackNumber);
      } else if (element.id === ID_BLOCK_GROUP) {
        // Inspect block headers without fetching an entire video BlockGroup.
        let childOffset = element.dataOffset;
        while (childOffset < element.end) {
          const child = await reader.element(childOffset);
          if (!child || child.end > element.end) throw new Error('Invalid Matroska block group');
          if (child.id === ID_BLOCK) {
            include = await reader.isTargetBlock(child, trackNumber);
            break;
          }
          childOffset = child.end;
        }
      }
      if (include) pendingBlocks.push(await reader.payload(element, true));
      flushBlocks();
    }
    position = element.end;
  }
  signal?.throwIfAborted();
  if (pendingBlocks.length) throw new Error('Missing Matroska cluster timestamp');
  return 'data:text/vtt;charset=utf-8,' + encodeURIComponent(cuesToWebVtt(cues));
}

export function isMkvOrWebmStream(url?: string): boolean {
  if (!url) return false;
  const lower = url.toLowerCase();
  return lower.includes('.mkv') || lower.includes('.webm') || lower.includes('format=mkv');
}
