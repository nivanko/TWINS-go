import React, { useState, useCallback, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, Check, Copy, RotateCw, X } from 'lucide-react';
import { PaymentStatsResponse, PaymentStatsEntry } from '@/shared/types/masternode.types';
import { useDisplayUnits } from '@/shared/hooks/useDisplayUnits';
import { RefreshCountdown } from '@/shared/components/RefreshCountdown';
import { CopyToClipboard, GetPaymentStats } from '@wailsjs/go/main/App';

const REFRESH_SECONDS = 60;

const PAGE_SIZES = [10, 25, 50, 100] as const;
type PageSize = typeof PAGE_SIZES[number];

// Tier colors matching MasternodeStatisticsPanel
const TIER_COLORS: Record<string, string> = {
  platinum: '#e5e4e2',
  gold: '#ffd700',
  silver: '#c0c0c0',
  bronze: '#cd7f32',
};

// Sortable columns (address and latestTxID are not sortable)
type SortColumn = 'tier' | 'paymentCount' | 'totalPaid' | 'lastPaidTime';
type SortDirection = 'asc' | 'desc';

interface PaymentStatsTabProps {
  isLoading: boolean;
  onStatsLoaded?: (stats: PaymentStatsResponse | null) => void;
}

// Format a number with thousands separators
function formatNumber(n: number): string {
  return n.toLocaleString();
}

// Format time ago from ISO string
function formatTimeAgo(isoStr: string): string {
  if (!isoStr) return 'N/A';
  const d = new Date(isoStr);
  if (isNaN(d.getTime()) || d.getUTCFullYear() <= 1970) return 'N/A';
  const now = Date.now();
  const diff = now - d.getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  return `${months}mo ago`;
}

function formatDateUTC(isoStr: string): string {
  if (!isoStr) return '';
  const d = new Date(isoStr);
  if (isNaN(d.getTime()) || d.getUTCFullYear() <= 1970) return '';
  return d.toISOString().replace('T', ' ').replace(/\.\d+Z$/, ' UTC');
}

