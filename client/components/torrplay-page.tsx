// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { App, URLOpenListenerEvent } from '@capacitor/app';
import { Filesystem } from '@capacitor/filesystem';
import { ScreenOrientation } from '@capacitor/screen-orientation';
import dynamic from 'next/dynamic';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import parseTorrent from 'parse-torrent';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { toast } from 'sonner';
import useSWR from 'swr';

import { AddTorrentDialog } from '@/components/add-torrent-dialog';
import { DeleteTorrentDialog } from '@/components/delete-torrent-dialog';
import { EditTorrentDialog } from '@/components/edit-torrent-dialog';
import { HandleTorrentDialog } from '@/components/handle-torrent-dialog';
import { Header } from '@/components/header';
import { LoginForm } from '@/components/login-form';
import { MetricsDialog } from '@/components/metrics-dialog';
import { PageContainer } from '@/components/page-container';
import { SettingsDialog } from '@/components/settings-dialog';
import { TorrentControls } from '@/components/torrent-controls';
import { TorrentGrid } from '@/components/torrent-grid';
import { TorrentStatsDialog } from '@/components/torrent-stats-dialog';
import { Button } from '@/components/ui/button';
import { useKeyboardNavigation } from '@/hooks/use-keyboard-navigation';
import {
  addTorrentFromMagnet,
  getTorrent,
  getTorrents,
} from '@/lib/api/torrents';
import { getApiBaseUrl, HttpError } from '@/lib/api-client';
import { useAuth } from '@/lib/auth-context';
import { useLiveUpdates } from '@/lib/live-updates-context';
import type { Torrent } from '@/lib/types/api';

const TorrentPlayerDialog = dynamic(
  () => import('@/components/torrent-player-dialog').then(mod => mod.TorrentPlayerDialog || mod.default),
  {
    ssr: false,
  }
);

interface CustomWindow extends Window {
  handleTorrentFileBase64?: (base64Data: string) => void
}

function getHashFromMagnet(magnetLink: string): string | null {
  const match = magnetLink.match(/xt=urn:btih:([a-fA-F0-9]{40})/);
  return match ? match[1] : null;
}

async function pollForTorrentReady(hash: string, timeout = 30000): Promise<Torrent> {
  const startTime = Date.now();
  while (Date.now() - startTime < timeout) {
    const torrent = await getTorrent(hash);
    if (torrent && torrent.files && torrent.files.length > 0) {
      return torrent;
    }
    await new Promise(resolve => setTimeout(resolve, 2000));
  }
  throw new Error('Torrent did not become ready in time. Failed to retrieve metadata.');
}

