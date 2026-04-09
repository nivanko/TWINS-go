import type { SliceCreator } from '../store.types';
import { core } from '@/shared/types/wallet.types';
import { GetTransactionsPage, ExportFilteredTransactionsCSV } from '@wailsjs/go/main/App';

/**
 * Transaction filter types - kept identical for dropdown compatibility
 */
export type DateFilter = 'all' | 'today' | 'week' | 'month' | 'lastMonth' | 'year' | 'range';
export type TypeFilter =
  | 'all'
  | 'mostCommon'
  | 'received'
  | 'sent'
  | 'toYourself'
  | 'mined'
  | 'minted'
  | 'masternode'
  | 'consolidation'
  | 'other';
export type WatchOnlyFilter = 'all' | 'yes' | 'no';
export type SortColumn = 'date' | 'type' | 'address' | 'amount';
export type SortDirection = 'asc' | 'desc';

/**
 * Valid page sizes for the page size selector
 */
export const PAGE_SIZES = [25, 50, 100, 250] as const;
export type PageSize = typeof PAGE_SIZES[number];

/**
 * Transactions State
 * Server-side paginated: only the current page is held in state.
 * All filtering, sorting, and pagination happen on the Go backend.
 *
 * Note: selectedTxids uses Record<string, boolean> instead of Set<string>
 * because immer middleware doesn't properly track Set mutations.
 */
export interface TransactionsState {
  // Current page data (from server)
  transactions: core.Transaction[];
  total: number;        // total matching current filter
  totalAll: number;     // total in wallet (unfiltered)
  totalPages: number;
  isLoadingTransactions: boolean;
  transactionsError: string | null;

  // Pagination
  currentPage: number;   // 1-based
  pageSize: PageSize;

  // Filter state (sent to server)
  dateFilter: DateFilter;
  typeFilter: TypeFilter;
  searchText: string;
  minAmount: string;
  dateRangeFrom: string; // ISO date string
  dateRangeTo: string;   // ISO date string
  watchOnlyFilter: WatchOnlyFilter;
  hasWatchOnlyAddresses: boolean;
  hideOrphanStakes: boolean;

  // Sort state (sent to server)
  sortColumn: SortColumn;
  sortDirection: SortDirection;

  // Selection state - current page only
  selectedTxids: Record<string, boolean>;

  // New transaction notification
  newTransactionCount: number;

  // Block explorer URLs (parsed from strThirdPartyTxUrls setting)
  blockExplorerUrls: BlockExplorerUrl[];
}

/**
 * Parsed block explorer URL
 */
export interface BlockExplorerUrl {
  url: string;
  hostname: string;
}

/**
 * Transactions Actions
 */
export interface TransactionsActions {
  // Data loading - fetches current page from server
  fetchPage: (page?: number) => Promise<void>;

  // Pagination actions
  setPage: (page: number) => void;
  setPageSize: (size: PageSize) => void;
  goToFirstPage: () => void;
  goToLastPage: () => void;
  goToPrevPage: () => void;
  goToNextPage: () => void;

  // Filter actions - each resets to page 1 and fetches
  setDateFilter: (filter: DateFilter) => void;
  setTypeFilter: (filter: TypeFilter) => void;
  setSearchText: (text: string) => void;
  setMinAmount: (amount: string) => void;
  setDateRange: (from: string, to: string) => void;
  setWatchOnlyFilter: (filter: WatchOnlyFilter) => void;
  syncHideOrphanStakes: () => Promise<void>;
  clearFilters: () => void;

  // Sort actions
  setSortColumn: (column: SortColumn) => void;
  toggleSortDirection: () => void;

  // Selection actions
  toggleSelection: (key: string) => void; // key = "txid:vout"
  selectAll: () => void;
  unselectAll: () => void;
  isSelected: (key: string) => boolean; // key = "txid:vout"

  // Computed getters
  getSelectedAmount: () => number;
  getSelectedCount: () => number;

  // Export (server-side CSV generation)
  exportCSV: () => Promise<boolean>;

  // Notification
  incrementNewTransactionCount: () => void;
  clearNewTransactionCount: () => void;

