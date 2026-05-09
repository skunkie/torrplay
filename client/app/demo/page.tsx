// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';

import { HeaderLayout } from '@/components/header-layout';
import { PageContainer } from '@/components/page-container';
import { TorrentControls } from '@/components/torrent-controls';
import { TorrentGrid } from '@/components/torrent-grid';
import { Button } from '@/components/ui/button';
import { Torrent, TorrentStats } from '@/lib/types/api';

import { DemoAddTorrentDialog } from './demo-add-torrent-dialog';
import { DemoDeleteTorrentDialog } from './demo-delete-torrent-dialog';
import { DemoEditTorrentDialog } from './demo-edit-torrent-dialog';
import { DemoMetricsDialog } from './demo-metrics-dialog';
import { DemoSettingsDialog } from './demo-settings-dialog';
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
    category: 'Animation',
    createdAt: '2026-01-01T00:00:00.000000+00:00',
    totalSize: 129302391,
    pieceCount: 987,
    pieceSize: 131072,
    storage: 'file',
    files: [
      { name: 'Sintel.de.srt', path: 'Sintel/Sintel.de.srt', length: 1652 },
      { name: 'Sintel.en.srt', path: 'Sintel/Sintel.en.srt', length: 1514 },
      { name: 'Sintel.es.srt', path: 'Sintel/Sintel.es.srt', length: 1554 },
      { name: 'Sintel.fr.srt', path: 'Sintel/Sintel.fr.srt', length: 1618 },
      { name: 'Sintel.it.srt', path: 'Sintel/Sintel.it.srt', length: 1546 },
      { name: 'Sintel.mp4', path: 'Sintel/Sintel.mp4', length: 129241752 },
      { name: 'Sintel.nl.srt', path: 'Sintel/Sintel.nl.srt', length: 1537 },
      { name: 'Sintel.pl.srt', path: 'Sintel/Sintel.pl.srt', length: 1536 },
      { name: 'Sintel.pt.srt', path: 'Sintel/Sintel.pt.srt', length: 1551 },
      { name: 'Sintel.ru.srt', path: 'Sintel/Sintel.ru.srt', length: 2016 },
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
    createdAt: '2026-01-01T00:00:00.000000+00:00',
    updatedAt: '2026-01-02T00:00:00.000000+00:00',
    totalSize: 276445467,
    pieceCount: 1055,
    pieceSize: 262144,
    storage: 'file',
    files: [
      { name: 'Big Buck Bunny.en.srt', path: 'Big Buck Bunny/Big Buck Bunny.en.srt', length: 140 },
      { name: 'Big Buck Bunny.mp4', path: 'Big Buck Bunny/Big Buck Bunny.mp4', length: 276134947 },
      { name: 'poster.jpg', path: 'Big Buck Bunny/poster.jpg', length: 310380 }
    ],
  },
  {
    hash: 'c9e15763f722f23e98a29decdfae341b98d53056',
    name: 'Cosmos Laundromat',
    title: 'Cosmos Laundromat',
    magnet: 'magnet:?xt=urn:btih:c9e15763f722f23e98a29decdfae341b98d53056&dn=Cosmos+Laundromat&tr=udp%3A%2F%2Fexplodie.org%3A6969&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.empire-js.us%3A1337&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=wss%3A%2F%2Ftracker.btorrent.xyz&tr=wss%3A%2F%2Ftracker.fastcast.nz&tr=wss%3A%2F%2Ftracker.openwebtorrent.com&ws=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2F&xs=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2Fcosmos-laundromat.torrent',
    poster: 'https://upload.wikimedia.org/wikipedia/commons/c/c5/CosmosLaundromatPoster.jpg',
    category: 'Animation',
    createdAt: '2026-01-01T00:00:00.000000+00:00',
    totalSize: 220864086,
    pieceCount: 843,
    pieceSize: 262000,
    storage: 'memory',
    files: [
      { name: 'Cosmos Laundromat.en.srt', path: 'Cosmos Laundromat/Cosmos Laundromat.en.srt', length: 3945 },
      { name: 'Cosmos Laundromat.es.srt', path: 'Cosmos Laundromat/Cosmos Laundromat.es.srt', length: 3911 },
      { name: 'Cosmos Laundromat.fr.srt', path: 'Cosmos Laundromat/Cosmos Laundromat.fr.srt', length: 4120 },
      { name: 'Cosmos Laundromat.it.srt', path: 'Cosmos Laundromat/Cosmos Laundromat.it.srt', length: 3945 },
      { name: 'Cosmos Laundromat.mp4', path: 'Cosmos Laundromat/Cosmos Laundromat.mp4', length: 220087570 },
      { name: 'poster.jpg', path: 'Cosmos Laundromat/poster.jpg', length: 760595 },
    ],
  },
  {
    hash: '209c8226b299b308beaf2b9cd3fb49212dbd13ec',
    name: 'Tears of Steel',
    title: 'Tears of Steel',
    magnet: 'magnet:?xt=urn:btih:209c8226b299b308beaf2b9cd3fb49212dbd13ec&dn=Tears+of+Steel&tr=udp%3A%2F%2Fexplodie.org%3A6969&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.empire-js.us%3A1337&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=wss%3A%2F%2Ftracker.btorrent.xyz&tr=wss%3A%2F%2Ftracker.fastcast.nz&tr=wss%3A%2F%2Ftracker.openwebtorrent.com&ws=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2F&xs=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2Ftears-of-steel.torrent',
    poster: 'https://upload.wikimedia.org/wikipedia/commons/thumb/7/70/Tos-poster.png/1280px-Tos-poster.png',
    category: 'Animation',
    createdAt: '2026-01-01T00:00:00.000000+00:00',
    totalSize: 571426507,
    pieceCount: 1090,
    pieceSize: 524288,
    storage: 'memory',
    files: [
      { name: 'Tears of Steel.de.srt', path: 'Tears of Steel/Tears of Steel.de.srt', length: 4850 },
      { name: 'Tears of Steel.en.srt', path: 'Tears of Steel/Tears of Steel.en.srt', length: 4755 },
      { name: 'Tears of Steel.es.srt', path: 'Tears of Steel/Tears of Steel.es.srt', length: 4944 },
      { name: 'Tears of Steel.fr.srt', path: 'Tears of Steel/Tears of Steel.fr.srt', length: 4618 },
      { name: 'Tears of Steel.it.srt', path: 'Tears of Steel/Tears of Steel.it.srt', length: 4746 },
      { name: 'Tears of Steel.nl.srt', path: 'Tears of Steel/Tears of Steel.nl.srt', length: 4531 },
      { name: 'Tears of Steel.no.srt', path: 'Tears of Steel/Tears of Steel.no.srt', length: 9558 },
      { name: 'Tears of Steel.ru.srt', path: 'Tears of Steel/Tears of Steel.ru.srt', length: 5933 },
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
    inMemory: 0,
    inMemorySize: 129302391,
    memoryStats: memoryStats,
    memoryUsagePercentage: 0,
    pieces: Array.from({ length: 987 }, (_, i) => ({
      complete: i < 987,
      inMemory: i < 987,
      index: i,
      size: 131072
    })),
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
    inMemory: 0,
    inMemorySize: 276445467,
    memoryStats: memoryStats,
    memoryUsagePercentage: 0,
    pieces: Array.from({ length: 1055 }, (_, i) => ({
      complete: i < 1055,
      inMemory: i < 1055,
      index: i,
      size: 262144
    })),
    totalPieces: 1055,
    totalSize: 276445467,
  },
  'c9e15763f722f23e98a29decdfae341b98d53056': {
    activePeers: 30,
    bytesHashed: 101600000,
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
    inMemory: 387,
    inMemorySize: 101400000,
    memoryStats: memoryStats,
    memoryUsagePercentage: 50,
    pieces: Array.from({ length: 843 }, (_, i) => ({
      complete: i < 387,
      inMemory: i < 387,
      index: i,
      size: 262000
    })),
    totalPieces: 843,
    totalSize: 220864086
  },
  '209c8226b299b308beaf2b9cd3fb49212dbd13ec': {
    activePeers: 10,
    bytesHashed: 572951958,
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
    connectedSeeders: 15,
    halfOpenPeers: 0,
    inMemory: 3,
    inMemorySize: 1525451,
    memoryStats: memoryStats,
    memoryUsagePercentage: 1,
    metadataChunksRead: 2,
    pendingPeers: 12,
    pieces: [
      { complete: true, inMemory: true, index: 0, size: 524288 },
      { complete: true, inMemory: true, index: 1, size: 524288 },
      { complete: true, inMemory: true, index: 1089, size: 476875 }
    ],
    piecesComplete: 3,
    piecesDirtiedBad: 0,
    piecesDirtiedGood: 3,
    totalPeers: 129,
    totalPieces: 1090,
    totalSize: 1525451
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

  const updateModal = (modalName: string | null, hashValue: string | null = null) => {
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

  const [titleFilter, setTitleFilter] = useState('');
  const [categoryFilter, setCategoryFilter] = useState('');
  const [sortBy, setSortBy] = useState('date');
  const [page, setPage] = useState(1);
  const [torrentsPerPage, setTorrentsPerPage] = useState(0);
  const [isDeleting, setIsDeleting] = useState(false);
  const [liveUpdatesPaused, setLiveUpdatesPaused] = useState(false);
  const [usePagination, setUsePagination] = useState(true);
  const [version] = useState<string | null>('Demo');
  const [isAuthenticated, setIsAuthenticated] = useState(false);
  const auth = { enabled: false, type: 'basic' as const };
  const logout = () => {
    setIsAuthenticated(false);
    toast.success('Logged out', { description: 'Demo mode - auth not actually changed' });
  };
  const handlePauseClick = () => setLiveUpdatesPaused(prev => !prev);

  useEffect(() => {
    const mediaQuery = window.matchMedia('(min-width: 768px)');
    const handleMediaChange = (e: { matches: boolean }) => {
      setUsePagination(e.matches);
      if (!e.matches) {
        setTorrentsPerPage(0);
      }
    };

    handleMediaChange(mediaQuery);
    mediaQuery.addEventListener('change', handleMediaChange);

    return () => {
      mediaQuery.removeEventListener('change', handleMediaChange);
    };
  }, []);

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

  const handleTitleFilterChange = (query: string) => {
    setTitleFilter(query);
  };

  const handleCategoryFilterChange = (value: string) => {
    setCategoryFilter(value === 'all' ? '' : value);
    setPage(1);
  };

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

  const totalPages = usePagination && torrentsPerPage > 0 ? Math.ceil(filteredAndSortedTorrents.length / torrentsPerPage) : 1;

  const paginatedTorrents = useMemo(() => {
    if (torrentsPerPage === 0) {
      return filteredAndSortedTorrents;
    }
    const start = (page - 1) * torrentsPerPage;
    const end = start + torrentsPerPage;
    return filteredAndSortedTorrents.slice(start, end);
  }, [filteredAndSortedTorrents, page, torrentsPerPage]);

  useEffect(() => {
    if (page > totalPages) {
      setPage(1);
    }
  }, [page, totalPages]);

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

  const handleSettingsClick = () => {
    updateModal('settings');
  };

  const handleMetricsClick = () => {
    updateModal('metrics');
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
        onSettingsClick={handleSettingsClick}
        onMetricsClick={handleMetricsClick}
        onTitleSearch={handleTitleFilterChange}
        liveUpdatesPaused={liveUpdatesPaused}
        handlePauseClick={handlePauseClick}
        version={version}
        isAuthenticated={isAuthenticated}
        logout={logout}
        auth={auth}
        isHidden={false}
      />
      <PageContainer>
        <TorrentControls
          torrentsData={{ torrents: demoTorrentsData }}
          torrents={categories}
          filteredAndSortedTorrents={filteredAndSortedTorrents}
          usePagination={usePagination}
          torrentsPerPage={torrentsPerPage}
          onTorrentsPerPageChange={value => { setTorrentsPerPage(Number(value)); setPage(1); }}
          categoryFilter={categoryFilter}
          onCategoryFilterChange={handleCategoryFilterChange}
          sortBy={sortBy}
          onSortByChange={setSortBy}
          onAddTorrent={() => updateModal('add')}
        />

        <TorrentGrid
          torrents={paginatedTorrents}
          onEdit={handleEdit}
          onViewStats={handleViewStats}
          onDelete={handleDelete}
          onPlay={handlePlay}
          onAddToDatabase={handleAddToDatabase}
        />

        {usePagination && torrentsPerPage > 0 && totalPages > 1 && (
          <div className='flex justify-center items-center gap-4 mt-8'>
            <Button onClick={() => setPage(page - 1)}
              disabled={page === 1}
              variant='outline'>
              Previous
            </Button>
            <span className='text-sm text-muted-foreground'>
              Page {page} of {totalPages}
            </span>
            <Button onClick={() => setPage(page + 1)}
              disabled={page === totalPages}
              variant='outline'>
              Next
            </Button>
          </div>
        )}
      </PageContainer>

      <DemoSettingsDialog
        open={modal === 'settings'}
        onOpenChange={(isOpen: boolean) => !isOpen && updateModal(null)}
      />
      <DemoMetricsDialog
        open={modal === 'metrics'}
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
