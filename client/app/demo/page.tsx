// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { toast } from 'sonner';

import { HeaderLayout } from '@/components/header-layout';
import { PageContainer } from '@/components/page-container';
import { TorrentControls } from '@/components/torrent-controls';
import { TorrentGrid } from '@/components/torrent-grid';
import { useTorrentFilterSettings } from '@/hooks/use-torrent-filter-settings';
import { Torrent, TorrentStats } from '@/lib/types/api';

import { DemoAddTorrentDialog } from './demo-add-torrent-dialog';
import { DemoDeleteTorrentDialog } from './demo-delete-torrent-dialog';
import { DemoEditTorrentDialog } from './demo-edit-torrent-dialog';
import { DemoMetricsDialog } from './demo-metrics-dialog';
import { DemoSettingsDialog } from './demo-settings-dialog';
import { DemoSystemInfoDialog } from './demo-system-info-dialog';
import { DemoTorrentPlayerDialog } from './demo-torrent-player-dialog';
import { DemoTorrentStatsDialog } from './demo-torrent-stats-dialog';

const memoryStats = {
  activeTorrents: 4,
  maxMemory: 536870912,
  totalPieces: 4000,
  usedMemory: 268435456
};

const demoTorrentsData: Torrent[] = [
  {
    hash: '08ada5a7a6183aae1e09d831df6748d566095a10',
    name: 'Sintel',
    title: 'Sintel',
    magnet: 'magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10',
    poster: 'https://upload.wikimedia.org/wikipedia/commons/8/8f/Sintel_poster.jpg',
    category: 'Fantasy',
    createdAt: '2026-01-10T09:00:00.000Z',
    updatedAt: '2026-01-18T11:00:00.000Z',
    totalSize: 137302391,
    pieceCount: 1048,
    pieceSize: 131072,
    storage: 'file',
    active: true,
    files: [
      { name: 'Sintel.en-sdh.vtt', path: 'Sintel/Sintel.en-sdh.vtt', length: 200 },
      { name: 'Sintel.es.vtt', path: 'Sintel/Sintel.es.vtt', length: 272 },
      { name: 'Sintel.webm', path: 'Sintel/Sintel.webm', length: 129241752 },
      { name: 'Sintel Trailer.webm', path: 'Sintel/Sintel Trailer.webm', length: 8000000 },
      { name: 'poster.jpg', path: 'Sintel/poster.jpg', length: 46115 },
    ],
  },
  {
    hash: 'dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c',
    name: 'Big Buck Bunny',
    title: 'Big Buck Bunny',
    magnet: 'magnet:?xt=urn:btih:dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c&dn=Big+Buck+Bunny&tr=udp%3A%2F%2Fexplodie.org%3A6969&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.empire-js.us%3A1337&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=wss%3A%2F%2Ftracker.btorrent.xyz&tr=wss%3A%2F%2Ftracker.fastcast.nz&tr=wss%3A%2F%2Ftracker.openwebtorrent.com&ws=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2F&xs=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2Fbig-buck-bunny.torrent',
    poster: 'https://upload.wikimedia.org/wikipedia/commons/c/c5/Big_buck_bunny_poster_big.jpg',
    category: 'Animation',
    createdAt: '2026-01-01T10:00:00.000Z',
    updatedAt: '2026-01-20T14:00:00.000Z',
    totalSize: 276445467,
    pieceCount: 1055,
    pieceSize: 262144,
    storage: 'file',
    files: [
      { name: 'Big Buck Bunny.en-sdh.vtt', path: 'Big Buck Bunny/Big Buck Bunny.en-sdh.vtt', length: 200 },
      { name: 'Big Buck Bunny.es.vtt', path: 'Big Buck Bunny/Big Buck Bunny.es.vtt', length: 272 },
      { name: 'Big Buck Bunny.webm', path: 'Big Buck Bunny/Big Buck Bunny.webm', length: 276134947 },
      { name: 'poster.jpg', path: 'Big Buck Bunny/poster.jpg', length: 310380 }
    ],
  },
  {
    hash: 'c9e15763f722f23e98a29decdfae341b98d53056',
    name: 'Cosmos Laundromat',
    title: 'Cosmos Laundromat',
    magnet: 'magnet:?xt=urn:btih:c9e15763f722f23e98a29decdfae341b98d53056&dn=Cosmos+Laundromat&tr=udp%3A%2F%2Fexplodie.org%3A6969&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.empire-js.us%3A1337&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=wss%3A%2F%2Ftracker.btorrent.xyz&tr=wss%3A%2F%2Ftracker.fastcast.nz&tr=wss%3A%2F%2Ftracker.openwebtorrent.com&ws=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2F&xs=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2Fcosmos-laundromat.torrent',
    poster: 'https://upload.wikimedia.org/wikipedia/commons/c/c5/CosmosLaundromatPoster.jpg',
    category: 'Sci-Fi',
    createdAt: '2026-01-04T12:00:00.000Z',
    updatedAt: '2026-01-15T16:00:00.000Z',
    totalSize: 220864086,
    pieceCount: 843,
    pieceSize: 262144,
    storage: 'memory',
    files: [
      { name: 'Cosmos Laundromat.en-sdh.vtt', path: 'Cosmos Laundromat/Cosmos Laundromat.en-sdh.vtt', length: 200 },
      { name: 'Cosmos Laundromat.es.vtt', path: 'Cosmos Laundromat/Cosmos Laundromat.es.vtt', length: 272 },
      { name: 'Cosmos Laundromat.webm', path: 'Cosmos Laundromat/Cosmos Laundromat.webm', length: 220087570 },
      { name: 'poster.jpg', path: 'Cosmos Laundromat/poster.jpg', length: 760595 },
    ],
  },
  {
    hash: '209c8226b299b308beaf2b9cd3fb49212dbd13ec',
    name: 'Tears of Steel',
    title: 'Tears of Steel',
    magnet: 'magnet:?xt=urn:btih:209c8226b299b308beaf2b9cd3fb49212dbd13ec&dn=Tears+of+Steel&tr=udp%3A%2F%2Fexplodie.org%3A6969&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.empire-js.us%3A1337&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=wss%3A%2F%2Ftracker.btorrent.xyz&tr=wss%3A%2F%2Ftracker.fastcast.nz&tr=wss%3A%2F%2Ftracker.openwebtorrent.com&ws=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2F&xs=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2Ftears-of-steel.torrent',
    poster: 'https://upload.wikimedia.org/wikipedia/commons/thumb/7/70/Tos-poster.png/1280px-Tos-poster.png',
    category: 'Live Action',
    createdAt: '2026-01-07T15:00:00.000Z',
    updatedAt: '2026-01-10T18:00:00.000Z',
    totalSize: 571426507,
    pieceCount: 1090,
    pieceSize: 524288,
    storage: 'memory',
    files: [
      { name: 'Tears of Steel.en-sdh.vtt', path: 'Tears of Steel/Tears of Steel.en-sdh.vtt', length: 200 },
      { name: 'Tears of Steel.es.vtt', path: 'Tears of Steel/Tears of Steel.es.vtt', length: 272 },
      { name: 'Tears of Steel.webm', path: 'Tears of Steel/Tears of Steel.webm', length: 571346576 },
      { name: 'poster.jpg', path: 'Tears of Steel/poster.jpg', length: 35996 },
    ],
  },
];