  // Block explorer URLs
  syncBlockExplorerUrls: () => Promise<void>;
}

export type TransactionsSlice = TransactionsState & TransactionsActions;

// Helper to get default date range (last 7 days to today)
function getDefaultDateRange(): { from: string; to: string } {
  const today = new Date();
  const lastWeek = new Date(today);
  lastWeek.setDate(lastWeek.getDate() - 7);
  return {
    from: lastWeek.toISOString().split('T')[0],
    to: today.toISOString().split('T')[0],
  };
}

// localStorage key constants
const STORAGE_KEY_DATE_FILTER = 'twins_transactionDateFilter';
const STORAGE_KEY_TYPE_FILTER = 'twins_transactionTypeFilter';
const STORAGE_KEY_PAGE_SIZE = 'twins_transactionPageSize';

// Valid filter values for validation
const validDateFilters: DateFilter[] = ['all', 'today', 'week', 'month', 'lastMonth', 'year', 'range'];
const validTypeFilters: TypeFilter[] = [
  'all', 'mostCommon', 'received', 'sent', 'toYourself', 'mined', 'minted',
  'masternode', 'consolidation', 'other'
];

function loadDateFilter(): DateFilter {
  try {
    const stored = localStorage.getItem(STORAGE_KEY_DATE_FILTER);
    if (stored && validDateFilters.includes(stored as DateFilter)) {
      return stored as DateFilter;
    }
  } catch {
    // Silently fail
  }
  return 'all';
}

function loadTypeFilter(): TypeFilter {
  try {
    const stored = localStorage.getItem(STORAGE_KEY_TYPE_FILTER);
    if (stored && validTypeFilters.includes(stored as TypeFilter)) {
      return stored as TypeFilter;
    }
  } catch {
    // Silently fail
  }
  return 'all';
}

function loadPageSize(): PageSize {
  try {
    const stored = localStorage.getItem(STORAGE_KEY_PAGE_SIZE);
    if (stored) {
      const num = parseInt(stored, 10);
      if ((PAGE_SIZES as readonly number[]).includes(num)) {
        return num as PageSize;
      }
    }
  } catch {
    // Silently fail
  }
  return 25;
}

/**
 * Parse third-party transaction URLs from settings.
 * Format: pipe-separated URLs with %s placeholder for txid.
 */
function parseBlockExplorerUrls(urlsString: string): BlockExplorerUrl[] {
  if (!urlsString) return [];

  return urlsString
    .split('|')
    .map(url => url.trim())
    .filter(url => url && url.includes('%s'))
    .map(url => {
      try {
        const parsed = new URL(url.replace('%s', 'placeholder'));
        return { url, hostname: parsed.hostname };
      } catch {
        return null;
      }
    })
    .filter((item): item is BlockExplorerUrl => item !== null);
}

const defaultDateRange = getDefaultDateRange();

// Initial state
const initialState: TransactionsState = {
  transactions: [],
  total: 0,
  totalAll: 0,
  totalPages: 0,
  isLoadingTransactions: false,
  transactionsError: null,

  currentPage: 1,
  pageSize: loadPageSize(),

  dateFilter: loadDateFilter(),
  typeFilter: loadTypeFilter(),
  searchText: '',
  minAmount: '',

  dateRangeFrom: defaultDateRange.from,
  dateRangeTo: defaultDateRange.to,

  watchOnlyFilter: 'all',
  hasWatchOnlyAddresses: false,

  hideOrphanStakes: false, // Synced from GUISettings via syncHideOrphanStakes()

  sortColumn: 'date',
  sortDirection: 'desc',

  selectedTxids: {},

  newTransactionCount: 0,

  blockExplorerUrls: [],
};

/**
 * Build a TransactionFilter from current state for the backend call
 */
function buildFilter(state: TransactionsState, pageOverride?: number): Record<string, unknown> {
  return {
    page: pageOverride ?? state.currentPage,
    page_size: state.pageSize,
    date_filter: state.dateFilter,
    date_range_from: state.dateFilter === 'range' ? state.dateRangeFrom : '',
    date_range_to: state.dateFilter === 'range' ? state.dateRangeTo : '',
    type_filter: state.typeFilter,
    search_text: state.searchText,
    min_amount: state.minAmount ? parseFloat(state.minAmount) || 0 : 0,
    watch_only_filter: state.watchOnlyFilter,
    hide_orphan_stakes: state.hideOrphanStakes,
    sort_column: state.sortColumn,
    sort_direction: state.sortDirection,
  };
}

