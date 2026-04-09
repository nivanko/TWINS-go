import React, { useEffect, useMemo, useState, useCallback, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useTransactions, useNotifications } from '@/store/useStore';
import { core } from '@/shared/types/wallet.types';
import {
  getTransactionTypeIcon,
  formatTransactionAmount,
  formatTransactionDate,
  formatTransactionDateUTC,
  getAmountColorClass,
  getTransactionTypeLabel,
} from '@/shared/utils/transactionIcons';
import { ConfirmationRing } from '@/shared/components/ConfirmationRing';
import { useDisplayUnits } from '@/shared/hooks/useDisplayUnits';
import { ChevronUp, ChevronDown, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Eye, EyeOff, Copy, Edit, FileText, ExternalLink } from 'lucide-react';
import type { DateFilter, TypeFilter, SortColumn } from '@/store/slices/transactionsSlice';
import { PAGE_SIZES } from '@/store/slices/transactionsSlice';
import type { PageSize } from '@/store/slices/transactionsSlice';
import { TransactionDetailsDialog } from '../components/TransactionDetailsDialog';
import { EditLabelDialog } from '../components/EditLabelDialog';
import { BrowserOpenURL, EventsOn, EventsOff } from '@wailsjs/runtime/runtime';

// Qt constants
const DECORATION_SIZE = 32; // Smaller icons for table view

/**
 * Context menu state interface
 */
interface ContextMenuState {
  visible: boolean;
  x: number;
  y: number;
  transaction: core.Transaction | null;
}


/**
 * Transaction row component for the table
 */
interface TransactionRowProps {
  transaction: core.Transaction;
  isSelected: boolean;
  onToggleSelect: () => void;
  onClick: () => void;
  onDoubleClick: () => void;
  onContextMenu: (e: React.MouseEvent, transaction: core.Transaction) => void;
}

const TransactionRow: React.FC<TransactionRowProps> = ({
  transaction,
  isSelected,
  onToggleSelect,
  onClick,
  onDoubleClick,
  onContextMenu,
}) => {
  const typeIcon = getTransactionTypeIcon(transaction.type);
  const { displayUnit, displayDigits } = useDisplayUnits();
  const formattedAmount = formatTransactionAmount(transaction.amount, transaction.confirmations || 0, displayUnit, displayDigits);
  const formattedDate = formatTransactionDate(transaction.time);
  const formattedDateUTC = formatTransactionDateUTC(transaction.time);
  const amountColorClass = getAmountColorClass(transaction.amount);
  const typeLabel = getTransactionTypeLabel(transaction.type);
  const displayAddress = transaction.label || transaction.address || 'Unknown';

  return (
    <tr
      className={`border-b border-gray-700 hover:bg-gray-800/50 cursor-pointer transition-colors ${
        isSelected ? 'bg-blue-900/30' : ''
      }`}
      onClick={onClick}
      onDoubleClick={onDoubleClick}
      onContextMenu={(e) => onContextMenu(e, transaction)}
    >
      {/* Selection Checkbox */}
      <td className="py-2 px-2 w-8">
        <input
          type="checkbox"
          checked={isSelected}
          onChange={onToggleSelect}
          onClick={(e) => e.stopPropagation()}
          className="w-4 h-4 cursor-pointer"
          style={{ accentColor: '#5a9cff' }}
        />
      </td>

      {/* Status Icon */}
      <td className="py-2 px-2 w-12">
        <ConfirmationRing
          typeIcon={typeIcon}
          confirmations={transaction.confirmations || 0}
          isConflicted={transaction.is_conflicted || false}
          isCoinstake={transaction.is_coinstake || false}
          maturesIn={transaction.matures_in || 0}
          size={DECORATION_SIZE}
        />
      </td>

      {/* Date */}
      <td
        className="py-2 px-3 text-sm text-gray-300 whitespace-nowrap"
        style={{ cursor: 'help' }}
        title={`UTC: ${formattedDateUTC}`}
      >
        {formattedDate}
      </td>

      {/* Type */}
      <td className="py-2 px-3 text-sm text-gray-300">
        {typeLabel}
      </td>

      {/* Address */}
      <td className="py-2 px-3 text-sm text-gray-400 truncate max-w-xs" title={displayAddress}>
        {displayAddress}
      </td>

      {/* Amount */}
      <td className={`py-2 px-3 text-sm font-mono text-right whitespace-nowrap ${amountColorClass}`}>
        {formattedAmount}
      </td>
    </tr>
  );
};

/**
 * Skeleton row component for loading state
 * Mimics TransactionRow structure with animated placeholders
 */