export const PaymentStatsTab: React.FC<PaymentStatsTabProps> = React.memo(({ isLoading, onStatsLoaded }) => {
  const { t } = useTranslation('masternode');
  const { formatAmount } = useDisplayUnits();

  // Sort state
  const [sortColumn, setSortColumn] = useState<SortColumn>('totalPaid');
  const [sortDirection, setSortDirection] = useState<SortDirection>('desc');
  const sortColumnRef = useRef<SortColumn>(sortColumn);
  sortColumnRef.current = sortColumn;

  // Pagination state
  const [currentPage, setCurrentPage] = useState(1);
  const [pageSize, setPageSize] = useState<PageSize>(10);

  // Data state — component owns its own data fetching
  const [stats, setStats] = useState<PaymentStatsResponse | null>(null);
  const [isFetching, setIsFetching] = useState(false);
  // Error state for the most recent fetch. Null when the last fetch succeeded
  // or the user dismissed the banner. Stays set across polls until cleared.
  // We deliberately do NOT clear `stats` on error so stale data remains visible.
  const [fetchError, setFetchError] = useState<string | null>(null);

  // Auto-refresh countdown
  const [countdown, setCountdown] = useState(REFRESH_SECONDS);
  const countdownRef = useRef(REFRESH_SECONDS);

  // Copy feedback state
  const [copiedTxID, setCopiedTxID] = useState<string | null>(null);

  // Mounted ref to prevent state updates after unmount
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  // Fetch data from backend with current sort/pagination params
  const fetchData = useCallback(async (page: number, size: PageSize, column: SortColumn, direction: SortDirection) => {
    setIsFetching(true);
    try {
      const result = await GetPaymentStats({
        sortColumn: column,
        sortDirection: direction,
        page,
        pageSize: size,
      });
      if (mountedRef.current && result) {
        setStats(result as PaymentStatsResponse);
        setFetchError(null);
        onStatsLoaded?.(result as PaymentStatsResponse);
      }
    } catch (error) {
      console.error('Failed to fetch payment stats:', error);
      if (mountedRef.current) {
        // Keep existing `stats` untouched so stale data stays visible.
        setFetchError(error instanceof Error ? error.message : String(error));
      }
    } finally {
      if (mountedRef.current) {
        setIsFetching(false);
      }
    }
  }, [onStatsLoaded]);

  // Stable ref for fetchData params to use in timer
  const fetchParamsRef = useRef({ currentPage, pageSize, sortColumn, sortDirection });
  fetchParamsRef.current = { currentPage, pageSize, sortColumn, sortDirection };

  // Initial fetch + refetch on sort/page changes — also resets countdown
  useEffect(() => {
    fetchData(currentPage, pageSize, sortColumn, sortDirection);
    countdownRef.current = REFRESH_SECONDS;
    setCountdown(REFRESH_SECONDS);
  }, [currentPage, pageSize, sortColumn, sortDirection, fetchData]);

  // Auto-refresh countdown timer
  useEffect(() => {
    const interval = setInterval(() => {
      countdownRef.current -= 1;
      setCountdown(countdownRef.current);
      if (countdownRef.current <= 0) {
        const p = fetchParamsRef.current;
        fetchData(p.currentPage, p.pageSize, p.sortColumn, p.sortDirection);
        countdownRef.current = REFRESH_SECONDS;
        setCountdown(REFRESH_SECONDS);
      }
    }, 1000);
    return () => clearInterval(interval);
  }, [fetchData]);

  const handleSort = useCallback((column: SortColumn) => {
    if (sortColumnRef.current === column) {
      setSortDirection(d => d === 'asc' ? 'desc' : 'asc');
    } else {
      setSortColumn(column);
      setSortDirection(column === 'tier' ? 'asc' : 'desc');
    }
    setCurrentPage(1);
  }, []);

  const handlePageSizeChange = useCallback((newSize: PageSize) => {
    setPageSize(newSize);
    setCurrentPage(1);
  }, []);

  const handleCopyTxID = useCallback(async (txid: string) => {
    try {
      await CopyToClipboard(txid);
      setCopiedTxID(txid);
      setTimeout(() => setCopiedTxID(null), 2000);
    } catch {
      // silently ignore
    }
  }, []);

  const renderSortIndicator = (column: SortColumn) => {
    if (sortColumn !== column) return null;
    return <span style={{ marginLeft: '4px' }}>{sortDirection === 'asc' ? '▲' : '▼'}</span>;
  };

  const handleRefresh = useCallback(() => {
    const p = fetchParamsRef.current;
    fetchData(p.currentPage, p.pageSize, p.sortColumn, p.sortDirection);
    countdownRef.current = REFRESH_SECONDS;
    setCountdown(REFRESH_SECONDS);
  }, [fetchData]);

  // Retry the last fetch using current params. Also resets the auto-refresh
  // countdown so the user is not immediately polled again after retrying.
  const handleRetry = useCallback(() => {
    handleRefresh();
  }, [handleRefresh]);

  const handleDismissError = useCallback(() => {
    setFetchError(null);
  }, []);

  const showLoading = isLoading || isFetching;

  // Loading skeleton — only shown while actively fetching with no prior data
  // and no error. If an error occurred on the first fetch, skip the skeleton
  // and render the error banner instead so the user knows what happened.
  if (showLoading && !stats && !fetchError) {
    return (
      <div style={{ padding: '16px', color: '#999', fontSize: '12px' }}>
        {t('paymentStats.loading')}
      </div>
    );
  }

  // First-load error: show the banner standalone so the user can see what
  // failed and retry without being told "No data available" (which would be
  // misleading when the real problem is a failed RPC call).
  if (!stats && fetchError) {
    return (
      <div style={{ padding: '16px' }}>
        <PaymentStatsErrorBanner
          title={t('paymentStats.fetchError')}
          message={fetchError}
          retryLabel={t('paymentStats.retry')}
          dismissLabel={t('paymentStats.dismiss')}
          onRetry={handleRetry}
          onDismiss={handleDismissError}
          isRetrying={isFetching}
        />
      </div>
    );
  }

  // No data (genuinely empty database, no error)
  if (!stats || !stats.entries?.length) {
    return (
      <div style={{ padding: '16px', color: '#999', fontSize: '12px' }}>
        {t('paymentStats.noData')}
      </div>
    );
  }

  const totalPages = stats.totalPages || 1;
  const safePage = stats.currentPage || 1;
  const totalEntries = stats.totalEntries || 0;
  const rangeStart = totalEntries > 0 ? (safePage - 1) * pageSize + 1 : 0;
  const rangeEnd = Math.min(safePage * pageSize, totalEntries);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0 }}>
      {/* Summary Cards */}
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(4, 1fr)',
        gap: '8px',
        marginBottom: '12px',
      }}>
        <SummaryCard
          label={t('paymentStats.summary.totalPaid')}
          value={formatAmount(stats.totalPaid)}
        />
        <SummaryCard
          label={t('paymentStats.summary.totalPayments')}
          value={formatNumber(stats.totalPayments)}
        />
        <SummaryCard
          label={t('paymentStats.summary.uniquePaymentAddresses')}
          value={formatNumber(stats.uniquePaymentAddresses)}
        />
        <SummaryCard
          label={t('paymentStats.summary.scannedBlocks')}
          value={`${formatNumber(stats.lowestBlock)} / ${formatNumber(stats.highestBlock)}`}
        />
      </div>

      {/* Fetch error banner — shown above the refresh countdown when a fetch
          fails while stale data is still on screen. Does NOT clear stats. */}
      {fetchError && (
        <PaymentStatsErrorBanner
          title={t('paymentStats.fetchError')}
          message={fetchError}
          retryLabel={t('paymentStats.retry')}
          dismissLabel={t('paymentStats.dismiss')}
          onRetry={handleRetry}
          onDismiss={handleDismissError}
          isRetrying={isFetching}
        />
      )}

      {/* Refresh countdown */}
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: '4px' }}>
        <RefreshCountdown
          countdown={countdown}
          total={REFRESH_SECONDS}
          mode="interactive"
          onRefresh={handleRefresh}
          isLoading={isFetching}
        />
      </div>

      {/* Table */}
      <div style={{
        flex: 1,
        overflow: 'auto',
        minHeight: 0,
        border: '1px solid #4a4a4a',
        borderRadius: '2px',
        backgroundColor: '#2b2b2b',
        opacity: isFetching ? 0.7 : 1,
        transition: 'opacity 0.15s',
      }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '11px', tableLayout: 'fixed' }}>
          <thead style={{ position: 'sticky', top: 0, zIndex: 1 }}>
            <tr>
              {([
                { key: null, label: t('paymentStats.table.address'), width: '220px' },
                { key: 'tier' as SortColumn, label: t('paymentStats.table.tier'), width: '80px' },
                { key: 'paymentCount' as SortColumn, label: t('paymentStats.table.paymentCount'), width: '90px' },
                { key: 'totalPaid' as SortColumn, label: t('paymentStats.table.totalPaid'), width: '140px' },
                { key: 'lastPaidTime' as SortColumn, label: t('paymentStats.table.lastPaidTime'), width: '120px' },
                { key: null, label: t('paymentStats.table.latestTxID') },
              ] as { key: SortColumn | null; label: string; width?: string }[]).map((col, idx) => (
                <th
                  key={col.label + idx}
                  onClick={col.key ? () => handleSort(col.key!) : undefined}
                  style={{
                    padding: '6px 8px',
                    textAlign: 'left',
                    cursor: col.key ? 'pointer' : 'default',
                    userSelect: 'none',
                    backgroundColor: '#3a3a3a',
                    color: '#ccc',
                    fontWeight: 'normal',
                    borderBottom: '1px solid #555',
                    width: col.width,
                    whiteSpace: 'nowrap',
                  }}
                >
                  {col.label}{col.key && renderSortIndicator(col.key)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {stats.entries.map((entry, idx) => (
              <PaymentRow
                key={entry.address}
                entry={entry}
                isEven={idx % 2 === 0}
                copiedTxID={copiedTxID}
                onCopyTxID={handleCopyTxID}
              />
            ))}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      <div style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        padding: '6px 0',
        fontSize: '11px',
        color: '#999',
        flexShrink: 0,
      }}>
        <span>
          {t('paymentStats.pagination.showing', {
            from: rangeStart,
            to: rangeEnd,
            total: totalEntries,
          })}
        </span>
        <div style={{ display: 'flex', gap: '4px', alignItems: 'center' }}>
          <select
            value={pageSize}
            onChange={(e) => handlePageSizeChange(Number(e.target.value) as PageSize)}
            style={{
              marginRight: '8px',
              padding: '2px 4px',
              fontSize: '11px',
              backgroundColor: '#3a3a3a',
              color: '#ccc',
              border: '1px solid #555',
              borderRadius: '2px',
              cursor: 'pointer',
            }}
          >
            {PAGE_SIZES.map((size) => (
              <option key={size} value={size}>
                {t('paymentStats.pagination.perPage', { count: size })}
              </option>
            ))}
          </select>
          <PaginationButton onClick={() => setCurrentPage(1)} disabled={safePage <= 1}>«</PaginationButton>
          <PaginationButton onClick={() => setCurrentPage(p => Math.max(1, p - 1))} disabled={safePage <= 1}>‹</PaginationButton>
          <span style={{ padding: '0 8px' }}>
            {t('paymentStats.pagination.page', { page: safePage, total: totalPages })}
          </span>
          <PaginationButton onClick={() => setCurrentPage(p => Math.min(totalPages, p + 1))} disabled={safePage >= totalPages}>›</PaginationButton>
          <PaginationButton onClick={() => setCurrentPage(totalPages)} disabled={safePage >= totalPages}>»</PaginationButton>
        </div>
      </div>
    </div>
  );
});

