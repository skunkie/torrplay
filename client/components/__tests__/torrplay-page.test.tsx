// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import TorrPlayPage from '@/components/torrplay-page';
import { getTorrent, getTorrents } from '@/lib/api/torrents';
import { STORAGE_KEY_SORT_BY } from '@/lib/torrent-filter-storage';
import type { Torrent, TorrentsResponse } from '@/lib/types/api';

// The header, controls, and grid are stubbed so the tests drive the page's own
// search wiring: the stubs expose the props the page passes and the callbacks
// it hands back.
// The router keeps the pushed query as the search params, a deep link reaches
// the page through the captured listener, and the player, the page's only
// dynamic component, exposes the props it gets.
const page = vi.hoisted(() => {
  const state = {
    controls: {} as Record<string, unknown>,
    openUrl: undefined as ((event: { url: string }) => Promise<void>) | undefined,
    params: new URLSearchParams(),
    player: {} as Record<string, unknown>,
    push: vi.fn((url: string) => {
      state.params = new URLSearchParams(url.split('?')[1] ?? '');
    }),
    replace: vi.fn(),
  };
  return state;
});

vi.mock('next/navigation', () => ({
  usePathname: () => '/',
  useRouter: () => ({ push: page.push, replace: page.replace }),
  useSearchParams: () => page.params,
}));

vi.mock('next/dynamic', () => ({
  default: () => (props: Record<string, unknown>) => {
    page.player = props;
    return null;
  },
}));

vi.mock('@capacitor/app', () => ({
  App: {
    addListener: (_event: string, listener: (event: { url: string }) => Promise<void>) => {
      page.openUrl = listener;
      return Promise.resolve({ remove: () => {} });
    },
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

// The facets describe the library, every torrent on the server, whatever the
// search matched, and version it as the server does.
const categories = [{ count: 2, name: 'Fantasy' }, { count: 1, name: 'Sci-Fi' }];

function response(torrents: Torrent[], library: Torrent[] = all): TorrentsResponse {
  const facets = { categories, total: library.length, version: library.map(t => t.hash).join() };
  return { facets, limit: 0, offset: 0, torrents, total: torrents.length };
}

// The list, date, name, size, and relevance orders all differ, so each
// assertion on the shown order proves which order the page applied.
const sintel = torrent('a', 'Sintel', '2026-01-02T00:00:00Z', 1);
const spring = torrent('b', 'Spring', '2026-01-01T00:00:00Z', 3);
const tearsOfSteel = torrent('c', 'Tears of Steel', '2026-01-03T00:00:00Z', 2);
const all = [spring, sintel, tearsOfSteel];

function renderPage() {
  const tree = () => (
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <TorrPlayPage homeHref='/' />
    </SWRConfig>
  );
  const view = render(tree());
  return { ...view, rerender: () => view.rerender(tree()) };
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
    page.params = new URLSearchParams();
    page.player = {};
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
    const library = [spring, tearsOfSteel];
    mockGetTorrents.mockImplementation(async params => {
      if (!params?.search) {
        return response(library, library);
      }
      return response(params.search === 's' ? [tearsOfSteel, spring] : [], library);
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
    await act(async () => answer(response([], library)));
  });

  it('matches the newest known torrents while a cached search revalidates', async () => {
    renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));
    search('s');
    await waitFor(() => expect(shownTitles()).toEqual(['Tears of Steel', 'Spring', 'Sintel']));
    // Sintel is deleted on the server.
    const library = [spring, tearsOfSteel];
    mockGetTorrents.mockImplementation(async params => response(params?.search ? [] : library, library));
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
    // Sintel is deleted on the server. A search fetches the changed full list
    // once, and the next full list does not answer.
    const library = [spring, tearsOfSteel];
    let fullLists = 0;
    mockGetTorrents.mockImplementation(params => {
      if (params?.search) {
        return Promise.resolve(response([], library));
      }
      return ++fullLists === 1 ? Promise.resolve(response(library, library)) : new Promise(() => {});
    });
    search('sintel');
    await waitFor(() => expect(searchCalls()).toContain('sintel'));
    await waitFor(() => expect(page.controls).toMatchObject({ isSearchPending: false, torrentCount: 2 }));

    search('');

    expect(shownTitles()).toEqual(['Tears of Steel', 'Spring']);
    expect(page.controls).toMatchObject({ torrentCount: 2 });
  });

  it('shows the matches before a changed full list arrives and shows that list once it does', async () => {
    renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));
    // Sintel is deleted on the server. The full list fetched for the search
    // answers only when told, and the one for the cleared search never does.
    const library = [spring, tearsOfSteel];
    let answerFull: () => void = () => {};
    let fullLists = 0;
    mockGetTorrents.mockImplementation(params => {
      if (params?.search) {
        return Promise.resolve(response(params.search === 'spr' ? [spring] : [], library));
      }
      return ++fullLists === 1
        ? new Promise(resolve => {
          answerFull = () => resolve(response(library, library));
        })
        : new Promise(() => {});
    });

    search('spr');
    await waitFor(() => expect(page.controls).toMatchObject({ isSearchPending: false }));
    expect(shownTitles()).toEqual(['Spring']);

    search('');
    expect(shownTitles()).toEqual(['Tears of Steel', 'Sintel', 'Spring']);

    await act(async () => answerFull());

    expect(shownTitles()).toEqual(['Tears of Steel', 'Spring']);
  });

  it('opens the player for a torrent added and played while searching', async () => {
    const extra = torrent('d'.repeat(40), 'Spring Extras', '2026-01-04T00:00:00Z', 4);
    localStorage.setItem('torrent_handler_choice', 'add_and_play');
    vi.mocked(getTorrent).mockResolvedValue({
      ...extra,
      files: [{ length: 4, name: 'Spring Extras.mp4', path: 'Spring Extras.mp4' }],
    });
    const view = renderPage();
    await waitFor(() => expect(shownTitles()).toHaveLength(3));
    search('spr');
    await waitFor(() => expect(page.controls).toMatchObject({ isSearchPending: false }));
    // The added torrent is on the server, whose full list does not answer, so
    // the known list still lacks it.
    const library = [...all, extra];
    mockGetTorrents.mockImplementation(params => params?.search
      ? Promise.resolve(response([spring, extra], library))
      : new Promise(() => {}));

    await act(async () => page.openUrl?.({ url: extra.magnet }));
    await waitFor(() => expect(page.push).toHaveBeenCalledWith(`/?modal=play&hash=${extra.hash}`, { scroll: false }));
    await waitFor(() => expect(searchCalls().filter(call => call === 'spr')).toHaveLength(2));
    view.rerender();

    await waitFor(() => expect(page.player).toMatchObject({ open: true, torrent: { hash: extra.hash } }));
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