const SkeletonRow: React.FC<{ index: number }> = ({ index }) => {
  // Vary widths slightly for more natural look
  const addressWidth = 120 + (index % 3) * 40;
  const amountWidth = 60 + (index % 2) * 20;

  return (
    <tr className="border-b border-gray-700">
      {/* Checkbox placeholder */}
      <td className="py-2 px-2 w-8">
        <div
          className="w-4 h-4 bg-gray-700 rounded animate-pulse"
          style={{ animationDelay: `${index * 50}ms` }}
        />
      </td>

      {/* Icon placeholder */}
      <td className="py-2 px-2 w-12">
        <div
          className="w-8 h-8 bg-gray-700 rounded animate-pulse"
          style={{ animationDelay: `${index * 50 + 25}ms` }}
        />
      </td>

      {/* Date placeholder */}
      <td className="py-2 px-3">
        <div
          className="h-4 bg-gray-700 rounded animate-pulse"
          style={{ width: '100px', animationDelay: `${index * 50 + 50}ms` }}
        />
      </td>

      {/* Type placeholder */}
      <td className="py-2 px-3">
        <div
          className="h-4 bg-gray-700 rounded animate-pulse"
          style={{ width: '80px', animationDelay: `${index * 50 + 75}ms` }}
        />
      </td>

      {/* Address placeholder */}
      <td className="py-2 px-3">
        <div
          className="h-4 bg-gray-700 rounded animate-pulse"
          style={{ width: `${addressWidth}px`, animationDelay: `${index * 50 + 100}ms` }}
        />
      </td>

      {/* Amount placeholder */}
      <td className="py-2 px-3">
        <div
          className="h-4 bg-gray-700 rounded animate-pulse ml-auto"
          style={{ width: `${amountWidth}px`, animationDelay: `${index * 50 + 125}ms` }}
        />
      </td>
    </tr>
  );
};

/**
 * Skeleton loader component showing multiple skeleton rows
 */
const TransactionsSkeleton: React.FC = () => {
  // Show 10 skeleton rows for visual consistency
  const skeletonRows = Array.from({ length: 10 }, (_, i) => i);

  return (
    <table className="w-full border-collapse">
      <thead className="sticky top-0 bg-gray-800 z-10">
        <tr className="border-b border-gray-600">
          <th className="py-2 px-2 w-8">
            <div className="w-4 h-4 bg-gray-700 rounded animate-pulse" />
          </th>
          <th className="py-2 px-2 w-12" />
          <th className="py-2 px-3 text-left text-sm font-medium text-gray-500">Date</th>
          <th className="py-2 px-3 text-left text-sm font-medium text-gray-500">Type</th>
          <th className="py-2 px-3 text-left text-sm font-medium text-gray-500">Address</th>
          <th className="py-2 px-3 text-right text-sm font-medium text-gray-500">Amount</th>
        </tr>
      </thead>
      <tbody>
        {skeletonRows.map((i) => (
          <SkeletonRow key={i} index={i} />
        ))}
      </tbody>
    </table>
  );
};

/**
 * Sortable column header component
 */
interface SortableHeaderProps {
  label: string;
  column: SortColumn;
  currentColumn: SortColumn;
  direction: 'asc' | 'desc';
  onSort: (column: SortColumn) => void;
  className?: string;
}

const SortableHeader: React.FC<SortableHeaderProps> = ({
  label,
  column,
  currentColumn,
  direction,
  onSort,
  className = '',
}) => {
  const isActive = currentColumn === column;

  return (
    <th
      className={`py-2 px-3 text-left text-sm font-medium text-gray-400 cursor-pointer hover:text-white select-none ${className}`}
      onClick={() => onSort(column)}
    >
      <div className="flex items-center gap-1">
        <span>{label}</span>
        {isActive && (
          direction === 'asc' ? (
            <ChevronUp size={14} className="text-blue-400" />
          ) : (
            <ChevronDown size={14} className="text-blue-400" />
          )
        )}
      </div>
    </th>
  );
};

/**
 * Main Transactions Page Component
 * Matches Qt wallet's transactionview appearance
 */