PaymentStatsTab.displayName = 'PaymentStatsTab';

// --- Sub-components ---

const SummaryCard: React.FC<{ label: string; value: string }> = ({ label, value }) => (
  <div style={{
    backgroundColor: '#3a3a3a',
    borderRadius: '4px',
    padding: '10px 12px',
    border: '1px solid #4a4a4a',
  }}>
    <div style={{ fontSize: '10px', color: '#999', marginBottom: '4px' }}>{label}</div>
    <div style={{ fontSize: '14px', color: '#ddd', fontWeight: 'bold' }}>{value}</div>
  </div>
);

const PaginationButton: React.FC<{ onClick: () => void; disabled: boolean; children: React.ReactNode }> = ({ onClick, disabled, children }) => (
  <button
    onClick={onClick}
    disabled={disabled}
    style={{
      padding: '2px 8px',
      fontSize: '12px',
      backgroundColor: disabled ? '#2a2a2a' : '#3a3a3a',
      color: disabled ? '#666' : '#ccc',
      border: '1px solid #555',
      borderRadius: '2px',
      cursor: disabled ? 'default' : 'pointer',
    }}
  >
    {children}
  </button>
);

interface PaymentStatsErrorBannerProps {
  title: string;
  message: string;
  retryLabel: string;
  dismissLabel: string;
  onRetry: () => void;
  onDismiss: () => void;
  isRetrying: boolean;
}