// Separate debounce timers for search text and min amount
let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;
let amountDebounceTimer: ReturnType<typeof setTimeout> | null = null;

export const createTransactionsSlice: SliceCreator<TransactionsSlice> = (set, get) => ({
  ...initialState,

  // Fetch a page from the server
  fetchPage: async (page?: number) => {
    const state = get();
    const targetPage = page ?? state.currentPage;

    set((s) => {
      s.isLoadingTransactions = true;
      s.transactionsError = null;
      if (page !== undefined) {
        s.currentPage = page;
      }
    });

    try {
      const filter = buildFilter(get(), targetPage);
      const result = await GetTransactionsPage(filter as any);

      set((s) => {
        s.transactions = result.transactions || [];
        s.total = result.total;
        s.totalAll = result.total_all;
        s.totalPages = result.total_pages;
        s.currentPage = result.page;
        s.pageSize = result.page_size as PageSize;
        s.isLoadingTransactions = false;
        // Clear selection when page changes
        s.selectedTxids = {};
      });
    } catch (error) {
      console.error('Failed to fetch transactions page:', error);
      set((s) => {
        s.transactionsError = error instanceof Error ? error.message : 'Failed to load transactions';
        s.isLoadingTransactions = false;
      });
    }
  },

  // Pagination actions
  setPage: (page: number) => {
    get().fetchPage(page);
  },

  setPageSize: (size: PageSize) => {
    set((s) => {
      s.pageSize = size;
      s.currentPage = 1;
    });
    try {
      localStorage.setItem(STORAGE_KEY_PAGE_SIZE, size.toString());
    } catch {
      // Silently fail
    }
    get().fetchPage(1);
  },

  goToFirstPage: () => {
    get().fetchPage(1);
  },

  goToLastPage: () => {
    const { totalPages } = get();
    if (totalPages > 0) {
      get().fetchPage(totalPages);
    }
  },

  goToPrevPage: () => {
    const { currentPage } = get();
    if (currentPage > 1) {
      get().fetchPage(currentPage - 1);
    }
  },

  goToNextPage: () => {
    const { currentPage, totalPages } = get();
    if (currentPage < totalPages) {
      get().fetchPage(currentPage + 1);
    }
  },

  // Filter actions - each resets to page 1 and fetches
  setDateFilter: (filter: DateFilter) => {
    set((s) => {
      s.dateFilter = filter;
      s.currentPage = 1;
    });
    try {
      localStorage.setItem(STORAGE_KEY_DATE_FILTER, filter);
    } catch {
      // Silently fail
    }
    get().fetchPage(1);
  },

  setTypeFilter: (filter: TypeFilter) => {
    set((s) => {
      s.typeFilter = filter;
      s.currentPage = 1;
    });
    try {
      localStorage.setItem(STORAGE_KEY_TYPE_FILTER, filter);
    } catch {
      // Silently fail
    }
    get().fetchPage(1);
  },

  setSearchText: (text: string) => {
    set((s) => {
      s.searchText = text;
    });
    // Debounce search: wait 300ms before fetching
    if (searchDebounceTimer) clearTimeout(searchDebounceTimer);
    searchDebounceTimer = setTimeout(() => {
      set((s) => { s.currentPage = 1; });
      get().fetchPage(1);
    }, 300);
  },

  setMinAmount: (amount: string) => {
    set((s) => {
      s.minAmount = amount;
    });
    // Debounce amount filter with its own timer
    if (amountDebounceTimer) clearTimeout(amountDebounceTimer);
    amountDebounceTimer = setTimeout(() => {
      set((s) => { s.currentPage = 1; });
      get().fetchPage(1);
    }, 300);
  },

  setDateRange: (from: string, to: string) => {
    const fromDate = new Date(from);
    const toDate = new Date(to);

    // Validate: swap if from is after to
    if (fromDate > toDate) {
      set((s) => {
        s.dateRangeFrom = to;
        s.dateRangeTo = from;
        s.currentPage = 1;
      });
    } else {
      set((s) => {
        s.dateRangeFrom = from;
        s.dateRangeTo = to;
        s.currentPage = 1;
      });
    }
    get().fetchPage(1);
  },

  setWatchOnlyFilter: (filter: WatchOnlyFilter) => {
    set((s) => {
      s.watchOnlyFilter = filter;
      s.currentPage = 1;
    });
    get().fetchPage(1);
  },

  syncHideOrphanStakes: async () => {
    try {
      const { GetSettingBool } = await import('@wailsjs/go/main/App');
      const hide = await GetSettingBool('fHideOrphans');
      const prev = get().hideOrphanStakes;
      if (hide !== prev) {
        set((s) => {
          s.hideOrphanStakes = hide;
          s.currentPage = 1;
        });
        get().fetchPage(1);
      }
    } catch {
      // Silently fail — keep current value
    }
  },

  syncBlockExplorerUrls: async () => {
    try {
      const { GetSettingString } = await import('@wailsjs/go/main/App');
      const urlsString = await GetSettingString('strThirdPartyTxUrls');
      const urls = parseBlockExplorerUrls(urlsString);
      set((s) => {
        s.blockExplorerUrls = urls;
      });
    } catch {
      // Silently fail — block explorer is optional
    }
  },

  clearFilters: () => {
    const defaultRange = getDefaultDateRange();
    set((s) => {
      s.dateFilter = 'all';
      s.typeFilter = 'all';
      s.searchText = '';
      s.minAmount = '';
      s.dateRangeFrom = defaultRange.from;
      s.dateRangeTo = defaultRange.to;
      s.watchOnlyFilter = 'all';
      s.currentPage = 1;
      // Note: hideOrphanStakes is NOT cleared — it's managed via GUISettings (Settings dialog)
    });
    get().fetchPage(1);
  },

  // Sort actions
  setSortColumn: (column: SortColumn) => {
    set((s) => {
      if (s.sortColumn === column) {
        s.sortDirection = s.sortDirection === 'asc' ? 'desc' : 'asc';
      } else {
        s.sortColumn = column;
        s.sortDirection = column === 'date' ? 'desc' : 'asc';
      }
      s.currentPage = 1;
    });
    get().fetchPage(1);
  },

  toggleSortDirection: () => {
    set((s) => {
      s.sortDirection = s.sortDirection === 'asc' ? 'desc' : 'asc';
      s.currentPage = 1;
    });
    get().fetchPage(1);
  },

  // Selection actions (current page only)
  toggleSelection: (key: string) => {
    set((s) => {
      if (s.selectedTxids[key]) {
        delete s.selectedTxids[key];
      } else {
        s.selectedTxids[key] = true;
      }
    });
  },

  selectAll: () => {
    const { transactions } = get();
    set((s) => {
      transactions.forEach((tx) => {
        s.selectedTxids[`${tx.txid}:${tx.vout}`] = true;
      });
    });
  },

  unselectAll: () => {
    set((s) => {
      s.selectedTxids = {};
    });
  },

  isSelected: (key: string) => {
    return !!get().selectedTxids[key];
  },

  getSelectedAmount: () => {
    const { transactions, selectedTxids } = get();
    return transactions
      .filter((tx) => selectedTxids[`${tx.txid}:${tx.vout}`])
      .reduce((sum, tx) => sum + tx.amount, 0);
  },

  getSelectedCount: () => {
    return Object.keys(get().selectedTxids).length;
  },

  // Export - delegates to backend for all matching results
  exportCSV: async () => {
    try {
      const state = get();
      const filter = buildFilter(state);
      const saved = await ExportFilteredTransactionsCSV(filter as any);
      return saved;
    } catch (error) {
      console.error('Failed to export transactions CSV:', error);
      throw error;
    }
  },

  // Notification
  incrementNewTransactionCount: () => {
    set((s) => {
      s.newTransactionCount += 1;
    });
  },

  clearNewTransactionCount: () => {
    set((s) => {
      s.newTransactionCount = 0;
    });
  },
});
