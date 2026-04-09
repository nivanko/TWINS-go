import type { SliceCreator } from '../store.types';
import type { core } from '@wailsjs/go/models';
import { GetReceivingAddressesPage, ExportReceivingAddressesCSV } from '@wailsjs/go/main/App';

/**
 * Receiving Addresses slice
 * ----------------------------------------------------------------------------
 * Server-side paginated state for the Receiving Addresses dialog. Mirrors the
 * transactionsSlice pattern: only the current page is held in state, all
 * filtering/sorting/pagination happen on the Go backend via
 * `GetReceivingAddressesPage(filter)`.
 *
 * Naming convention: every field and action is prefixed with `recvAddrs` to
 * avoid name collisions when slices are merged into the combined store via
 * object spread. The transactions slice already owns short names like
 * `total`, `pageSize`, `fetchPage`, `setSortColumn`, etc., so colliding on
 * any of those would silently clobber its actions when this slice is loaded.
 *
 * Filter semantics (defined by the user task spec, enforced by the backend):
 *   - The enumeration always returns every wallet receiving address (labeled,
 *     used, and external keypool entries). There is no "show only labeled or
 *     funded" mode.
 *   - hideZeroBalance = false (default): no extra filter.
 *   - hideZeroBalance = true: drop rows whose balance is exactly 0.
 *
 * The default keeps the dialog's open-state behavior unchanged from the
 * legacy non-paginated view: everything visible until the user opts into
 * hiding zero-balance rows.
 */

export const RECV_ADDRS_PAGE_SIZES = [25, 50, 100, 250] as const;
export type RecvAddrsPageSize = (typeof RECV_ADDRS_PAGE_SIZES)[number];

/** Sortable columns for the receiving addresses dialog. */
export type RecvAddrsSortColumn = 'label' | 'balance';
export type RecvAddrsSortDirection = 'asc' | 'desc';

// localStorage key constants — keys are prefixed to avoid colliding with
// the transactions slice keys (which use the same `twins_` namespace).
//
// Note: previous versions of this slice also persisted `twins_recvAddrsShowAll`
// and `twins_recvAddrsShowZeroBalance`. Those keys are intentionally left
// orphaned in existing user browsers — no migration is run. They sit unused
// and have no effect on current state.
const STORAGE_KEY_PAGE_SIZE = 'twins_recvAddrsPageSize';
const STORAGE_KEY_SORT_COLUMN = 'twins_recvAddrsSortColumn';
const STORAGE_KEY_SORT_DIRECTION = 'twins_recvAddrsSortDirection';
const STORAGE_KEY_HIDE_ZERO_BALANCE = 'twins_recvAddrsHideZeroBalance';

const validSortColumns: RecvAddrsSortColumn[] = ['label', 'balance'];
const validSortDirections: RecvAddrsSortDirection[] = ['asc', 'desc'];

function loadPageSize(): RecvAddrsPageSize {
  try {
    const stored = localStorage.getItem(STORAGE_KEY_PAGE_SIZE);
    if (stored) {
      const num = parseInt(stored, 10);
      if ((RECV_ADDRS_PAGE_SIZES as readonly number[]).includes(num)) {
        return num as RecvAddrsPageSize;
      }
    }
  } catch {
    // Silently fall through to default
  }
  return 25;
}

function loadSortColumn(): RecvAddrsSortColumn {
  try {
    const stored = localStorage.getItem(STORAGE_KEY_SORT_COLUMN);
    if (stored && validSortColumns.includes(stored as RecvAddrsSortColumn)) {
      return stored as RecvAddrsSortColumn;
    }
  } catch {
    // Silently fall through to default
  }
  return 'balance';
}

function loadSortDirection(): RecvAddrsSortDirection {
  try {
    const stored = localStorage.getItem(STORAGE_KEY_SORT_DIRECTION);
    if (stored && validSortDirections.includes(stored as RecvAddrsSortDirection)) {
      return stored as RecvAddrsSortDirection;
    }
  } catch {
    // Silently fall through to default
  }
  return 'desc';
}

function loadBoolSetting(key: string, defaultValue: boolean): boolean {
  try {
    const stored = localStorage.getItem(key);
    if (stored === 'true') return true;
    if (stored === 'false') return false;
  } catch {
    // Silently fall through to default
  }
  return defaultValue;
}

function persist(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Silently fail — persistence is best-effort
  }
}

/**
 * Slice state.
 *
 * Note: `recvAddrsRows` carries `core.ReceivingAddressRow[]` from the Wails-
 * generated models. Field names match the Go JSON tags:
 *   - `address`, `label`, `balance`, `has_payment_request`, `created`
 */