export const Transactions: React.FC = () => {
  const { t } = useTranslation('wallet');
  const { formatAmount } = useDisplayUnits();
  const {
    transactions,
    total,
    totalAll,
    totalPages,
    isLoading,
    error,
    currentPage,
    pageSize,
    dateFilter,
    typeFilter,
    searchText,
    minAmount,
    dateRangeFrom,
    dateRangeTo,
    watchOnlyFilter,
    hasWatchOnlyAddresses,
    syncHideOrphanStakes,
    syncBlockExplorerUrls,
    blockExplorerUrls,
    sortColumn,
    sortDirection,
    selectedTxids,
    newTransactionCount,
    fetchPage,
    setPageSize,
    goToFirstPage,
    goToLastPage,
    goToPrevPage,
    goToNextPage,
    setDateFilter,
    setTypeFilter,
    setSearchText,
    setMinAmount,
    setDateRange,
    setWatchOnlyFilter,
    setSortColumn,
    toggleSelection,
    selectAll,
    unselectAll,
    isSelected,
    getSelectedAmount,
    getSelectedCount,
    exportCSV,
    incrementNewTransactionCount,
    clearNewTransactionCount,
  } = useTransactions();

  const { addNotification } = useNotifications();

  // Exporting state
  const [isExporting, setIsExporting] = useState(false);

  // Sync settings from GUISettings and fetch first page on mount
  useEffect(() => {
    syncBlockExplorerUrls();
    syncHideOrphanStakes().then(() => fetchPage());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Selection-dependent computed values - include selectedTxids for reactivity
  const selectedAmount = useMemo(() => getSelectedAmount(), [getSelectedAmount, selectedTxids]);
  const selectedCount = useMemo(() => getSelectedCount(), [getSelectedCount, selectedTxids]);

  // Calculate display range for footer
  const rangeStart = total > 0 ? (currentPage - 1) * pageSize + 1 : 0;
  const rangeEnd = Math.min(currentPage * pageSize, total);

  // Date filter options (Qt parity)
  const dateOptions: { value: DateFilter; label: string }[] = [
    { value: 'all', label: 'All' },
    { value: 'today', label: 'Today' },
    { value: 'week', label: 'This week' },
    { value: 'month', label: 'This month' },
    { value: 'lastMonth', label: 'Last month' },
    { value: 'year', label: 'This year' },
    { value: 'range', label: 'Range...' },
  ];

  // Type filter options (14 Qt options)
  const typeOptions: { value: TypeFilter; label: string }[] = [
    { value: 'all', label: 'All' },
    { value: 'mostCommon', label: 'Most Common' },
    { value: 'received', label: 'Received with' },
    { value: 'sent', label: 'Sent to' },
    { value: 'toYourself', label: 'To yourself' },
    { value: 'mined', label: 'Mined' },
    { value: 'minted', label: 'Minted' },
    { value: 'masternode', label: 'Masternode Reward' },
    { value: 'consolidation', label: 'UTXO Consolidation' },
    { value: 'other', label: 'Other' },
  ];

  // Handle select all checkbox
  const handleSelectAllChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.checked) {
      selectAll();
    } else {
      unselectAll();
    }
  };

  // Check if all visible transactions are selected - include selectedTxids for reactivity
  const allSelected = useMemo(
    () => transactions.length > 0 &&
      transactions.every((tx) => selectedTxids[`${tx.txid}:${tx.vout}`]),
    [transactions, selectedTxids]
  );

  // Context menu state
  const [contextMenu, setContextMenu] = useState<ContextMenuState>({
    visible: false,
    x: 0,
    y: 0,
    transaction: null,
  });
  const contextMenuRef = useRef<HTMLDivElement>(null);

  // Transaction details dialog state
  const [detailsDialogOpen, setDetailsDialogOpen] = useState(false);
  const [selectedTransaction, setSelectedTransaction] = useState<core.Transaction | null>(null);

  // Edit label dialog state
  const [editLabelDialogOpen, setEditLabelDialogOpen] = useState(false);
  const [editLabelAddress, setEditLabelAddress] = useState('');
  const [editLabelCurrentLabel, setEditLabelCurrentLabel] = useState('');


  // Subscribe to transaction events - show notification banner instead of auto-refreshing
  useEffect(() => {
    const unsubReceived = EventsOn('transaction:received', () => {
      incrementNewTransactionCount();
    });

    const unsubConfirmed = EventsOn('transaction:confirmed', () => {
      incrementNewTransactionCount();
    });

    return () => {
      EventsOff('transaction:received');
      EventsOff('transaction:confirmed');
      if (typeof unsubReceived === 'function') unsubReceived();
      if (typeof unsubConfirmed === 'function') unsubConfirmed();
    };
  }, [incrementNewTransactionCount]);

  // Handle double-click to open transaction details
  const handleTransactionDoubleClick = useCallback((transaction: core.Transaction) => {
    setSelectedTransaction(transaction);
    setDetailsDialogOpen(true);
  }, []);

  // Close transaction details dialog
  const handleCloseDetailsDialog = useCallback(() => {
    setDetailsDialogOpen(false);
    setSelectedTransaction(null);
  }, []);

  // Handle right-click on transaction row
  const handleContextMenu = useCallback((e: React.MouseEvent, transaction: core.Transaction) => {
    e.preventDefault();
    e.stopPropagation();

    // Calculate position with viewport boundary checking
    // Menu dimensions estimated from styling (minWidth: 200px, ~12 items at 32px each)
    const menuWidth = 220;
    const menuHeight = 420;
    const padding = 10;

    let x = e.clientX;
    let y = e.clientY;

    // Prevent menu from rendering off-screen (right/bottom edges)
    if (x + menuWidth + padding > window.innerWidth) {
      x = window.innerWidth - menuWidth - padding;
    }
    if (y + menuHeight + padding > window.innerHeight) {
      y = window.innerHeight - menuHeight - padding;
    }
    // Ensure minimum padding from left/top edges
    x = Math.max(padding, x);
    y = Math.max(padding, y);

    setContextMenu({
      visible: true,
      x,
      y,
      transaction,
    });
  }, []);

  // Close context menu
  const closeContextMenu = useCallback(() => {
    setContextMenu(prev => ({ ...prev, visible: false }));
  }, []);

  // Close context menu on click outside
  useEffect(() => {
    if (contextMenu.visible) {
      const handleClickOutside = (e: MouseEvent) => {
        if (contextMenuRef.current && !contextMenuRef.current.contains(e.target as Node)) {
          closeContextMenu();
        }
      };
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [contextMenu.visible, closeContextMenu]);

  // Close context menu on Escape key
  useEffect(() => {
    if (contextMenu.visible) {
      const handleKeyDown = (e: KeyboardEvent) => {
        if (e.key === 'Escape') {
          closeContextMenu();
        }
      };
      document.addEventListener('keydown', handleKeyDown);
      return () => document.removeEventListener('keydown', handleKeyDown);
    }
  }, [contextMenu.visible, closeContextMenu]);

  // Copy to clipboard helper
  const copyToClipboard = useCallback(async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // Clipboard API may fail in some contexts - silently fail
    }
    closeContextMenu();
  }, [closeContextMenu]);

  // Format amount for clipboard (no thousand separators, 8 decimals, with sign)
  const formatAmountForClipboard = useCallback((amount: number): string => {
    const sign = amount >= 0 ? '+' : '';
    return `${sign}${amount.toFixed(8)} TWINS`;
  }, []);

  // Context menu action handlers
  const handleCopyAddress = useCallback(() => {
    if (contextMenu.transaction?.address) {
      copyToClipboard(contextMenu.transaction.address);
    }
  }, [contextMenu.transaction, copyToClipboard]);

  const handleCopyLabel = useCallback(() => {
    if (contextMenu.transaction?.label) {
      copyToClipboard(contextMenu.transaction.label);
    }
  }, [contextMenu.transaction, copyToClipboard]);

  const handleCopyAmount = useCallback(() => {
    if (contextMenu.transaction) {
      copyToClipboard(formatAmountForClipboard(contextMenu.transaction.amount));
    }
  }, [contextMenu.transaction, copyToClipboard, formatAmountForClipboard]);

  const handleCopyTxID = useCallback(() => {
    if (contextMenu.transaction?.txid) {
      copyToClipboard(contextMenu.transaction.txid);
    }
  }, [contextMenu.transaction, copyToClipboard]);

  const handleEditLabel = useCallback(() => {
    if (contextMenu.transaction?.address) {
      setEditLabelAddress(contextMenu.transaction.address);
      setEditLabelCurrentLabel(contextMenu.transaction.label || '');
      setEditLabelDialogOpen(true);
    }
    closeContextMenu();
  }, [contextMenu.transaction, closeContextMenu]);

  // Handle label updated - refresh transactions to show new label
  const handleLabelUpdated = useCallback((_address: string, newLabel: string) => {
    // Refresh current page to show updated label
    fetchPage();
    addNotification({
      type: 'success',
      title: 'Label updated',
      message: newLabel ? `Label set to "${newLabel}"` : 'Label cleared',
    });
  }, [fetchPage, addNotification]);

  // Close edit label dialog
  const handleCloseEditLabelDialog = useCallback(() => {
    setEditLabelDialogOpen(false);
  }, []);

  // Handle opening block explorer URL
  const handleOpenBlockExplorer = useCallback((urlTemplate: string) => {
    if (contextMenu.transaction?.txid) {
      // Validate txid format (64 hex characters for SHA256 hash)
      const txidRegex = /^[a-fA-F0-9]{64}$/;
      if (!txidRegex.test(contextMenu.transaction.txid)) {
        // Invalid txid - prevent opening potentially malicious URL
        closeContextMenu();
        return;
      }
      // Replace %s with txid and open in browser
      const url = urlTemplate.replace('%s', contextMenu.transaction.txid);
      BrowserOpenURL(url);
    }
    closeContextMenu();
  }, [contextMenu.transaction, closeContextMenu]);

  const handleShowDetails = useCallback(() => {
    if (contextMenu.transaction) {
      setSelectedTransaction(contextMenu.transaction);
      setDetailsDialogOpen(true);
    }
    closeContextMenu();
  }, [contextMenu.transaction, closeContextMenu]);

  // Handle new transaction banner click - refresh and clear count
  const handleNewTransactionBannerClick = useCallback(() => {
    clearNewTransactionCount();
    fetchPage(1);
  }, [clearNewTransactionCount, fetchPage]);

  // Handle export button click - delegates to backend for all filtered results
  const handleExport = useCallback(async () => {
    if (total === 0) {
      addNotification({
        type: 'warning',
        title: 'No transactions to export',
        message: 'There are no transactions matching the current filters.',
      });
      return;
    }

    setIsExporting(true);
    try {
      const saved = await exportCSV();
      if (saved) {
        addNotification({
          type: 'success',
          title: 'Export successful',
          message: `Exported ${total} transaction${total !== 1 ? 's' : ''} to CSV file`,
        });
      }
      // If saved is false, user cancelled the dialog - no notification needed
    } catch (error) {
      addNotification({
        type: 'error',
        title: 'Export failed',
        message: error instanceof Error ? error.message : 'Failed to export transactions',
      });
    } finally {
      setIsExporting(false);
    }
  }, [total, exportCSV, addNotification]);

  return (
    <div className="qt-frame" style={{ height: '100%', display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
      <div className="qt-vbox" style={{ padding: '8px', flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
        {/* Page Header */}
        <div className="qt-header-label" style={{ marginBottom: '8px', fontSize: '18px' }}>
          {t('transactions.title')}
        </div>

        {/* Filter Bar - Single row matching Qt layout */}
        <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center', marginBottom: '4px' }}>
          {/* Watch-only Filter Icons (only shown if there are watch-only addresses) */}
          {hasWatchOnlyAddresses && (
            <div className="qt-hbox" style={{ gap: '2px', alignItems: 'center' }}>
              <button
                type="button"
                onClick={() => setWatchOnlyFilter('all')}
                title="All transactions"
                style={{
                  padding: '4px',
                  backgroundColor: watchOnlyFilter === 'all' ? '#4a4a4a' : 'transparent',
                  border: '1px solid #3a3a3a',
                  borderRadius: '2px',
                  cursor: 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <Eye size={16} className="text-gray-400" />
              </button>
              <button
                type="button"
                onClick={() => setWatchOnlyFilter('yes')}
                title="Watch-only transactions"
                style={{
                  padding: '4px',
                  backgroundColor: watchOnlyFilter === 'yes' ? '#4a4a4a' : 'transparent',
                  border: '1px solid #3a3a3a',
                  borderRadius: '2px',
                  cursor: 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <Eye size={16} className="text-green-400" />
              </button>
              <button
                type="button"
                onClick={() => setWatchOnlyFilter('no')}
                title="Non-watch-only transactions"
                style={{
                  padding: '4px',
                  backgroundColor: watchOnlyFilter === 'no' ? '#4a4a4a' : 'transparent',
                  border: '1px solid #3a3a3a',
                  borderRadius: '2px',
                  cursor: 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                <EyeOff size={16} className="text-red-400" />
              </button>
            </div>
          )}

          {/* Date Filter */}
          <select
            value={dateFilter}
            onChange={(e) => setDateFilter(e.target.value as DateFilter)}
            className="qt-input"
            style={{
              padding: '4px 8px',
              fontSize: '12px',
              backgroundColor: '#2b2b2b',
              border: '1px solid #1a1a1a',
              color: '#ddd',
              width: '120px',
            }}
          >
            {dateOptions.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>

          {/* Type Filter */}
          <select
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value as TypeFilter)}
            className="qt-input"
            style={{
              padding: '4px 8px',
              fontSize: '12px',
              backgroundColor: '#2b2b2b',
              border: '1px solid #1a1a1a',
              color: '#ddd',
              width: '210px',
            }}
          >
            {typeOptions.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>

          {/* Address/Label Search Field */}
          <input
            type="text"
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            placeholder={t('transactions.filters.search')}
            className="qt-input"
            style={{
              flex: 1,
              padding: '4px 8px',
              fontSize: '12px',
              backgroundColor: '#2b2b2b',
              border: '1px solid #1a1a1a',
              color: '#ddd',
            }}
          />

          {/* Min Amount */}
          <input
            type="text"
            value={minAmount}
            onChange={(e) => setMinAmount(e.target.value)}
            placeholder={t('transactions.filters.minAmount')}
            className="qt-input"
            style={{
              width: '100px',
              padding: '4px 8px',
              fontSize: '12px',
              backgroundColor: '#2b2b2b',
              border: '1px solid #1a1a1a',
              color: '#ddd',
            }}
          />
        </div>

        {/* Date Range Row (shown only when Range... is selected) */}
        {dateFilter === 'range' && (
          <div
            className="qt-frame-secondary"
            style={{
              marginBottom: '4px',
              padding: '4px 8px',
              border: '1px solid #4a4a4a',
              borderRadius: '2px',
              backgroundColor: '#3a3a3a',
            }}
          >
            <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center' }}>
              <label className="qt-label" style={{ fontSize: '12px' }}>{t('transactions.filters.range').replace('...', '')}:</label>
              <input
                type="date"
                value={dateRangeFrom}
                onChange={(e) => setDateRange(e.target.value, dateRangeTo)}
                className="qt-input"
                style={{
                  padding: '4px 8px',
                  fontSize: '12px',
                  backgroundColor: '#2b2b2b',
                  border: '1px solid #1a1a1a',
                  color: '#ddd',
                  minWidth: '100px',
                }}
              />
              <label className="qt-label" style={{ fontSize: '12px' }}>{t('transactions.filters.rangeTo')}</label>
              <input
                type="date"
                value={dateRangeTo}
                onChange={(e) => setDateRange(dateRangeFrom, e.target.value)}
                className="qt-input"
                style={{
                  padding: '4px 8px',
                  fontSize: '12px',
                  backgroundColor: '#2b2b2b',
                  border: '1px solid #1a1a1a',
                  color: '#ddd',
                  minWidth: '100px',
                }}
              />
            </div>
          </div>
        )}

        {/* New Transaction Notification Banner */}
        {newTransactionCount > 0 && (
          <button
            type="button"
            onClick={handleNewTransactionBannerClick}
            style={{
              width: '100%',
              padding: '6px 12px',
              marginBottom: '4px',
              backgroundColor: '#1a3a5c',
              border: '1px solid #2a5a8c',
              borderRadius: '2px',
              color: '#7ab8ff',
              fontSize: '12px',
              cursor: 'pointer',
              textAlign: 'center',
            }}
          >
            {newTransactionCount} new transaction{newTransactionCount !== 1 ? 's' : ''} - click to refresh
          </button>
        )}

        {/* Transaction Table */}
        <div
          className="qt-frame-secondary"
          style={{
            flex: 1,
            minHeight: 0,
            border: '1px solid #4a4a4a',
            borderRadius: '2px',
            backgroundColor: '#2b2b2b',
            overflow: 'auto',
          }}
        >
          {isLoading ? (
            <TransactionsSkeleton />
          ) : error ? (
            <div className="flex items-center justify-center h-full text-red-500">
              {t('transactions.error', { message: error })}
            </div>
          ) : transactions.length === 0 ? (
            <div className="flex items-center justify-center h-full text-gray-500">
              {t('transactions.noTransactions')}
            </div>
          ) : (
            <table className="w-full border-collapse">
              <thead className="sticky top-0 bg-gray-800 z-10">
                <tr className="border-b border-gray-600">
                  {/* Select All Checkbox */}
                  <th className="py-2 px-2 w-8">
                    <input
                      type="checkbox"
                      checked={allSelected}
                      onChange={handleSelectAllChange}
                      className="w-4 h-4 cursor-pointer"
                      style={{ accentColor: '#5a9cff' }}
                    />
                  </th>

                  {/* Status Icon Header (no sort) */}
                  <th className="py-2 px-2 w-12 text-left text-sm font-medium text-gray-400">
                    {/* Empty - icon column */}
                  </th>

                  <SortableHeader
                    label="Date"
                    column="date"
                    currentColumn={sortColumn}
                    direction={sortDirection}
                    onSort={setSortColumn}
                  />

                  <SortableHeader
                    label="Type"
                    column="type"
                    currentColumn={sortColumn}
                    direction={sortDirection}
                    onSort={setSortColumn}
                  />

                  <SortableHeader
                    label="Address"
                    column="address"
                    currentColumn={sortColumn}
                    direction={sortDirection}
                    onSort={setSortColumn}
                    className="flex-1"
                  />

                  <SortableHeader
                    label="Amount"
                    column="amount"
                    currentColumn={sortColumn}
                    direction={sortDirection}
                    onSort={setSortColumn}
                    className="text-right"
                  />
                </tr>
              </thead>
              <tbody>
                {transactions.map((tx) => (
                  <TransactionRow
                    key={`${tx.txid}:${tx.vout}`}
                    transaction={tx}
                    isSelected={isSelected(`${tx.txid}:${tx.vout}`)}
                    onToggleSelect={() => toggleSelection(`${tx.txid}:${tx.vout}`)}
                    onClick={() => toggleSelection(`${tx.txid}:${tx.vout}`)}
                    onDoubleClick={() => handleTransactionDoubleClick(tx)}
                    onContextMenu={handleContextMenu}
                  />
                ))}
              </tbody>
            </table>
          )}
        </div>

        {/* Context Menu */}
        {contextMenu.visible && contextMenu.transaction && (
          <div
            ref={contextMenuRef}
            role="menu"
            aria-label="Transaction context menu"
            style={{
              position: 'fixed',
              top: contextMenu.y,
              left: contextMenu.x,
              backgroundColor: '#3a3a3a',
              border: '1px solid #555',
              borderRadius: '4px',
              boxShadow: '0 4px 12px rgba(0,0,0,0.3)',
              zIndex: 1000,
              minWidth: '200px',
              padding: '4px 0',
            }}
          >
            {/* Copy address */}
            <button
              type="button"
              role="menuitem"
              onClick={handleCopyAddress}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleCopyAddress(); } }}
              disabled={!contextMenu.transaction.address}
              style={{
                width: '100%',
                padding: '6px 12px',
                backgroundColor: 'transparent',
                border: 'none',
                color: contextMenu.transaction.address ? '#ddd' : '#666',
                fontSize: '12px',
                textAlign: 'left',
                cursor: contextMenu.transaction.address ? 'pointer' : 'not-allowed',
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
              }}
              onMouseEnter={(e) => {
                if (contextMenu.transaction?.address) {
                  e.currentTarget.style.backgroundColor = '#4a5568';
                }
              }}
              onMouseLeave={(e) => (e.currentTarget.style.backgroundColor = 'transparent')}
            >
              <Copy size={14} />
              Copy address
            </button>

            {/* Copy label */}
            <button
              type="button"
              role="menuitem"
              onClick={handleCopyLabel}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleCopyLabel(); } }}
              disabled={!contextMenu.transaction.label}
              style={{
                width: '100%',
                padding: '6px 12px',
                backgroundColor: 'transparent',
                border: 'none',
                color: contextMenu.transaction.label ? '#ddd' : '#666',
                fontSize: '12px',
                textAlign: 'left',
                cursor: contextMenu.transaction.label ? 'pointer' : 'not-allowed',
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
              }}
              onMouseEnter={(e) => {
                if (contextMenu.transaction?.label) {
                  e.currentTarget.style.backgroundColor = '#4a5568';
                }
              }}
              onMouseLeave={(e) => (e.currentTarget.style.backgroundColor = 'transparent')}
            >
              <Copy size={14} />
              Copy label
            </button>

            {/* Copy amount */}
            <button
              type="button"
              role="menuitem"
              onClick={handleCopyAmount}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleCopyAmount(); } }}
              style={{
                width: '100%',
                padding: '6px 12px',
                backgroundColor: 'transparent',
                border: 'none',
                color: '#ddd',
                fontSize: '12px',
                textAlign: 'left',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
              }}
              onMouseEnter={(e) => (e.currentTarget.style.backgroundColor = '#4a5568')}
              onMouseLeave={(e) => (e.currentTarget.style.backgroundColor = 'transparent')}
            >
              <Copy size={14} />
              Copy amount
            </button>

            {/* Copy transaction ID */}
            <button
              type="button"
              role="menuitem"
              onClick={handleCopyTxID}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleCopyTxID(); } }}
              style={{
                width: '100%',
                padding: '6px 12px',
                backgroundColor: 'transparent',
                border: 'none',
                color: '#ddd',
                fontSize: '12px',
                textAlign: 'left',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
              }}
              onMouseEnter={(e) => (e.currentTarget.style.backgroundColor = '#4a5568')}
              onMouseLeave={(e) => (e.currentTarget.style.backgroundColor = 'transparent')}
            >
              <Copy size={14} />
              Copy transaction ID
            </button>

            {/* Separator */}
            <div role="separator" style={{ height: '1px', backgroundColor: '#555', margin: '4px 0' }} />

            {/* Edit label */}
            <button
              type="button"
              role="menuitem"
              onClick={handleEditLabel}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleEditLabel(); } }}
              disabled={!contextMenu.transaction.address}
              style={{
                width: '100%',
                padding: '6px 12px',
                backgroundColor: 'transparent',
                border: 'none',
                color: contextMenu.transaction.address ? '#ddd' : '#666',
                fontSize: '12px',
                textAlign: 'left',
                cursor: contextMenu.transaction.address ? 'pointer' : 'not-allowed',
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
              }}
              onMouseEnter={(e) => {
                if (contextMenu.transaction?.address) {
                  e.currentTarget.style.backgroundColor = '#4a5568';
                }
              }}
              onMouseLeave={(e) => (e.currentTarget.style.backgroundColor = 'transparent')}
            >
              <Edit size={14} />
              Edit label
            </button>

            {/* Show transaction details */}
            <button
              type="button"
              role="menuitem"
              onClick={handleShowDetails}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleShowDetails(); } }}
              style={{
                width: '100%',
                padding: '6px 12px',
                backgroundColor: 'transparent',
                border: 'none',
                color: '#ddd',
                fontSize: '12px',
                textAlign: 'left',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
              }}
              onMouseEnter={(e) => (e.currentTarget.style.backgroundColor = '#4a5568')}
              onMouseLeave={(e) => (e.currentTarget.style.backgroundColor = 'transparent')}
            >
              <FileText size={14} />
              Show transaction details
            </button>

            {/* Block Explorer URLs (if configured) */}
            {blockExplorerUrls.length > 0 && (
              <>
                {/* Separator */}
                <div role="separator" style={{ height: '1px', backgroundColor: '#555', margin: '4px 0' }} />

                {/* One menu item per configured explorer */}
                {blockExplorerUrls.map((explorer, index) => (
                  <button
                    key={index}
                    type="button"
                    role="menuitem"
                    onClick={() => handleOpenBlockExplorer(explorer.url)}
                    onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleOpenBlockExplorer(explorer.url); } }}
                    style={{
                      width: '100%',
                      padding: '6px 12px',
                      backgroundColor: 'transparent',
                      border: 'none',
                      color: '#ddd',
                      fontSize: '12px',
                      textAlign: 'left',
                      cursor: 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      gap: '8px',
                    }}
                    onMouseEnter={(e) => (e.currentTarget.style.backgroundColor = '#4a5568')}
                    onMouseLeave={(e) => (e.currentTarget.style.backgroundColor = 'transparent')}
                  >
                    <ExternalLink size={14} />
                    {blockExplorerUrls.length === 1 ? 'Open in block explorer' : `Open in ${explorer.hostname}`}
                  </button>
                ))}
              </>
            )}

          </div>
        )}

        {/* Footer */}
        <div
          className="qt-frame-secondary"
          style={{
            marginTop: '8px',
            padding: '8px',
            border: '1px solid #4a4a4a',
            borderRadius: '2px',
            backgroundColor: '#3a3a3a',
          }}
        >
          {/* Row 1: Pagination controls */}
          {totalPages > 1 && (
            <div className="qt-hbox" style={{ justifyContent: 'center', alignItems: 'center', gap: '4px', marginBottom: '6px' }}>
              {/* First page */}
              <button
                type="button"
                onClick={goToFirstPage}
                disabled={currentPage <= 1 || isLoading}
                style={{
                  padding: '2px 6px',
                  backgroundColor: 'transparent',
                  border: '1px solid #555',
                  borderRadius: '2px',
                  color: currentPage <= 1 ? '#555' : '#ddd',
                  cursor: currentPage <= 1 ? 'not-allowed' : 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                }}
                title="First page"
              >
                <ChevronsLeft size={14} />
              </button>

              {/* Previous page */}
              <button
                type="button"
                onClick={goToPrevPage}
                disabled={currentPage <= 1 || isLoading}
                style={{
                  padding: '2px 6px',
                  backgroundColor: 'transparent',
                  border: '1px solid #555',
                  borderRadius: '2px',
                  color: currentPage <= 1 ? '#555' : '#ddd',
                  cursor: currentPage <= 1 ? 'not-allowed' : 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                }}
                title="Previous page"
              >
                <ChevronLeft size={14} />
              </button>

              {/* Page indicator */}
              <span style={{ fontSize: '12px', color: '#ddd', padding: '0 8px' }}>
                Page {currentPage} of {totalPages}
              </span>

              {/* Next page */}
              <button
                type="button"
                onClick={goToNextPage}
                disabled={currentPage >= totalPages || isLoading}
                style={{
                  padding: '2px 6px',
                  backgroundColor: 'transparent',
                  border: '1px solid #555',
                  borderRadius: '2px',
                  color: currentPage >= totalPages ? '#555' : '#ddd',
                  cursor: currentPage >= totalPages ? 'not-allowed' : 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                }}
                title="Next page"
              >
                <ChevronRight size={14} />
              </button>

              {/* Last page */}
              <button
                type="button"
                onClick={goToLastPage}
                disabled={currentPage >= totalPages || isLoading}
                style={{
                  padding: '2px 6px',
                  backgroundColor: 'transparent',
                  border: '1px solid #555',
                  borderRadius: '2px',
                  color: currentPage >= totalPages ? '#555' : '#ddd',
                  cursor: currentPage >= totalPages ? 'not-allowed' : 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                }}
                title="Last page"
              >
                <ChevronsRight size={14} />
              </button>

              {/* Page size selector */}
              <select
                value={pageSize}
                onChange={(e) => setPageSize(Number(e.target.value) as PageSize)}
                style={{
                  marginLeft: '12px',
                  padding: '2px 4px',
                  fontSize: '11px',
                  backgroundColor: '#2b2b2b',
                  border: '1px solid #555',
                  borderRadius: '2px',
                  color: '#ddd',
                }}
              >
                {PAGE_SIZES.map((size) => (
                  <option key={size} value={size}>
                    {size} per page
                  </option>
                ))}
              </select>
            </div>
          )}

          {/* Row 2: Count, selected amount, export */}
          <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
            {/* Left side: Transaction count and selected amount */}
            <div className="qt-hbox" style={{ gap: '24px', alignItems: 'center' }}>
              {/* Transaction Count Display */}
              <div className="qt-label" style={{ fontSize: '12px' }}>
                {total === totalAll ? (
                  <span>
                    {total > 0
                      ? `Showing ${rangeStart}-${rangeEnd} of ${total} transaction${total !== 1 ? 's' : ''}`
                      : `0 transactions`}
                  </span>
                ) : (
                  <span>
                    {total > 0
                      ? `Showing ${rangeStart}-${rangeEnd} of ${total} filtered (${totalAll} total)`
                      : `0 of ${totalAll} transaction${totalAll !== 1 ? 's' : ''}`}
                  </span>
                )}
              </div>

              {/* Selected Amount Display */}
              <div className="qt-label" style={{ fontSize: '12px' }}>
                {t('transactions.footer.selectedAmount')}{' '}
                <span style={{ fontFamily: 'monospace' }}>
                  {selectedCount > 0
                    ? `${selectedAmount >= 0 ? '+' : ''}${formatAmount(Math.abs(selectedAmount))}`
                    : formatAmount(0)}
                </span>
                {selectedCount > 0 && (
                  <span style={{ color: '#888', marginLeft: '8px' }}>
                    ({selectedCount} selected)
                  </span>
                )}
              </div>
            </div>

            {/* Export Button */}
            <button
              type="button"
              className="qt-button"
              onClick={handleExport}
              disabled={isExporting}
              style={{
                padding: '4px 16px',
                fontSize: '12px',
                backgroundColor: '#404040',
                border: '1px solid #555',
                borderRadius: '3px',
                color: isExporting ? '#888' : '#ddd',
                cursor: isExporting ? 'not-allowed' : 'pointer',
              }}
            >
              {isExporting ? 'Exporting...' : t('transactions.footer.export')}
            </button>
          </div>
        </div>
      </div>

      {/* Transaction Details Dialog */}
      <TransactionDetailsDialog
        isOpen={detailsDialogOpen}
        onClose={handleCloseDetailsDialog}
        transaction={selectedTransaction}
      />

      {/* Edit Label Dialog */}
      <EditLabelDialog
        isOpen={editLabelDialogOpen}
        address={editLabelAddress}
        currentLabel={editLabelCurrentLabel}
        onClose={handleCloseEditLabelDialog}
        onLabelUpdated={handleLabelUpdated}
      />
    </div>
  );
};
