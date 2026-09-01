// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { vi } from 'vitest';

// Helper to construct a synthetic EBML buffer
export function encodeVint(val: number, isId = false): Uint8Array {
  if (isId) {
    // Return bytes for ID directly
    const bytes: number[] = [];
    let temp = val;
    while (temp > 0) {
      bytes.unshift(temp & 0xff);
      temp = temp >> 8;
    }
    return new Uint8Array(bytes);
  }

  // Value length encoding
  let len = 1;
  let max = 0x7f;
  while (val > max && len < 8) {
    len++;
    max = (max << 7) | 0x7f;
  }
  const buf = new Uint8Array(len);
  const mask = 1 << (8 - len);
  buf[0] = ((val >> ((len - 1) * 8)) & (mask - 1)) | mask;
  for (let i = 1; i < len; i++) {
    buf[i] = (val >> ((len - 1 - i) * 8)) & 0xff;
  }
  return buf;
}

export function createEbmlElement(id: number, data: Uint8Array): Uint8Array {
  const idBytes = encodeVint(id, true);
  const sizeBytes = encodeVint(data.length, false);
  const result = new Uint8Array(idBytes.length + sizeBytes.length + data.length);
  result.set(idBytes, 0);
  result.set(sizeBytes, idBytes.length);
  result.set(data, idBytes.length + sizeBytes.length);
  return result;
}

export function concatBuffers(...buffers: Uint8Array[]): Uint8Array {
  const total = buffers.reduce((acc, b) => acc + b.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const b of buffers) {
    out.set(b, offset);
    offset += b.length;
  }
  return out;
}

export function createStringElement(id: number, text: string): Uint8Array {
  const data = new TextEncoder().encode(text);
  return createEbmlElement(id, data);
}

export function createUIntElement(id: number, val: number, byteLen = 1): Uint8Array {
  const data = new Uint8Array(byteLen);
  for (let i = byteLen - 1; i >= 0; i--) {
    data[i] = val & 0xff;
    val = val >> 8;
  }
  return createEbmlElement(id, data);
}

export function rangeFetch(buffer: Uint8Array) {
  return vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
    const range = new Headers(init?.headers).get('Range')!;
    const [, startValue, endValue] = /bytes=(\d+)-(\d+)/.exec(range)!;
    const start = Number(startValue);
    const end = Math.min(Number(endValue), buffer.length - 1);
    if (start >= buffer.length) return new Response(null, { status: 416, headers: { 'Content-Range': `bytes */${buffer.length}` } });
    return new Response(buffer.slice(start, end + 1), {
      status: 206,
      headers: { 'Content-Range': `bytes ${start}-${end}/${buffer.length}` },
    });
  });
}

export function subtitleCluster(timestamp: number, text: string) {
  return createEbmlElement(0x1f43b675, concatBuffers(
    createUIntElement(0xe7, timestamp, 4),
    createEbmlElement(0xa0, concatBuffers(
      createEbmlElement(0xa1, concatBuffers(new Uint8Array([0x81, 0, 0, 0]), new TextEncoder().encode(text))),
      createUIntElement(0x9b, 200, 2)
    ))
  ));
}

/** Interleaved video packets, subtitle blocks, and a normal video-only seek index. */
export function indexedSubtitleMovie(scaleNs = 1000000, withIndex = true) {
  const seekHead = (offset: number) => createEbmlElement(0x114d9b74, createEbmlElement(0x4dbb, concatBuffers(
    createEbmlElement(0x53ab, encodeVint(0x1c53bb6b, true)), createUIntElement(0x53ac, offset, 4)
  )));
  const tracks = createEbmlElement(0x1654ae6b, createEbmlElement(0xae, concatBuffers(
    createUIntElement(0xd7, 1), createUIntElement(0x83, 17), createStringElement(0x86, 'S_TEXT/UTF8')
  )));
  const info = createEbmlElement(0x1549a966, createUIntElement(0x2ad7b1, scaleNs, 4));
  const video = createEbmlElement(0xa3, concatBuffers(new Uint8Array([0x82, 0, 0, 0]), new Uint8Array(8192)));
  const clusters = Array.from({ length: 13 }, (_, i) => createEbmlElement(0x1f43b675, concatBuffers(
    createUIntElement(0xe7, i * 10 * 1e9 / scaleNs, 4),
    createEbmlElement(0xa0, concatBuffers(
      createEbmlElement(0xa1, concatBuffers(new Uint8Array([0x81, 0, 0, 0]), new TextEncoder().encode(`Cue ${i * 10}`))),
      createUIntElement(0x9b, 2 * 1e9 / scaleNs, 4)
    )), ...Array.from({ length: 24 }, () => video)
  )));
  let offset = (withIndex ? seekHead(0).length : 0) + tracks.length + info.length;
  const positions = clusters.map(cluster => { const start = offset; offset += cluster.length; return start; });
  const cuesOffset = offset;
  const cues = createEbmlElement(0x1c53bb6b, concatBuffers(...positions.map((offset, i) => createEbmlElement(0xbb, concatBuffers(
    createUIntElement(0xb3, i * 10 * 1e9 / scaleNs, 4),
    createEbmlElement(0xb7, concatBuffers(createUIntElement(0xf7, 2), createUIntElement(0xf1, offset, 4)))
  )))));
  const payload = concatBuffers(withIndex ? seekHead(cuesOffset) : new Uint8Array(), tracks, info, ...clusters, withIndex ? cues : new Uint8Array());
  const buffer = createEbmlElement(0x18538067, payload);
  const segmentOffset = buffer.length - payload.length;
  return { buffer, cuesOffset: segmentOffset + cuesOffset, clusterOffsets: positions.map(position => segmentOffset + position) };
}