export interface ReceivingAddressesState {
  // Current page data (from server)
  recvAddrsRows: core.ReceivingAddressRow[];
  recvAddrsTotal: number; // matching current filter
  recvAddrsTotalAll: number; // unfiltered wallet total
  recvAddrsTotalPages: number;
  recvAddrsIsLoading: boolean;
  recvAddrsError: string | null;

  // Pagination
  recvAddrsCurrentPage: number; // 1-based
  recvAddrsPageSize: RecvAddrsPageSize;

  // Filter state (sent to server)
  recvAddrsHideZeroBalance: boolean;
  recvAddrsSearchText: string;

  // Sort state (sent to server)
  recvAddrsSortColumn: RecvAddrsSortColumn;
  recvAddrsSortDirection: RecvAddrsSortDirection;
}

export interface ReceivingAddressesActions {
  // Data loading
  recvAddrsFetchPage: (page?: number) => Promise<void>;

  // Pagination
  recvAddrsSetPage: (page: number) => void;
  recvAddrsSetPageSize: (size: RecvAddrsPageSize) => void;
  recvAddrsGoToFirstPage: () => void;
  recvAddrsGoToPrevPage: () => void;
  recvAddrsGoToNextPage: () => void;
  recvAddrsGoToLastPage: () => void;

  // Filters (each resets to page 1 and fetches)
  recvAddrsSetHideZeroBalance: (value: boolean) => void;
  recvAddrsSetSearchText: (text: string) => void;

  // Sort
  recvAddrsSetSortColumn: (column: RecvAddrsSortColumn) => void;
  recvAddrsToggleSortDirection: () => void;

  // Reset transient state on dialog close (does not clear persisted prefs)
  recvAddrsResetTransient: () => void;

  // Export — server generates CSV for the full filtered set, opens save dialog
  recvAddrsExportCSV: () => Promise<boolean>;
}

export type ReceivingAddressesSlice = ReceivingAddressesState & ReceivingAddressesActions;

const initialState: ReceivingAddressesState = {
  recvAddrsRows: [],
  recvAddrsTotal: 0,
  recvAddrsTotalAll: 0,
  recvAddrsTotalPages: 0,
  recvAddrsIsLoading: false,
  recvAddrsError: null,

  recvAddrsCurrentPage: 1,
  recvAddrsPageSize: loadPageSize(),

  // Default: off. Matches the legacy open-state behavior where every wallet
  // receiving address (including zero-balance keypool entries) is visible.
  recvAddrsHideZeroBalance: loadBoolSetting(STORAGE_KEY_HIDE_ZERO_BALANCE, false),
  recvAddrsSearchText: '',

  recvAddrsSortColumn: loadSortColumn(),
  recvAddrsSortDirection: loadSortDirection(),
};

/**
 * Build the backend filter object from current slice state. Mirrors the
 * snake_case JSON shape produced by the Go `core.ReceivingAddressFilter`
 * struct so the call passes through Wails serialization unchanged.
 */
function buildFilter(
  state: ReceivingAddressesState,
  pageOverride?: number
): Record<string, unknown> {
  return {
    page: pageOverride ?? state.recvAddrsCurrentPage,
    page_size: state.recvAddrsPageSize,
    hide_zero_balance: state.recvAddrsHideZeroBalance,
    search_text: state.recvAddrsSearchText,
    sort_column: state.recvAddrsSortColumn,
    sort_direction: state.recvAddrsSortDirection,
  };
}

// Search input is debounced separately from filter checkboxes so the user
// can type without firing one fetch per keystroke. 300ms matches the
// transactions slice convention.
let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;