const demoTorrentStats: Record<string, TorrentStats> = {
  '08ada5a7a6183aae1e09d831df6748d566095a10': {
    activePeers: 15,
    bytesHashed: 129302391,
    bytesRead: 135000000,
    bytesReadData: 130000000,
    bytesReadUsefulData: 129500000,
    bytesReadUsefulIntendedData: 129302391,
    bytesWritten: 2500000000,
    bytesWrittenData: 2400000000,
    chunksRead: 1050,
    chunksReadUseful: 1000,
    chunksReadWasted: 50,
    chunksWritten: 20000,
    connectedSeeders: 10,
    halfOpenPeers: 2,
    metadataChunksRead: 5,
    pendingPeers: 3,
    piecesComplete: 987,
    piecesDirtiedBad: 5,
    piecesDirtiedGood: 1000,
    totalPeers: 20,
    completedSize: 129302391,
    writtenBytes: 117440512,
    inMemory: 0,
    inMemorySize: 0,
    memoryStats: memoryStats,
    memoryUsagePercentage: 0,
    pieces: Array.from({ length: 987 }, (_, i) => ({
      complete: true,
      inMemory: false,
      index: i,
      size: i === 986 ? 65399 : 131072,
    })),
    readers: [
      { position: 350, start: 340, end: 460 }
    ],
    totalPieces: 987,
    totalSize: 129302391,
  },
  'dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c': {
    activePeers: 25,
    bytesHashed: 276445467,
    bytesRead: 280000000,
    bytesReadData: 277000000,
    bytesReadUsefulData: 276500000,
    bytesReadUsefulIntendedData: 276445467,
    bytesWritten: 1600000000,
    bytesWrittenData: 1500000000,
    chunksRead: 1100,
    chunksReadUseful: 1055,
    chunksReadWasted: 45,
    chunksWritten: 6000,
    connectedSeeders: 15,
    halfOpenPeers: 5,
    metadataChunksRead: 8,
    pendingPeers: 5,
    piecesComplete: 1055,
    piecesDirtiedBad: 10,
    piecesDirtiedGood: 1060,
    totalPeers: 35,
    completedSize: 276445467,
    writtenBytes: 268435456,
    inMemory: 0,
    inMemorySize: 0,
    memoryStats: memoryStats,
    memoryUsagePercentage: 0,
    pieces: Array.from({ length: 1055 }, (_, i) => ({
      complete: true,
      inMemory: false,
      index: i,
      size: i === 1054 ? 145691 : 262144,
    })),
    readers: [
      { position: 200, start: 190, end: 320 }
    ],
    totalPieces: 1055,
    totalSize: 276445467,
  },
  'c9e15763f722f23e98a29decdfae341b98d53056': {
    activePeers: 30,
    bytesHashed: 220864086,
    bytesRead: 110000000,
    bytesReadData: 105000000,
    bytesReadUsefulData: 102000000,
    bytesReadUsefulIntendedData: 101600000,
    bytesWritten: 55000000,
    bytesWrittenData: 50000000,
    chunksRead: 500,
    chunksReadUseful: 450,
    chunksReadWasted: 50,
    chunksWritten: 250,
    connectedSeeders: 12,
    halfOpenPeers: 5,
    metadataChunksRead: 10,
    pendingPeers: 7,
    piecesComplete: 387,
    piecesDirtiedBad: 10,
    piecesDirtiedGood: 400,
    totalPeers: 42,
    completedSize: 101600000,
    writtenBytes: 94371840,
    inMemory: 387,
    inMemorySize: 101400000,
    memoryStats: memoryStats,
    memoryUsagePercentage: 50,
    pieces: Array.from({ length: 843 }, (_, i) => ({
      complete: i < 387,
      inMemory: i < 387,
      index: i,
      size: i === 842 ? 138838 : 262144,
    })),
    readers: [
      { position: 180, start: 170, end: 300 }
    ],
    totalPieces: 843,
    totalSize: 220864086
  },
  '209c8226b299b308beaf2b9cd3fb49212dbd13ec': {
    activePeers: 10,
    bytesHashed: 1525451,
    bytesRead: 1666873,
    bytesReadData: 1640139,
    bytesReadUsefulData: 1525451,
    bytesReadUsefulIntendedData: 1525451,
    bytesWritten: 4836,
    bytesWrittenData: 0,
    chunksRead: 101,
    chunksReadUseful: 94,
    chunksReadWasted: 7,
    chunksWritten: 0,
    completedSize: 1525451,
    writtenBytes: 1048576,
    connectedSeeders: 15,
    halfOpenPeers: 0,
    inMemory: 3,
    inMemorySize: 1525451,
    memoryStats: memoryStats,
    memoryUsagePercentage: 1,
    metadataChunksRead: 2,
    pendingPeers: 12,
    pieces: Array.from({ length: 1090 }, (_, i) => ({
      complete: i === 0 || i === 1 || i === 1089,
      inMemory: i === 0 || i === 1 || i === 1089,
      index: i,
      size: i === 1089 ? 476875 : 524288,
    })),
    readers: [
      { position: 0, start: 0, end: 40 }
    ],
    piecesComplete: 3,
    piecesDirtiedBad: 0,
    piecesDirtiedGood: 3,
    totalPeers: 129,
    totalPieces: 1090,
    totalSize: 571426507
  },
};