export default function TorrPlayPage({ homeHref }: { homeHref: string }) {
  const { isAuthenticated, isLoading: isAuthLoading } = useAuth();
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();

  const [titleFilter, setTitleFilter] = useState('');
  const [categoryFilter, setCategoryFilter] = useState('');
  const [sortBy, setSortBy] = useState('date');
  const [page, setPage] = useState(1);
  const [torrentsPerPage, setTorrentsPerPage] = useState(0);

  const [isOffline, setIsOffline] = useState(false);
  const [usePagination, setUsePagination] = useState(true);
  const { liveUpdatesPaused, setLiveUpdatesPaused } = useLiveUpdates();
  const errorCount = useRef(0);

  const [handlingMagnetLink, setHandlingMagnetLink] = useState<string | null>(null);
  const [handlingFile, setHandlingFile] = useState<File | null>(null);
  const [handlingType, setHandlingType] = useState<'magnet' | 'file' | null>(null);

  const [isProcessingFile, setIsProcessingFile] = useState(false);
  const isProcessingFileRef = useRef(isProcessingFile);
  useEffect(() => {
    isProcessingFileRef.current = isProcessingFile;
  }, [isProcessingFile]);

  const gridColumnCount = useRef(0);
  const headerRef = useRef<HTMLDivElement>(null);
  const topControlsRef = useRef<HTMLDivElement>(null);
  const mobileControlsRef = useRef<HTMLDivElement>(null);
  const gridRef = useRef<HTMLDivElement>(null);
  const paginationRef = useRef<HTMLDivElement>(null);
  const dispatchTorrentRef = useRef<typeof dispatchTorrent>(null);
  const handleTorrentFileURIRef = useRef<typeof handleTorrentFileURI>(null);

  const sections = useMemo(() => [
    { id: 'header', ref: headerRef, selector: 'a, button, input, [role="combobox"]' },
    { id: 'filters', ref: usePagination ? topControlsRef : mobileControlsRef, selector: 'button, [role="combobox"]' },
    { id: 'grid', ref: gridRef, selector: '[data-radix-collection-item]' },
    { id: 'pagination', ref: paginationRef, selector: 'button' },
  ], [usePagination]);

  useKeyboardNavigation(sections, () => gridColumnCount.current, usePagination);

  const modal = searchParams.get('modal');
  const hash = searchParams.get('hash');

  const { data: torrentsData, mutate, isLoading, error } = useSWR(
    isAuthenticated ? 'torrents' : null,
    () => getTorrents({}),
    {
      refreshInterval: liveUpdatesPaused || isOffline ? 0 : 5000,
      revalidateOnFocus: false,
      shouldRetryOnError: !isOffline,
      onError: () => {
        if (!isOffline) {
          errorCount.current++;
          if (errorCount.current >= 5) {
            setIsOffline(true);
            setLiveUpdatesPaused(true);
          }
        }
      },
      onSuccess: () => {
        if (isOffline) {
          setIsOffline(false);
        }
        errorCount.current = 0;
      }
    }
  );

  useEffect(() => {
    if (isOffline && !liveUpdatesPaused) {
      setIsOffline(false);
      errorCount.current = 0;
      mutate();
    }
  }, [isOffline, liveUpdatesPaused, mutate]);

  const selectedTorrent = useMemo(() => {
    const validModals = ['edit', 'stats', 'play', 'delete'];
    if (modal && validModals.includes(modal) && hash && torrentsData?.torrents) {
      return torrentsData.torrents.find(t => t.hash === hash) || null;
    }
    return null;
  }, [modal, hash, torrentsData]);

  const updateModal = useCallback((newModal: string | null, newHash: string | null = null) => {
    const params = new URLSearchParams(searchParams.toString());
    if (newModal) {
      params.set('modal', newModal);
    } else {
      params.delete('modal');
    }
    if (newHash) {
      params.set('hash', newHash);
    } else {
      params.delete('hash');
    }
    router.push(`${pathname}?${params.toString()}`, { scroll: false });
  }, [pathname, router, searchParams]);

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

  const handlePlay = useCallback((torrent: Torrent) => {
    updateModal('play', torrent.hash);
  }, [updateModal]);

  const handleMagnetAction = useCallback(async (magnetLink: string, save: boolean) => {
    const hash = getHashFromMagnet(magnetLink);
    if (!hash) {
      toast.error('Invalid magnet link');
      return;
    }
    const toastId = toast.loading(save ? 'Adding torrent...' : 'Fetching torrent metadata...');
    try {
      if (save) {
        try {
          await addTorrentFromMagnet(magnetLink);
          mutate();
        } catch (error) {
          if (!(error instanceof HttpError && error.status === 409)) {
            throw error;
          }
        }
      }
      toast.loading('Waiting for torrent metadata...', { id: toastId });
      const readyTorrent = await pollForTorrentReady(hash);
      toast.dismiss(toastId);
      handlePlay(readyTorrent);
    } catch (error) {
      toast.error(save ? 'Failed to add and play torrent' : 'Failed to play torrent', {
        id: toastId,
        description: error instanceof Error ? error.message : 'Could not start torrent.',
      });
    }
  }, [mutate, handlePlay]);

  const handleTorrentFileAction = useCallback(async (file: File, save: boolean) => {
    try {
      const buffer = await file.arrayBuffer();
      const parsed = await parseTorrent(Buffer.from(buffer));
      const hash = parsed.infoHash;
      if (!hash) {
        throw new Error('Could not parse torrent file to get info hash.');
      }
      const magnetLink = `magnet:?xt=urn:btih:${hash}`;
      await handleMagnetAction(magnetLink, save);
    } catch (error) {
      toast.error('Failed to process torrent file.', {
        description: error instanceof Error ? error.message : 'Unknown error.',
      });
    }
  }, [handleMagnetAction]);

  const dispatchTorrent = useCallback(async (type: 'magnet' | 'file', data: string | File) => {
    const choice = localStorage.getItem('torrent_handler_choice');
    const save = choice === 'add_and_play';

    if (choice) {
      if (type === 'magnet') {
        await handleMagnetAction(data as string, save);
      } else {
        await handleTorrentFileAction(data as File, save);
      }
    } else {
      setHandlingType(type);
      if (type === 'magnet') {
        setHandlingMagnetLink(data as string);
      } else {
        setHandlingFile(data as File);
      }
    }
  }, [handleMagnetAction, handleTorrentFileAction]);

  useEffect(() => {
    dispatchTorrentRef.current = dispatchTorrent;
  }, [dispatchTorrent]);

  const readTorrentFileFromURI = useCallback(async (uri: string): Promise<Blob | null> => {
    if (uri.startsWith('file://')) {
      const result = await Filesystem.readFile({ path: uri });
      if (typeof result.data === 'string') {
        const byteCharacters = atob(result.data);
        const byteNumbers = new Array(byteCharacters.length);
        for (let i = 0; i < byteCharacters.length; i++) {
          byteNumbers[i] = byteCharacters.charCodeAt(i);
        }
        const byteArray = new Uint8Array(byteNumbers);
        return new Blob([byteArray], { type: 'application/x-bittorrent' });
      }
      return result.data as Blob;
    } else if (uri.startsWith('content://')) {
      console.warn(`Attempted to read content:// URI from web layer. URI: ${uri}`);
      return null;
    }
    throw new Error('Unsupported URI scheme');
  }, []);

  const handleTorrentFileURI = useCallback(async (uri: string) => {
    if (isProcessingFileRef.current) {
      toast.info('Already processing a torrent file. Please wait.');
      return;
    }
    setIsProcessingFile(true);
    try {
      const blob = await readTorrentFileFromURI(uri);
      if (blob) {
        const file = new File([blob], 'torrent.torrent', { type: 'application/x-bittorrent' });
        await dispatchTorrent('file', file);
      }
    } catch (error) {
      toast.error('Failed to open torrent file', {
        description: error instanceof Error ? error.message : 'Could not read file.',
      });
    } finally {
      setIsProcessingFile(false);
    }
  }, [readTorrentFileFromURI, dispatchTorrent]);

  useEffect(() => {
    handleTorrentFileURIRef.current = handleTorrentFileURI;
  }, [handleTorrentFileURI]);

  useEffect(() => {
    const handleDeepLink = async (event: URLOpenListenerEvent) => {
      const url = event.url;
      if (url.startsWith('magnet:')) {
        const fn = dispatchTorrentRef.current;
        if (fn) await fn('magnet', url);
      } else if (url.startsWith('file://')) {
        const fn = handleTorrentFileURIRef.current;
        if (fn) await fn(url);
      }
    };
    const listenerPromise = App.addListener('appUrlOpen', handleDeepLink);
    App.getLaunchUrl().then(event => {
      if (event && event.url) {
        handleDeepLink(event as URLOpenListenerEvent);
      }
    });
    return () => {
      listenerPromise.then(listener => listener.remove());
    };
  }, []);

  useEffect(() => {
    const handleBase64 = async (base64Data: string) => {
      if (isProcessingFileRef.current) {
        toast.info('Already processing a torrent file. Please wait.');
        return;
      }
      setIsProcessingFile(true);
      try {
        const fetchRes = await fetch(`data:application/x-bittorrent;base64,${base64Data}`);
        const blob = await fetchRes.blob();
        const file = new File([blob], 'torrent.torrent', { type: 'application/x-bittorrent' });
        const fn = dispatchTorrentRef.current;
        if (fn) await fn('file', file);
      } catch (error) {
        toast.error('Failed to handle torrent file', {
          description: error instanceof Error ? error.message : 'Could not process Base64 data.',
        });
      } finally {
        setIsProcessingFile(false);
      }
    };
    (window as CustomWindow).handleTorrentFileBase64 = handleBase64;
    return () => {
      delete (window as CustomWindow).handleTorrentFileBase64;
    };
  }, []);

  useEffect(() => {
    const handleOrientation = (info: { type: string }) => {
      console.log('Screen orientation changed:', info.type);
    };

    const listenerPromise = ScreenOrientation.addListener('screenOrientationChange', handleOrientation);

    ScreenOrientation.orientation().then(info => {
      console.log('Initial screen orientation:', info.type);
    });

    return () => {
      listenerPromise.then(listener => listener.remove());
    };
  }, []);

  const categories = useMemo(() => {
    if (!torrentsData?.torrents) return [];
    const allCategories = torrentsData.torrents.map(t => t.category).filter(Boolean) as string[];
    return Array.from(new Set(allCategories)).sort();
  }, [torrentsData]);

  const handleTitleFilterChange = (query: string) => {
    setTitleFilter(query);
    setPage(1);
  };

  const handleCategoryFilterChange = (value: string) => {
    setCategoryFilter(value === 'all' ? '' : value);
    setPage(1);
  };

  const handleTorrentsPerPageChange = (value: string) => {
    setTorrentsPerPage(Number(value));
    setPage(1);
  };

  const openDeleteDialog = (torrent: Torrent) => {
    updateModal('delete', torrent.hash);
  };
  const handleAddToDatabase = async (torrent: Torrent) => {
    try {
      await addTorrentFromMagnet(torrent.magnet);
      toast.success('Torrent added to database');
      mutate(); // Re-fetch the torrents list
    } catch (error) {
      toast.error('Failed to add torrent to database', {
        description: error instanceof Error ? error.message : 'Unknown error',
      });
    }
  };
  const filteredAndSortedTorrents = useMemo(() => {
    const filtered = torrentsData?.torrents
      ? torrentsData.torrents.filter(torrent => {
        const titleMatch =
          !titleFilter ||
          (torrent.title || torrent.name || '')
            .toLowerCase()
            .includes(titleFilter.toLowerCase());
        const categoryMatch = !categoryFilter || (torrent.category || '') === categoryFilter;
        return titleMatch && categoryMatch;
      })
      : [];
    return filtered.slice().sort((a, b) => {
      const aInDb = a.createdAt !== undefined;
      const bInDb = b.createdAt !== undefined;

      if (aInDb !== bInDb) {
        return aInDb ? 1 : -1;
      }
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
  }, [torrentsData, titleFilter, categoryFilter, sortBy]);

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

  if (isAuthLoading) {
    return (
      <div className='flex justify-center items-center h-screen'>
        <p className='text-muted-foreground'>Loading...</p>
      </div>
    );
  }

  if (!isAuthenticated) {
    return <LoginForm />;
  }

  const renderContent = () => {
    if (isLoading && !torrentsData?.torrents.length) {
      return (
        <div className='text-center py-16'>
          <p className='text-muted-foreground'>Loading torrents...</p>
        </div>
      );
    }

    if (isOffline) {
      return (
        <div className='text-center py-16'>
          <p className='text-destructive mb-2'>Backend is offline</p>
          <p className='text-sm 4xl:text-md text-muted-foreground mt-2'>
            Live updates are paused. To reconnect, un-pause live updates.
          </p>
          <p className='text-sm 4xl:text-md text-muted-foreground mt-2'>API URL: {getApiBaseUrl()}</p>
        </div>
      );
    }
    if (error && !torrentsData) {
      return (
        <div className='text-center py-16'>
          <p className='text-destructive mb-2'>Could not connect to backend</p>
          <p className='text-sm 4xl:text-md text-muted-foreground mt-2'>
            Attempting to reconnect...
          </p>
          <p className='text-sm 4xl:text-md text-muted-foreground mt-2'>API URL: {getApiBaseUrl()}</p>
        </div>
      );
    }
    if (paginatedTorrents.length === 0) {
      return (
        <div className='text-center py-16 space-y-4'>
          <div>
            <h3 className='text-lg font-semibold mb-2'>
              {titleFilter || categoryFilter ? 'No torrents found' : 'No torrents yet'}
            </h3>
            <p className='text-muted-foreground text-sm mb-6'>
              {titleFilter || categoryFilter
                ? 'Try adjusting your filters or add a new torrent'
                : 'Get started by adding your first torrent using a magnet link, info hash or torrent file.'}
            </p>
          </div>
        </div>
      );
    }
    return (
      <TorrentGrid
        torrents={paginatedTorrents}
        onEdit={t => updateModal('edit', t.hash)}
        onViewStats={t => updateModal('stats', t.hash)}
        onDelete={openDeleteDialog}
        onPlay={handlePlay}
        onColumnCountChange={count => {
          gridColumnCount.current = count;
        }}
        onAddToDatabase={handleAddToDatabase}
      />
    );
  };

  const closeHandlingDialog = () => {
    setHandlingType(null);
    setHandlingMagnetLink(null);
    setHandlingFile(null);
  };

  const handleDialogAction = (action: 'play' | 'add_and_play', remember: boolean) => {
    if (remember) {
      localStorage.setItem('torrent_handler_choice', action);
    }
    const shouldSave = action === 'add_and_play';
    if (handlingType === 'magnet' && handlingMagnetLink) {
      handleMagnetAction(handlingMagnetLink, shouldSave);
    } else if (handlingType === 'file' && handlingFile) {
      handleTorrentFileAction(handlingFile, shouldSave);
    }
    closeHandlingDialog();
  };

  return (
    <>
      <Header
        homeHref={homeHref}
        ref={headerRef}
        onSettingsClick={() => updateModal('settings')}
        onMetricsClick={() => updateModal('metrics')}
        onTitleSearch={handleTitleFilterChange}
      />
      <PageContainer>
        <TorrentControls
          torrentsData={torrentsData}
          torrents={categories}
          filteredAndSortedTorrents={filteredAndSortedTorrents}
          torrentsPerPage={torrentsPerPage}
          onTorrentsPerPageChange={handleTorrentsPerPageChange}
          categoryFilter={categoryFilter}
          onCategoryFilterChange={handleCategoryFilterChange}
          sortBy={sortBy}
          onSortByChange={setSortBy}
          onAddTorrent={() => updateModal('add')}
        />

        <div ref={gridRef}>
          {renderContent()}
        </div>

        {usePagination && torrentsPerPage > 0 && totalPages > 1 && (
          <div ref={paginationRef}
            className='flex justify-center items-center gap-4 mt-8'>
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

      <SettingsDialog open={modal === 'settings'}
        onOpenChange={() => updateModal(null)} />
      <MetricsDialog open={modal === 'metrics'}
        onOpenChange={() => updateModal(null)} />
      <TorrentStatsDialog
        torrent={selectedTorrent}
        open={modal === 'stats' && !!selectedTorrent}
        onOpenChange={() => updateModal(null)}
      />
      <AddTorrentDialog
        open={modal === 'add'}
        onOpenChange={() => updateModal(null)}
        onSuccess={() => {
          mutate();
          updateModal(null);
        }}
      />
      <EditTorrentDialog
        torrent={selectedTorrent}
        open={modal === 'edit' && !!selectedTorrent}
        onOpenChange={() => updateModal(null)}
        onSuccess={() => {
          mutate();
          updateModal(null);
        }}
      />
      <TorrentPlayerDialog
        torrent={selectedTorrent}
        open={modal === 'play' && !!selectedTorrent}
        onOpenChange={() => updateModal(null)}
      />

      {handlingType && (
        <HandleTorrentDialog
          open={!!handlingType}
          type={handlingType}
          onOpenChange={open => !open && closeHandlingDialog()}
          onPlay={remember => handleDialogAction('play', remember)}
          onAddAndPlay={remember => handleDialogAction('add_and_play', remember)}
        />
      )}

      <DeleteTorrentDialog
        torrent={selectedTorrent}
        open={modal === 'delete' && !!selectedTorrent}
        onOpenChange={() => updateModal(null)}
        onSuccess={() => mutate()}
      />
    </>
  );
}