export const createReceivingAddressesSlice: SliceCreator<ReceivingAddressesSlice> = (set, get) => ({
  ...initialState,

  recvAddrsFetchPage: async (page?: number) => {
    const state = get();
    const targetPage = page ?? state.recvAddrsCurrentPage;

    set((s) => {
      s.recvAddrsIsLoading = true;
      s.recvAddrsError = null;
      if (page !== undefined) {
        s.recvAddrsCurrentPage = page;
      }
    });

    try {
      const filter = buildFilter(get(), targetPage);
      // Wails-generated TS expects a `core.ReceivingAddressFilter` instance;
      // a structurally-identical plain object passes through unchanged.
      const result = await GetReceivingAddressesPage(filter as never);

      set((s) => {
        s.recvAddrsRows = result.addresses || [];
        s.recvAddrsTotal = result.total;
        s.recvAddrsTotalAll = result.total_all;
        s.recvAddrsTotalPages = result.total_pages;
        s.recvAddrsCurrentPage = result.page;
        s.recvAddrsPageSize = result.page_size as RecvAddrsPageSize;
        s.recvAddrsIsLoading = false;
      });
    } catch (error) {
      console.error('Failed to fetch receiving addresses page:', error);
      set((s) => {
        s.recvAddrsError =
          error instanceof Error ? error.message : 'Failed to load receiving addresses';
        s.recvAddrsIsLoading = false;
      });
    }
  },

  // Pagination
  recvAddrsSetPage: (page: number) => {
    get().recvAddrsFetchPage(page);
  },

  recvAddrsSetPageSize: (size: RecvAddrsPageSize) => {
    set((s) => {
      s.recvAddrsPageSize = size;
      s.recvAddrsCurrentPage = 1;
    });
    persist(STORAGE_KEY_PAGE_SIZE, size.toString());
    get().recvAddrsFetchPage(1);
  },

  recvAddrsGoToFirstPage: () => {
    get().recvAddrsFetchPage(1);
  },

  recvAddrsGoToPrevPage: () => {
    const { recvAddrsCurrentPage } = get();
    if (recvAddrsCurrentPage > 1) {
      get().recvAddrsFetchPage(recvAddrsCurrentPage - 1);
    }
  },

  recvAddrsGoToNextPage: () => {
    const { recvAddrsCurrentPage, recvAddrsTotalPages } = get();
    if (recvAddrsCurrentPage < recvAddrsTotalPages) {
      get().recvAddrsFetchPage(recvAddrsCurrentPage + 1);
    }
  },

  recvAddrsGoToLastPage: () => {
    const { recvAddrsTotalPages } = get();
    if (recvAddrsTotalPages > 0) {
      get().recvAddrsFetchPage(recvAddrsTotalPages);
    }
  },

  // Filters
  recvAddrsSetHideZeroBalance: (value: boolean) => {
    set((s) => {
      s.recvAddrsHideZeroBalance = value;
      s.recvAddrsCurrentPage = 1;
    });
    persist(STORAGE_KEY_HIDE_ZERO_BALANCE, value.toString());
    get().recvAddrsFetchPage(1);
  },

  recvAddrsSetSearchText: (text: string) => {
    set((s) => {
      s.recvAddrsSearchText = text;
    });
    if (searchDebounceTimer) clearTimeout(searchDebounceTimer);
    searchDebounceTimer = setTimeout(() => {
      set((s) => {
        s.recvAddrsCurrentPage = 1;
      });
      get().recvAddrsFetchPage(1);
    }, 300);
  },

  // Sort
  recvAddrsSetSortColumn: (column: RecvAddrsSortColumn) => {
    set((s) => {
      if (s.recvAddrsSortColumn === column) {
        // Toggle direction when clicking the same column
        s.recvAddrsSortDirection = s.recvAddrsSortDirection === 'asc' ? 'desc' : 'asc';
      } else {
        s.recvAddrsSortColumn = column;
        // Sensible per-column default direction:
        //   label  -> asc (alphabetical)
        //   balance-> desc (largest first)
        s.recvAddrsSortDirection = column === 'label' ? 'asc' : 'desc';
      }
      s.recvAddrsCurrentPage = 1;
    });
    const next = get();
    persist(STORAGE_KEY_SORT_COLUMN, next.recvAddrsSortColumn);
    persist(STORAGE_KEY_SORT_DIRECTION, next.recvAddrsSortDirection);
    get().recvAddrsFetchPage(1);
  },

  recvAddrsToggleSortDirection: () => {
    set((s) => {
      s.recvAddrsSortDirection = s.recvAddrsSortDirection === 'asc' ? 'desc' : 'asc';
      s.recvAddrsCurrentPage = 1;
    });
    persist(STORAGE_KEY_SORT_DIRECTION, get().recvAddrsSortDirection);
    get().recvAddrsFetchPage(1);
  },

  recvAddrsResetTransient: () => {
    // Cancel pending debounce so a stale fetch can't fire after the dialog
    // closes and user prefs are touched. Search text is intentionally cleared
    // because it is not persisted to localStorage.
    if (searchDebounceTimer) {
      clearTimeout(searchDebounceTimer);
      searchDebounceTimer = null;
    }
    set((s) => {
      s.recvAddrsSearchText = '';
      s.recvAddrsError = null;
    });
  },

  recvAddrsExportCSV: async () => {
    try {
      const filter = buildFilter(get());
      const saved = await ExportReceivingAddressesCSV(filter as never);
      return saved;
    } catch (error) {
      console.error('Failed to export receiving addresses CSV:', error);
      set((s) => {
        s.recvAddrsError =
          error instanceof Error ? error.message : 'Failed to export receiving addresses';
      });
      return false;
    }
  },
});