// Inline amber warning banner shown when a payment stats fetch fails. Uses the
// established WindowTab amber palette (#4a2a00 bg / #664400 border / #ffa500
// icon / #ffcc00 text) so it is clearly distinct from the grey empty-data
// message rendered when the database is genuinely empty.
const PaymentStatsErrorBanner: React.FC<PaymentStatsErrorBannerProps> = ({
  title,
  message,
  retryLabel,
  dismissLabel,
  onRetry,
  onDismiss,
  isRetrying,
}) => (
  <div
    role="alert"
    style={{
      padding: '10px 14px',
      marginBottom: '8px',
      backgroundColor: '#4a2a00',
      border: '1px solid #664400',
      borderRadius: '4px',
      display: 'flex',
      alignItems: 'flex-start',
      gap: '10px',
    }}
  >
    <AlertTriangle size={16} style={{ color: '#ffa500', flexShrink: 0, marginTop: '1px' }} />
    <div style={{ flex: 1, minWidth: 0 }}>
      <div style={{ color: '#ffcc00', fontSize: '12px', fontWeight: 'bold', marginBottom: '2px' }}>
        {title}
      </div>
      <div style={{ color: '#ddd', fontSize: '11px', wordBreak: 'break-word' }}>
        {message}
      </div>
    </div>
    <button
      onClick={onRetry}
      disabled={isRetrying}
      title={retryLabel}
      style={{
        padding: '4px 10px',
        fontSize: '11px',
        backgroundColor: isRetrying ? '#2a2a2a' : '#3a3a3a',
        color: isRetrying ? '#666' : '#ffcc00',
        border: '1px solid #664400',
        borderRadius: '2px',
        cursor: isRetrying ? 'default' : 'pointer',
        display: 'flex',
        alignItems: 'center',
        gap: '4px',
        flexShrink: 0,
      }}
    >
      <RotateCw size={11} />
      {retryLabel}
    </button>
    <button
      onClick={onDismiss}
      aria-label={dismissLabel}
      title={dismissLabel}
      style={{
        padding: '2px',
        background: 'none',
        border: 'none',
        color: '#ffcc00',
        cursor: 'pointer',
        display: 'flex',
        alignItems: 'center',
        flexShrink: 0,
      }}
    >
      <X size={14} />
    </button>
  </div>
);

