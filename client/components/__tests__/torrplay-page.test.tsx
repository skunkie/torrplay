// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import TorrPlayPage from '@/components/torrplay-page';
import { getTorrents } from '@/lib/api/torrents';
import { STORAGE_KEY_SORT_BY } from '@/lib/torrent-filter-storage';
import type { Torrent, TorrentsResponse } from '@/lib/types/api';

// The header, controls, and grid are stubbed so the tests drive the page's own
// search wiring: the stubs expose the props the page passes and the callbacks
// it hands back.
const page = vi.hoisted(() => ({
  controls: {} as Record<string, unknown>,
  replace: vi.fn(),
}));

vi.mock('next/navigation', () => {
  const searchParams = new URLSearchParams();
  return {
    usePathname: () => '/',
    useRouter: () => ({ push: vi.fn(), replace: page.replace }),
    useSearchParams: () => searchParams,
  };
});

vi.mock('next/dynamic', () => ({ default: () => () => null }));

vi.mock('@capacitor/app', () => ({
  App: {
    addListener: () => Promise.resolve({ remove: () => {} }),
    getLaunchUrl: () => Promise.resolve(undefined),
  },
}));

vi.mock('@capacitor/filesystem', () => ({ Filesystem: {} }));
vi.mock('parse-torrent', () => ({ default: vi.fn() }));

vi.mock('@/lib/auth-context', () => ({
  useAuth: () => ({ isAuthenticated: true, isLoading: false }),
}));

vi.mock('@/lib/app-update-context', () => ({
  useOptionalAppUpdate: () => null,
}));

vi.mock('@/lib/live-updates-context', () => ({
  useLiveUpdates: () => ({ liveUpdatesPaused: false, setLiveUpdatesPaused: vi.fn() }),
}));

vi.mock('@/lib/api/torrents', () => ({
  addTorrentFromMagnet: vi.fn(),
  getTorrent: vi.fn(),
  getTorrents: vi.fn(),
}));

vi.mock('@/components/header', () => ({
  Header: ({ onTitleSearch, searchQuery }: { onTitleSearch: (query: string) => void, searchQuery: string }) => (
    <input aria-label='Search'
      value={searchQuery}
      onChange={event => onTitleSearch(event.target.value)} />
  ),
}));

vi.mock('@/components/torrent-controls', () => ({
  TorrentControls: (props: Record<string, unknown>) => {
    page.controls = props;
    return null;
  },
}));

vi.mock('@/components/torrent-grid', () => ({
  TorrentGrid: ({ torrents }: { torrents: Torrent[] }) => (
    <ul aria-label='Torrents'>
      {torrents.map(t => <li key={t.hash}>{t.title}</li>)}
    </ul>
  ),
}));

vi.mock('@/components/add-torrent-dialog', () => ({ AddTorrentDialog: () => null }));
vi.mock('@/components/delete-torrent-dialog', () => ({ DeleteTorrentDialog: () => null }));
vi.mock('@/components/edit-torrent-dialog', () => ({ EditTorrentDialog: () => null }));
vi.mock('@/components/handle-torrent-dialog', () => ({ HandleTorrentDialog: () => null }));
vi.mock('@/components/metrics-dialog', () => ({ MetricsDialog: () => null }));
vi.mock('@/components/settings-dialog', () => ({ SettingsDialog: () => null }));
vi.mock('@/components/system-info-dialog', () => ({ SystemInfoDialog: () => null }));
vi.mock('@/components/torrent-stats-dialog', () => ({ TorrentStatsDialog: () => null }));
vi.mock('@/components/update-dialog', () => ({ UpdateDialog: () => null }));

const mockGetTorrents = vi.mocked(getTorrents);

function torrent(hash: string, title: string, createdAt: string, totalSize: number): Torrent {
  return {
    active: false,
    createdAt,
    hash,
    magnet: `magnet:?xt=urn:btih:${hash}`,
    name: title,
    pieceCount: 1,
    pieceSize: 1,
    storage: 'memory',
    title,
    totalSize,
  } as Torrent;
}

// The facets describe every torrent, whatever the search matched.
const facets = { categories: [{ count: 2, name: 'Fantasy' }, { count: 1, name: 'Sci-Fi' }], total: 3 };

function response(torrents: Torrent[]): TorrentsResponse {
  return { facets, limit: 0, offset: 0, torrents, total: torrents.length };
}