let dbTorrents = [...demoTorrentsData];

const getTorrents = () => new Promise<{ torrents: Torrent[], total: number, limit: number, offset: number }>(resolve => setTimeout(() => resolve({ torrents: demoTorrentsData, total: demoTorrentsData.length, limit: 100, offset: 0 }), 500));

const deleteTorrent = (hash: string) => new Promise(resolve => setTimeout(() => {
  dbTorrents = dbTorrents.filter(t => t.hash !== hash);
  resolve({});
}, 500));

function DemoContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const modal = searchParams.get('modal');
  const hash = searchParams.get('hash');
  const gridRef = useRef<HTMLDivElement>(null);
  const lastActiveElementRef = useRef<HTMLElement | null>(null);

  const updateModal = (modalName: string | null, hashValue: string | null = null) => {
    if (modalName && typeof document !== 'undefined') {
      const active = document.activeElement as HTMLElement | null;
      if (active && active !== document.body && !active.closest('[role="dialog"]')) {
        lastActiveElementRef.current = active;
      }
    }
    const params = new URLSearchParams(searchParams.toString());
    if (modalName) {
      params.set('modal', modalName);
    } else {
      params.delete('modal');
    }
    if (hashValue) {
      params.set('hash', hashValue);
    } else {
      params.delete('hash');
    }
    router.push(`?${params.toString()}`, { scroll: false });
  };

  const prevModalRef = useRef<string | null>(modal);
  useEffect(() => {
    if (prevModalRef.current && !modal) {
      requestAnimationFrame(() => {
        if (lastActiveElementRef.current && document.body.contains(lastActiveElementRef.current)) {
          lastActiveElementRef.current.focus();
        } else if (gridRef.current && document.activeElement === document.body) {
          const firstItem = gridRef.current.querySelector<HTMLElement>('[data-radix-collection-item], [data-item]');
          firstItem?.focus();
        }
      });
    }
    prevModalRef.current = modal;
  }, [modal]);

  const {
    titleFilter,
    handleTitleFilterChange,
    categoryFilter,
    handleCategoryFilterChange,
    sortBy,
    handleSortByChange,
  } = useTorrentFilterSettings({ searchParams });

  const [isDeleting, setIsDeleting] = useState(false);
  const [liveUpdatesPaused, setLiveUpdatesPaused] = useState(false);
  const [version] = useState<string | null>('Demo');
  const [isAuthenticated, setIsAuthenticated] = useState(false);
  const auth = { enabled: false, type: 'basic' as const };
  const logout = () => {
    setIsAuthenticated(false);
    toast.success('Logged out', { description: 'Demo mode - auth not actually changed' });
  };
  const handlePauseClick = () => setLiveUpdatesPaused(prev => !prev);

  useEffect(() => {
    getTorrents().then((data: { torrents: Torrent[], total: number, limit: number, offset: number }) => {
      setTorrents(data.torrents);
    });
  }, []);

  const [torrents, setTorrents] = useState<Torrent[]>([]);
  const categories = useMemo(() => {
    const allCategories = torrents.map(t => t.category).filter(Boolean) as string[];
    return Array.from(new Set(allCategories)).sort();
  }, [torrents]);

  const filteredAndSortedTorrents = useMemo(() => {
    const filtered = torrents
      .filter(torrent => {
        const titleMatch =
          !titleFilter ||
          (torrent.title || torrent.name || '')
            .toLowerCase()
            .includes(titleFilter.toLowerCase());
        const categoryMatch = !categoryFilter || (torrent.category || '') === categoryFilter;
        return titleMatch && categoryMatch;
      });

    return filtered.slice().sort((a, b) => {
      switch (sortBy) {
        case 'name':
          return (a.title || a.name || '').localeCompare(b.title || b.name || '');
        case 'size':
          return (b.totalSize || 0) - (a.totalSize || 0);
        case 'updated':
          return new Date(b.updatedAt || 0).getTime() - new Date(a.updatedAt || 0).getTime();
        case 'date':
        default:
          return new Date(b.createdAt || 0).getTime() - new Date(a.createdAt || 0).getTime();
      }
    });
  }, [torrents, titleFilter, categoryFilter, sortBy]);

  const selectedTorrent = useMemo(() => {
    const validModals = ['edit', 'stats', 'play', 'delete'];
    if (modal && validModals.includes(modal) && hash && torrents) {
      return torrents.find(t => t.hash === hash) || null;
    }
    return null;
  }, [modal, hash, torrents]);

  const handlePlay = async (torrent: Torrent) => {
    updateModal('play', torrent.hash);
  };

  const handleViewStats = async (torrent: Torrent) => {
    updateModal('stats', torrent.hash);
  };

  const handleEdit = async (torrent: Torrent) => {
    updateModal('edit', torrent.hash);
  };

  const handleDelete = (torrent: Torrent) => {
    updateModal('delete', torrent.hash);
  };

  const handleAddToDatabase = (torrent: Torrent) => {
    toast.success('Added to database (demo)', {
      description: `Torrent "${torrent.title || torrent.name}" would be added.`,
    });
  };

  const handleMetricsClick = () => {
    updateModal('metrics');
  };

  const handleSettingsClick = () => {
    updateModal('settings');
  };

  const handleSystemInfoClick = () => {
    updateModal('system-info');
  };

  const handleDeleteClick = async () => {
    if (!selectedTorrent) return;
    setIsDeleting(true);
    try {
      await deleteTorrent(selectedTorrent.hash);
      setTorrents(prevTorrents => prevTorrents.filter(t => t.hash !== selectedTorrent.hash));
      updateModal(null);
      toast.success('Torrent deleted', {
        description: `Successfully deleted ${selectedTorrent.title || selectedTorrent.name}`,
      });
    } catch {
      toast.error('Delete failed', { description: 'Failed to delete torrent' });
    } finally {
      setIsDeleting(false);
    }
  };

  const handleAddSuccess = () => {
    getTorrents().then(data => {
      setTorrents(data.torrents);
    });
  };

  return (
    <>
      <HeaderLayout
        homeHref='/demo'
        onMetricsClick={handleMetricsClick}
        onSettingsClick={handleSettingsClick}
        onSystemInfoClick={handleSystemInfoClick}
        onTitleSearch={handleTitleFilterChange}
        searchQuery={titleFilter}
        liveUpdatesPaused={liveUpdatesPaused}
        handlePauseClick={handlePauseClick}
        version={version}
        isAuthenticated={isAuthenticated}
        logout={logout}
        auth={auth}
        isHidden={false}
        inert={Boolean(modal)}
      />
      <PageContainer inert={Boolean(modal)}>
        <TorrentControls
          torrentsData={{ torrents }}
          torrents={categories}
          filteredAndSortedTorrents={filteredAndSortedTorrents}
          categoryFilter={categoryFilter}
          onCategoryFilterChange={handleCategoryFilterChange}
          sortBy={sortBy}
          onSortByChange={handleSortByChange}
          onAddTorrent={() => updateModal('add')}
          titleFilter={titleFilter}
        />

        <div ref={gridRef}>
          <TorrentGrid
            torrents={filteredAndSortedTorrents}
            onEdit={handleEdit}
            onViewStats={handleViewStats}
            onDelete={handleDelete}
            onPlay={handlePlay}
            onAddToDatabase={handleAddToDatabase}
          />
        </div>
      </PageContainer>

      <DemoMetricsDialog
        open={modal === 'metrics'}
        onOpenChange={(isOpen: boolean) => !isOpen && updateModal(null)}
      />
      <DemoSettingsDialog
        open={modal === 'settings'}
        onOpenChange={(isOpen: boolean) => !isOpen && updateModal(null)}
      />
      <DemoSystemInfoDialog
        open={modal === 'system-info'}
        onOpenChange={(isOpen: boolean) => !isOpen && updateModal(null)}
      />
      <DemoEditTorrentDialog
        torrent={selectedTorrent}
        open={modal === 'edit' && !!selectedTorrent}
        onOpenChange={(isOpen: boolean) => !isOpen && updateModal(null)}
      />
      <DemoTorrentStatsDialog
        torrent={selectedTorrent}
        open={modal === 'stats' && !!selectedTorrent}
        onOpenChange={(isOpen: boolean) => !isOpen && updateModal(null)}
        stats={demoTorrentStats}
      />
      <DemoTorrentPlayerDialog
        torrent={selectedTorrent}
        open={modal === 'play' && !!selectedTorrent}
        onOpenChange={(isOpen: boolean) => !isOpen && updateModal(null)}
        enablePreload={true}
      />
      <DemoDeleteTorrentDialog
        torrent={selectedTorrent}
        open={modal === 'delete' && !!selectedTorrent}
        onOpenChange={(isOpen: boolean) => !isOpen && updateModal(null)}
        isDeleting={isDeleting}
        onDelete={handleDeleteClick}
      />

      <DemoAddTorrentDialog
        open={modal === 'add'}
        onOpenChange={(isOpen: boolean) => !isOpen && updateModal(null)}
        onSuccess={handleAddSuccess}
      />
    </>
  );
}

export default function Demo() {
  return (
    <Suspense>
      <DemoContent />
    </Suspense>
  );
}