interface PaymentRowProps {
  entry: PaymentStatsEntry;
  isEven: boolean;
  copiedTxID: string | null;
  onCopyTxID: (txid: string) => void;
}

const PaymentRow: React.FC<PaymentRowProps> = React.memo(({ entry, isEven, copiedTxID, onCopyTxID }) => {
  const { formatAmount } = useDisplayUnits();
  const tierColor = TIER_COLORS[entry.tier] || '#999';
  const tierLabel = entry.tier ? entry.tier.charAt(0).toUpperCase() + entry.tier.slice(1) : '';
  const isCopied = copiedTxID === entry.latestTxID;

  return (
    <tr style={{
      backgroundColor: isEven ? '#2b2b2b' : '#2f2f2f',
      borderBottom: '1px solid #4a4a4a',
    }}>
      <td style={{ padding: '5px 8px', color: '#ddd', fontFamily: 'monospace', fontSize: '10px' }}>
        {entry.address}
      </td>
      <td style={{ padding: '5px 8px', color: tierColor, fontWeight: entry.tier ? 'bold' : 'normal' }}>
        {tierLabel || '—'}
      </td>
      <td style={{ padding: '5px 8px', color: '#ddd', textAlign: 'right' }}>
        {entry.paymentCount.toLocaleString()}
      </td>
      <td style={{ padding: '5px 8px', color: '#ddd', textAlign: 'right' }}>
        {formatAmount(entry.totalPaid, false)}
      </td>
      <td style={{ padding: '5px 8px', color: '#ccc' }} title={formatDateUTC(entry.lastPaidTime)}>
        {formatTimeAgo(entry.lastPaidTime)}
      </td>
      <td style={{ padding: '5px 8px' }}>
        {entry.latestTxID ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
            <span style={{
              color: '#999',
              fontFamily: 'monospace',
              fontSize: '10px',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
              maxWidth: '160px',
            }} title={entry.latestTxID}>
              {entry.latestTxID}
            </span>
            <button
              onClick={() => onCopyTxID(entry.latestTxID)}
              style={{
                background: 'none',
                border: 'none',
                cursor: 'pointer',
                padding: '2px',
                color: isCopied ? '#4caf50' : '#888',
                flexShrink: 0,
              }}
              title={isCopied ? 'Copied!' : 'Copy TX ID'}
            >
              {isCopied ? <Check size={12} /> : <Copy size={12} />}
            </button>
          </div>
        ) : (
          <span style={{ color: '#666' }}>—</span>
        )}
      </td>
    </tr>
  );
});

PaymentRow.displayName = 'PaymentRow';