// The list, date, name, size, and relevance orders all differ, so each
// assertion on the shown order proves which order the page applied.
const sintel = torrent('a', 'Sintel', '2026-01-02T00:00:00Z', 1);
const spring = torrent('b', 'Spring', '2026-01-01T00:00:00Z', 3);
const tearsOfSteel = torrent('c', 'Tears of Steel', '2026-01-03T00:00:00Z', 2);
const all = [spring, sintel, tearsOfSteel];

function renderPage() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <TorrPlayPage homeHref='/' />
    </SWRConfig>,
  );
}

function shownTitles(): string[] {
  return screen.queryAllByRole('listitem').map(item => item.textContent ?? '');
}

function searchCalls(): (string | undefined)[] {
  return mockGetTorrents.mock.calls.map(([params]) => params?.search).filter(Boolean);
}

function search(query: string) {
  fireEvent.change(screen.getByLabelText('Search'), { target: { value: query } });
}

describe('TorrPlayPage search', () => {
  beforeEach(() => {
    page.controls = {};
    mockGetTorrents.mockImplementation(async params => {
      if (!params?.search) {
        return response(all);
      }
      // The server answers a search in its own relevance order.
      return response(params.search === 's' ? [tearsOfSteel, spring, sintel] : [spring]);
    });
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('lists every torrent newest first without a search', async () => {
    renderPage();

    await waitFor(() => expect(shownTitles()).toEqual(['Tears of Steel', 'Sintel', 'Spring']));
    expect(searchCalls()).toEqual([]);
    expect(page.controls).toMatchObject({ isSearching: false, isSearchPending: false, sortBy: 'date' });
  });

  it('requests only the settled query after the debounce', async () => {
    renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));

    search('S');
    search('Sp');
    search('Spr');

    await waitFor(() => expect(searchCalls()).toEqual(['Spr']));
    expect(mockGetTorrents).toHaveBeenCalledWith({ fuzzy: true, search: 'Spr' });
  });

  it('matches titles locally until the server answers the query', async () => {
    let answer: (value: TorrentsResponse) => void = () => {};
    renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));
    mockGetTorrents.mockImplementation(params => params?.search
      ? new Promise(resolve => {
        answer = resolve;
      })
      : Promise.resolve(response(all)));

    search('  spr ');

    expect(shownTitles()).toEqual(['Spring']);
    expect(page.controls).toMatchObject({ isSearching: true, isSearchPending: true, sortBy: 'relevance' });
    await waitFor(() => expect(searchCalls()).toEqual(['spr']));

    await act(async () => answer(response([sintel, tearsOfSteel, spring])));

    await waitFor(() => expect(shownTitles()).toEqual(['Sintel', 'Tears of Steel', 'Spring']));
    expect(page.controls).toMatchObject({ isSearchPending: false });
  });

  it('keeps the server order for relevance and sorts matches without storing the order', async () => {
    localStorage.setItem(STORAGE_KEY_SORT_BY, 'size');
    renderPage();
    await waitFor(() => expect(shownTitles()).toEqual(['Spring', 'Tears of Steel', 'Sintel']));

    search('s');
    await waitFor(() => expect(shownTitles()).toEqual(['Tears of Steel', 'Spring', 'Sintel']));

    act(() => (page.controls.onSortByChange as (value: string) => void)('name'));

    expect(shownTitles()).toEqual(['Sintel', 'Spring', 'Tears of Steel']);
    expect(page.controls).toMatchObject({ sortBy: 'name' });
    expect(page.replace).not.toHaveBeenCalled();
    expect(localStorage.getItem(STORAGE_KEY_SORT_BY)).toBe('size');

    search('');

    await waitFor(() => expect(shownTitles()).toEqual(['Spring', 'Tears of Steel', 'Sintel']));
    expect(page.controls).toMatchObject({ isSearching: false, sortBy: 'size' });

    search('s');

    await waitFor(() => expect(page.controls).toMatchObject({ sortBy: 'relevance' }));
  });

  it('makes a single request per refresh while searching', async () => {
    renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));
    mockGetTorrents.mockClear();

    search('spr');

    await waitFor(() => expect(searchCalls()).toEqual(['spr']));
    await waitFor(() => expect(page.controls).toMatchObject({ isSearchPending: false }));
    expect(shownTitles()).toEqual(['Spring']);
    expect(mockGetTorrents.mock.calls).toEqual([[{ fuzzy: true, search: 'spr' }]]);
    expect(page.controls).toMatchObject({ torrentCount: 3, torrents: ['Fantasy', 'Sci-Fi'] });
  });

  it('matches every known torrent locally after a search narrowed the response', async () => {
    let answer: (value: TorrentsResponse) => void = () => {};
    renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));
    search('spr');
    await waitFor(() => expect(shownTitles()).toEqual(['Spring']));
    mockGetTorrents.mockImplementation(() => new Promise(resolve => {
      answer = resolve;
    }));

    search('steel');

    expect(shownTitles()).toEqual(['Tears of Steel']);
    await waitFor(() => expect(searchCalls()).toContain('steel'));
    await act(async () => answer(response([tearsOfSteel])));
    expect(shownTitles()).toEqual(['Tears of Steel']);
  });

  it('does not restore a deleted torrent from an earlier search', async () => {
    renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));
    search('s');
    await waitFor(() => expect(shownTitles()).toEqual(['Tears of Steel', 'Spring', 'Sintel']));
    // Sintel is deleted on the server.
    mockGetTorrents.mockImplementation(async params => {
      if (!params?.search) {
        return response([spring, tearsOfSteel]);
      }
      return response(params.search === 's' ? [tearsOfSteel, spring] : []);
    });

    search('si');
    await waitFor(() => expect(searchCalls()).toContain('si'));
    await waitFor(() => expect(page.controls).toMatchObject({ isSearchPending: false }));
    // Going back serves the cached answer to "s", which still lists Sintel.
    search('s');
    await waitFor(() => expect(shownTitles()).toEqual(['Tears of Steel', 'Spring']));
    let answer: (value: TorrentsResponse) => void = () => {};
    mockGetTorrents.mockImplementation(() => new Promise(resolve => {
      answer = resolve;
    }));

    search('sin');

    expect(shownTitles()).toEqual([]);
    await waitFor(() => expect(searchCalls()).toContain('sin'));
    await act(async () => answer(response([])));
  });

  it('matches the newest known torrents while a cached search revalidates', async () => {
    renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));
    search('s');
    await waitFor(() => expect(shownTitles()).toEqual(['Tears of Steel', 'Spring', 'Sintel']));
    // Sintel is deleted on the server.
    mockGetTorrents.mockImplementation(async () => response([]));
    search('si');
    await waitFor(() => expect(searchCalls()).toContain('si'));
    await waitFor(() => expect(page.controls).toMatchObject({ isSearchPending: false }));
    // Going back serves the cached answer to "s", which still lists Sintel,
    // and its revalidation never answers.
    mockGetTorrents.mockImplementation(() => new Promise(() => {}));
    search('s');
    await waitFor(() => expect(shownTitles()).toEqual(['Tears of Steel', 'Spring', 'Sintel']));
    await waitFor(() => expect(searchCalls().filter(call => call === 's')).toHaveLength(2));

    search('sin');

    expect(shownTitles()).toEqual([]);
  });

  it('counts the shown torrents while a cleared search awaits the full list', async () => {
    renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));
    search('spr');
    await waitFor(() => expect(searchCalls()).toEqual(['spr']));
    await waitFor(() => expect(page.controls).toMatchObject({ isSearchPending: false }));
    // Sintel is deleted and another torrent added on the server, and the full
    // list has not answered yet.
    mockGetTorrents.mockImplementation(params => params?.search
      ? Promise.resolve(response([]))
      : new Promise(() => {}));
    search('sintel');
    await waitFor(() => expect(searchCalls()).toContain('sintel'));
    await waitFor(() => expect(page.controls).toMatchObject({ isSearchPending: false, torrentCount: 3 }));

    search('');

    expect(shownTitles()).toEqual(['Tears of Steel', 'Spring']);
    expect(page.controls).toMatchObject({ torrentCount: 2 });
  });

  it('stops waiting for a search that fails', async () => {
    let fail: (reason: Error) => void = () => {};
    renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));
    mockGetTorrents.mockImplementation(params => params?.search
      ? new Promise((_, reject) => {
        fail = reject;
      })
      : Promise.resolve(response(all)));

    search('Llamaland');

    expect(screen.getByText('Searching torrents...')).toBeInTheDocument();
    await waitFor(() => expect(searchCalls()).toEqual(['Llamaland']));

    await act(async () => fail(new Error('network down')));

    await waitFor(() => expect(screen.getByText('No torrents found')).toBeInTheDocument());
    expect(page.controls).toMatchObject({ isSearchPending: false });
  });
});
