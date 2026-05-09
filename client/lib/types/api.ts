// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

// API Types based on OpenAPI specification

interface Auth {
  enabled: boolean,
  type: 'basic' | 'bearer',
  username?: string,
  password?: string
}

interface MemoryStats {
  activeTorrents: number,
  maxMemory: number,
  totalPieces: number,
  usedMemory: number
}

interface PieceInfo {
  complete: boolean,
  inMemory: boolean,
  index: number,
  size: number
}

interface Settings {
  auth: Auth,
  enableDlna: boolean,
  enableDownloader: boolean,
  fileStoragePath: string,
  friendlyName: string,
  maxMemory: number
}

interface SystemInfo {
  buildDate: string,
  commit: string,
  uptime: number,
  version: string
}

interface TokenRequest {
  grantType: 'password',
  username: string,
  password: string
}

interface TokenResponse {
  accessToken: string,
  tokenType: 'Bearer',
  expiresIn?: number
}

interface Torrent {
  category?: string,
  createdAt?: string,
  files: TorrentFile[],
  hash: string,
  magnet: string,
  name: string,
  pieceCount: number,
  pieceSize: number,
  poster?: string,
  storage: 'memory' | 'file',
  title?: string,
  totalSize: number,
  updatedAt?: string
}

interface TorrentFile {
  length: number,
  name: string,
  path: string
}

interface TorrentAdd {
  category?: string,
  hash?: string,
  magnet?: string,
  poster?: string,
  storage?: 'memory' | 'file',
  title?: string
}

interface TorrentAddWithFile {
  file: File,
  poster?: string,
  storage?: 'memory' | 'file',
  title?: string
};

interface TorrentStats {
  activePeers: number,
  bytesHashed: number,
  bytesRead: number,
  bytesReadData: number,
  bytesReadUsefulData: number,
  bytesReadUsefulIntendedData: number,
  bytesWritten: number,
  bytesWrittenData: number,
  chunksRead: number,
  chunksReadUseful: number,
  chunksReadWasted: number,
  chunksWritten: number,
  connectedSeeders: number,
  halfOpenPeers: number,
  metadataChunksRead: number,
  pendingPeers: number,
  piecesComplete: number,
  piecesDirtiedBad: number,
  piecesDirtiedGood: number,
  totalPeers: number,
  completedSize: number,
  inMemory: number,
  inMemorySize: number,
  memoryStats: MemoryStats,
  memoryUsagePercentage: number,
  pieces: PieceInfo[],
  totalPieces: number,
  totalSize: number
}

interface TorrentsResponse {
  limit: number,
  offset: number,
  torrents: Torrent[],
  total: number
}

interface TorrentUpdate {
  category?: string,
  poster?: string,
  storage?: 'memory' | 'file',
  title?: string
}

export type {
  Auth,
  MemoryStats,
  PieceInfo,
  Settings,
  SystemInfo,
  TokenRequest,
  TokenResponse,
  Torrent,
  TorrentAdd,
  TorrentAddWithFile,
  TorrentFile,
  TorrentsResponse,
  TorrentStats,
  TorrentUpdate,
};
