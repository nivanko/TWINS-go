import React, { useEffect, useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import {
  X,
  ChevronUp,
  ChevronDown,
  Copy,
  Download,
  Plus,
  ArrowRight,
  ChevronsLeft,
  ChevronLeft,
  ChevronRight,
  ChevronsRight,
  Search,
} from 'lucide-react';
import { useReceive, useReceivingAddresses } from '@/store/useStore';
import { useDisplayUnits } from '@/shared/hooks/useDisplayUnits';
import { writeToClipboard } from '@/shared/utils/clipboard';
import {
  RECV_ADDRS_PAGE_SIZES,
  type RecvAddrsPageSize,
} from '@/store/slices/receivingAddressesSlice';

interface ReceivingAddressesDialogProps {
  isOpen: boolean;
  onClose: () => void;
}

// Shared column widths so the header row aligns with the data rows.
const COL_LABEL_WIDTH = '150px';
const COL_BALANCE_WIDTH = '160px';

export const ReceivingAddressesDialog: React.FC<ReceivingAddressesDialogProps> = ({
  isOpen,
  onClose,
}) => {
  const { t } = useTranslation('wallet');
  const { isGeneratingAddress, generateNewAddress, selectAddressForRequest } = useReceive();

  const {
    rows,
    total,
    totalAll,
    totalPages,
    isLoading,
    error,
    currentPage,
    pageSize,
    hideZeroBalance,
    searchText,
    sortColumn,
    sortDirection,
    fetchPage,
    setPageSize,
    goToFirstPage,
    goToPrevPage,
    goToNextPage,
    goToLastPage,
    setHideZeroBalance,
    setSearchText,
    setSortColumn,
    resetTransient,
    exportCSV,
  } = useReceivingAddresses();

  const { formatAmount, unitLabel } = useDisplayUnits();

  // Toast feedback for inline copy / export actions.
  const [copyFeedback, setCopyFeedback] = useState<string | null>(null);

  // New address label prompt state.
  const [showLabelPrompt, setShowLabelPrompt] = useState(false);
  const [newAddressLabel, setNewAddressLabel] = useState('');

  // Auto-clear the toast after 2s. Cleanup prevents memory leaks if the
  // component unmounts before the timer fires.
  useEffect(() => {
    if (!copyFeedback) return;
    const timeoutId = setTimeout(() => setCopyFeedback(null), 2000);
    return () => clearTimeout(timeoutId);
  }, [copyFeedback]);

  // Fetch the first page whenever the dialog opens. The slice retains its
  // pagination/sort/filter state across opens (per user request:
  // pageSize/hideZeroBalance/sortColumn/sortDirection persist via
  // localStorage), but we always re-fetch fresh data on each open in case
  // balances or labels changed since last view.
  useEffect(() => {
    if (isOpen) {
      fetchPage(1);
    }
  }, [isOpen, fetchPage]);

  // Wrapped close handler — resets transient state (toast, label prompt,
  // search input) before delegating to parent's onClose. The slice's
  // resetTransient() also cancels any pending search debounce so a stale
  // fetch can't fire after the dialog closes.
  const handleClose = useCallback(() => {
    setCopyFeedback(null);
    setShowLabelPrompt(false);
    setNewAddressLabel('');
    resetTransient();
    onClose();
  }, [onClose, resetTransient]);

  // Per-row copy: copies address directly without selection state.
  const handleCopyAddress = useCallback(
    async (address: string) => {
      const ok = await writeToClipboard(address);
      setCopyFeedback(
        ok ? t('receive.addressesDialog.addressCopied') : t('receive.addressesDialog.copyFailed')
      );
    },
    [t]
  );

  // Per-row picker: selects the address for the Payment Request form on the
  // Receive page and closes the dialog. Resets transient state via the same
  // path as handleClose so nothing lingers across the next reopen.
  const handleUseForRequest = useCallback(
    (address: string) => {
      setCopyFeedback(null);
      setShowLabelPrompt(false);
      setNewAddressLabel('');
      resetTransient();
      selectAddressForRequest(address);
    },
    [selectAddressForRequest, resetTransient]
  );

  // Export the full filtered set via the backend handler.
  const handleExport = useCallback(async () => {
    if (total === 0) return;
    try {
      const saved = await exportCSV();
      if (saved) {
        setCopyFeedback(t('receive.addressesDialog.exportSuccess'));
      }
    } catch {
      setCopyFeedback(t('receive.addressesDialog.exportFailed'));
    }
  }, [exportCSV, total, t]);

  // New address creation flow.
  const handleNewAddress = useCallback(() => {
    setShowLabelPrompt(true);
    setNewAddressLabel('');
  }, []);

  const handleCreateAddress = useCallback(async () => {
    const result = await generateNewAddress(newAddressLabel);
    if (result) {
      setShowLabelPrompt(false);
      setNewAddressLabel('');
      // Refresh the page so the new address appears in the table immediately.
      fetchPage(currentPage);
    }
  }, [generateNewAddress, newAddressLabel, fetchPage, currentPage]);

  const handleCancelNewAddress = useCallback(() => {
    setShowLabelPrompt(false);
    setNewAddressLabel('');
  }, []);

  // Keyboard handler — Escape closes; Enter inside the label prompt creates.
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (showLabelPrompt) {
          handleCancelNewAddress();
        } else {
          handleClose();
        }
      } else if (e.key === 'Enter' && showLabelPrompt) {
        handleCreateAddress();
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, showLabelPrompt, handleClose, handleCancelNewAddress, handleCreateAddress]);

  if (!isOpen) return null;

  const exportDisabled = total === 0;

  // Pagination button enable/disable logic.
  const canGoPrev = currentPage > 1;
  const canGoNext = currentPage < totalPages;

  // Compute showing range "X-Y of Z" for the footer.
  const showingFrom = total === 0 ? 0 : (currentPage - 1) * pageSize + 1;
  const showingTo = Math.min(currentPage * pageSize, total);

  // Sort indicator helper — only renders the chevron when this column is
  // currently active.
  const renderSortIndicator = (column: 'label' | 'balance') => {
    if (sortColumn !== column) return null;
    return sortDirection === 'asc' ? <ChevronUp size={12} /> : <ChevronDown size={12} />;
  };

  return (
    <div
      className="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
      onClick={(e) => {
        if (e.target === e.currentTarget && !showLabelPrompt) {
          handleClose();
        }
      }}
      role="presentation"
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="receiving-addresses-title"
        aria-describedby="receiving-addresses-description"
        style={{
          backgroundColor: '#2f2f2f',
          border: '1px solid #3a3a3a',
          borderRadius: '8px',
          boxShadow: '0 8px 32px rgba(0, 0, 0, 0.5)',
          width: '900px',
          maxWidth: '95vw',
          // Pin the dialog to a fixed height so it does not resize when the
          // scrollable row area grows or shrinks in response to filter / sort
          // / search changes. Without this, toggling a filter checkbox would
          // shrink the dialog when the filtered set had fewer rows than the
          // current page could hold, causing a visible size change.
          //
          // Numbers and pattern match OptionsDialog.tsx exactly (see
          // features/settings/components/OptionsDialog.tsx:177-178). We do
          // NOT add a `maxHeight: '90vh'` cap here, because pairing it with
          // `minHeight: 480px` would cause `min-height` to override
          // `max-height` on viewports under 533px (per CSS 2.1 §10.7), and
          // the centered overlay would push the dialog header off-screen
          // with no way to scroll up to it. OptionsDialog has been in
          // production without a maxHeight for the same reason; trusting
          // the precedent here.
          height: '80vh',
          minHeight: '480px',
          display: 'flex',
          flexDirection: 'column',
        }}
      >
        {/* Header — title on the left, close on the right */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '14px 16px',
            borderBottom: '1px solid #3a3a3a',
            flexShrink: 0,
          }}
        >
          <h2
            id="receiving-addresses-title"
            style={{ fontSize: '14px', fontWeight: 600, color: '#ddd', margin: 0 }}
          >
            {t('receive.addressesDialog.title')}
          </h2>
          <button
            type="button"
            onClick={handleClose}
            aria-label="Close dialog"
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              width: '28px',
              height: '28px',
              background: 'none',
              border: '1px solid transparent',
              borderRadius: '6px',
              color: '#888',
              cursor: 'pointer',
              transition: 'color 0.15s, border-color 0.15s, background-color 0.15s',
            }}
            onMouseEnter={(e) => {
              const el = e.currentTarget;
              el.style.color = '#ddd';
              el.style.borderColor = '#4a4a4a';
              el.style.backgroundColor = '#383838';
            }}
            onMouseLeave={(e) => {
              const el = e.currentTarget;
              el.style.color = '#888';
              el.style.borderColor = 'transparent';
              el.style.backgroundColor = 'transparent';
            }}
          >
            <X size={16} />
          </button>
        </div>

        {/* Description */}
        <div
          style={{
            padding: '12px 16px 8px 16px',
            borderBottom: '1px solid #3a3a3a',
            flexShrink: 0,
          }}
        >
          <p
            id="receiving-addresses-description"
            style={{ fontSize: '12px', color: '#aaa', margin: 0, lineHeight: '1.4' }}
          >
            {t('receive.addressesDialog.description')}
          </p>
        </div>

        {/* Filter bar — checkboxes + search input */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '20px',
            padding: '10px 16px',
            borderBottom: '1px solid #3a3a3a',
            flexShrink: 0,
            flexWrap: 'wrap',
          }}
        >
          <label
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
              fontSize: '12px',
              color: '#ddd',
              cursor: 'pointer',
              userSelect: 'none',
            }}
            title={t('receive.addressesDialog.filters.hideZeroBalanceHelp')}
          >
            <input
              type="checkbox"
              checked={hideZeroBalance}
              onChange={(e) => setHideZeroBalance(e.target.checked)}
              style={{ cursor: 'pointer' }}
            />
            {t('receive.addressesDialog.filters.hideZeroBalance')}
          </label>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
              flex: 1,
              minWidth: '200px',
              backgroundColor: '#262626',
              border: '1px solid #3a3a3a',
              borderRadius: '6px',
              padding: '4px 10px',
            }}
          >
            <Search size={13} color="#888" />
            <input
              type="text"
              value={searchText}
              onChange={(e) => setSearchText(e.target.value)}
              placeholder={t('receive.addressesDialog.filters.searchPlaceholder')}
              aria-label="Search addresses"
              style={{
                flex: 1,
                background: 'transparent',
                border: 'none',
                outline: 'none',
                color: '#ddd',
                fontSize: '12px',
              }}
            />
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', flexShrink: 0 }}>
            <button
              type="button"
              onClick={handleNewAddress}
              disabled={isGeneratingAddress}
              aria-label="Create new receiving address"
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '4px',
                padding: '6px 12px',
                fontSize: '12px',
                backgroundColor: '#4a7c59',
                border: '1px solid #5a8c69',
                borderRadius: '6px',
                color: '#fff',
                cursor: isGeneratingAddress ? 'not-allowed' : 'pointer',
                opacity: isGeneratingAddress ? 0.5 : 1,
                transition: 'background-color 0.15s, border-color 0.15s',
              }}
              onMouseEnter={(e) => {
                if (!isGeneratingAddress) {
                  const el = e.currentTarget;
                  el.style.backgroundColor = '#5a8c69';
                  el.style.borderColor = '#6a9c79';
                }
              }}
              onMouseLeave={(e) => {
                const el = e.currentTarget;
                el.style.backgroundColor = '#4a7c59';
                el.style.borderColor = '#5a8c69';
              }}
            >
              <Plus size={13} />
              {t('receive.addressesDialog.new')}
            </button>
            <button
              type="button"
              onClick={handleExport}
              disabled={exportDisabled}
              aria-label="Export addresses to CSV"
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '4px',
                padding: '6px 12px',
                fontSize: '12px',
                backgroundColor: '#383838',
                border: '1px solid #4a4a4a',
                borderRadius: '6px',
                color: '#ddd',
                cursor: exportDisabled ? 'not-allowed' : 'pointer',
                opacity: exportDisabled ? 0.5 : 1,
                transition: 'background-color 0.15s, border-color 0.15s',
              }}
              onMouseEnter={(e) => {
                if (!exportDisabled) {
                  const el = e.currentTarget;
                  el.style.backgroundColor = '#454545';
                  el.style.borderColor = '#5a5a5a';
                }
              }}
              onMouseLeave={(e) => {
                const el = e.currentTarget;
                el.style.backgroundColor = '#383838';
                el.style.borderColor = '#4a4a4a';
              }}
            >
              <Download size={13} />
              {t('receive.addressesDialog.export')}
            </button>
          </div>
        </div>

        {/* Error Display */}
        {error && (
          <div
            style={{
              padding: '8px 16px',
              backgroundColor: '#4a2a2a',
              borderBottom: '1px solid #ff6666',
              flexShrink: 0,
            }}
          >
            <p style={{ fontSize: '12px', color: '#ff6666', margin: 0 }}>{error}</p>
          </div>
        )}

        {/* Card content area */}
        <div
          style={{
            flex: 1,
            display: 'flex',
            flexDirection: 'column',
            minHeight: 0,
            padding: '12px 16px 8px',
          }}
        >
          {/* Column header row (matches data row layout) */}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '12px',
              padding: '0 12px 8px 12px',
              borderBottom: '1px solid #3a3a3a',
              marginBottom: '8px',
              flexShrink: 0,
            }}
          >
            <span
              style={{
                flex: 1,
                color: '#aaa',
                fontSize: '11px',
                fontWeight: 600,
                minWidth: 0,
              }}
            >
              {t('receive.addressesDialog.addressColumn')}
            </span>
            <button
              type="button"
              onClick={() => setSortColumn('label')}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '4px',
                width: COL_LABEL_WIDTH,
                background: 'none',
                border: 'none',
                padding: 0,
                color: sortColumn === 'label' ? '#ddd' : '#aaa',
                fontSize: '11px',
                fontWeight: 600,
                cursor: 'pointer',
                textAlign: 'left',
                flexShrink: 0,
              }}
              aria-label={t('receive.addressesDialog.sort.sortBy', {
                column: t('receive.addressesDialog.labelColumn'),
              })}
            >
              {t('receive.addressesDialog.labelColumn')}
              {renderSortIndicator('label')}
            </button>
            <button
              type="button"
              onClick={() => setSortColumn('balance')}
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'flex-end',
                gap: '4px',
                width: COL_BALANCE_WIDTH,
                background: 'none',
                border: 'none',
                padding: 0,
                color: sortColumn === 'balance' ? '#ddd' : '#aaa',
                fontSize: '11px',
                fontWeight: 600,
                cursor: 'pointer',
                textAlign: 'right',
                flexShrink: 0,
              }}
              aria-label={t('receive.addressesDialog.sort.sortBy', {
                column: t('receive.addressesDialog.balanceColumn'),
              })}
            >
              {t('receive.addressesDialog.balanceColumn')} ({unitLabel})
              {renderSortIndicator('balance')}
            </button>
            {/* Spacer for the row-level picker button column */}
            <div style={{ width: '24px', flexShrink: 0 }} />
          </div>

          {/* Scrollable rows */}
          {/*
            Render branch rules (to prevent size/blink during filter/sort/search):

            1. Initial load (no rows cached yet AND isLoading) → show "Loading..."
               placeholder. This is the ONLY state that displays a placeholder.
            2. Filtered-empty (rows.length === 0 AND !isLoading) → show empty
               message. This distinguishes "no addresses at all" from "no matches
               for current filter".
            3. Has rows → always render the row list, even if isLoading is true.
               Previously we replaced rows with the "Loading..." placeholder on
               every fetch, which caused a visible flash (rows → placeholder →
               rows) every time the user toggled a filter/sort/search. The slice
               keeps the old rows in state during fetch, so we can safely keep
               them visible while the new page loads.

            Design note (intentional divergence from the Transactions page):
            The Transactions page replaces its rows with a skeleton loader on
            every refetch (see features/wallet/pages/Transactions.tsx and
            store/slices/transactionsSlice.ts:289-290). We deliberately do NOT
            do that here, because filter/sort/search interactions on a small
            address list typically return a similar result set, and a skeleton
            flash on every keystroke is more jarring than briefly seeing the
            previous page's rows. Opacity dimming was considered and rejected
            for lower visual noise. If you ever migrate this dialog into the
            unified address book, evaluate whether to keep this behavior or
            adopt the Transactions skeleton convention.
          */}
          <div style={{ flex: 1, overflowY: 'auto', overflowX: 'hidden', minHeight: 0 }}>
            {rows.length === 0 ? (
              <div
                style={{
                  textAlign: 'center',
                  color: '#666',
                  padding: '32px',
                  fontSize: '12px',
                }}
              >
                {isLoading
                  ? t('receive.addressesDialog.loading')
                  : totalAll === 0
                    ? t('receive.addressesDialog.noAddresses')
                    : t('receive.addressesDialog.noMatches')}
              </div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                {rows.map((addr) => {
                  const balance = addr.balance || 0;
                  const hasBalance = balance > 0;
                  return (
                    <div
                      key={addr.address}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: '12px',
                        padding: '8px 12px',
                        backgroundColor: '#2a2a2a',
                        borderRadius: '6px',
                        border: '1px solid transparent',
                        transition: 'border-color 0.15s',
                      }}
                      onMouseEnter={(e) => {
                        (e.currentTarget as HTMLDivElement).style.borderColor = '#444';
                      }}
                      onMouseLeave={(e) => {
                        (e.currentTarget as HTMLDivElement).style.borderColor = 'transparent';
                      }}
                    >
                      {/* Address + inline copy icon */}
                      <div
                        style={{
                          flex: 1,
                          display: 'flex',
                          alignItems: 'center',
                          gap: '6px',
                          minWidth: 0,
                        }}
                      >
                        <span
                          style={{
                            fontFamily: 'monospace',
                            fontSize: '12px',
                            color: '#ddd',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                          }}
                          title={addr.address}
                        >
                          {addr.address}
                        </span>
                        <button
                          type="button"
                          onClick={() => handleCopyAddress(addr.address)}
                          title={t('receive.addressesDialog.copy')}
                          aria-label={`${t('receive.addressesDialog.copy')} ${addr.address}`}
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            width: '24px',
                            height: '24px',
                            background: 'none',
                            border: '1px solid #3a3a3a',
                            borderRadius: '4px',
                            color: '#888',
                            cursor: 'pointer',
                            flexShrink: 0,
                            transition: 'color 0.15s, border-color 0.15s',
                          }}
                          onMouseEnter={(e) => {
                            const el = e.currentTarget;
                            el.style.color = '#ddd';
                            el.style.borderColor = '#555';
                          }}
                          onMouseLeave={(e) => {
                            const el = e.currentTarget;
                            el.style.color = '#888';
                            el.style.borderColor = '#3a3a3a';
                          }}
                        >
                          <Copy size={12} />
                        </button>
                      </div>

                      {/* Label */}
                      <div
                        style={{
                          width: COL_LABEL_WIDTH,
                          fontSize: '12px',
                          color: addr.label ? '#ddd' : '#666',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                          flexShrink: 0,
                        }}
                        title={addr.label || undefined}
                      >
                        {addr.label || t('receive.addressesDialog.noLabel')}
                      </div>

                      {/* Balance — numeric only; dim '-' when zero */}
                      <span
                        style={{
                          width: COL_BALANCE_WIDTH,
                          fontFamily: 'monospace',
                          fontSize: '12px',
                          color: hasBalance ? '#4a7c59' : '#555',
                          fontWeight: hasBalance ? 500 : 400,
                          textAlign: 'right',
                          flexShrink: 0,
                        }}
                      >
                        {hasBalance ? formatAmount(balance, false) : '-'}
                      </span>

                      {/* Use for payment request — visually separated at row edge */}
                      <button
                        type="button"
                        onClick={() => handleUseForRequest(addr.address)}
                        title={t('receive.addressesDialog.useForRequest')}
                        aria-label={`${t('receive.addressesDialog.useForRequest')} ${addr.address}`}
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                          width: '24px',
                          height: '24px',
                          background: 'none',
                          border: '1px solid #3a3a3a',
                          borderRadius: '4px',
                          color: '#6699cc',
                          cursor: 'pointer',
                          flexShrink: 0,
                          transition: 'color 0.15s, border-color 0.15s, background-color 0.15s',
                        }}
                        onMouseEnter={(e) => {
                          const el = e.currentTarget;
                          el.style.color = '#88bbee';
                          el.style.borderColor = '#6699cc';
                          el.style.backgroundColor = 'rgba(102, 153, 204, 0.1)';
                        }}
                        onMouseLeave={(e) => {
                          const el = e.currentTarget;
                          el.style.color = '#6699cc';
                          el.style.borderColor = '#3a3a3a';
                          el.style.backgroundColor = 'transparent';
                        }}
                      >
                        <ArrowRight size={12} />
                      </button>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </div>

        {/* Pagination footer */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: '12px',
            padding: '10px 16px',
            borderTop: '1px solid #3a3a3a',
            flexShrink: 0,
            flexWrap: 'wrap',
          }}
        >
          {/* Left: showing X-Y of Z (filtered/total) */}
          <div style={{ fontSize: '11px', color: '#888' }}>
            {total === 0
              ? t('receive.addressesDialog.pagination.noResults')
              : total < totalAll
                ? t('receive.addressesDialog.pagination.showingFiltered', {
                    from: showingFrom,
                    to: showingTo,
                    total,
                    totalAll,
                  })
                : t('receive.addressesDialog.pagination.showing', {
                    from: showingFrom,
                    to: showingTo,
                    total,
                  })}
          </div>

          {/* Center: First/Prev/Page X of Y/Next/Last */}
          <div style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
            <PaginationButton
              onClick={goToFirstPage}
              disabled={!canGoPrev}
              ariaLabel={t('receive.addressesDialog.pagination.first')}
              icon={<ChevronsLeft size={14} />}
            />
            <PaginationButton
              onClick={goToPrevPage}
              disabled={!canGoPrev}
              ariaLabel={t('receive.addressesDialog.pagination.previous')}
              icon={<ChevronLeft size={14} />}
            />
            <span
              style={{
                fontSize: '11px',
                color: '#aaa',
                padding: '0 8px',
                minWidth: '80px',
                textAlign: 'center',
              }}
            >
              {t('receive.addressesDialog.pagination.pageOf', {
                current: currentPage,
                total: Math.max(totalPages, 1),
              })}
            </span>
            <PaginationButton
              onClick={goToNextPage}
              disabled={!canGoNext}
              ariaLabel={t('receive.addressesDialog.pagination.next')}
              icon={<ChevronRight size={14} />}
            />
            <PaginationButton
              onClick={goToLastPage}
              disabled={!canGoNext}
              ariaLabel={t('receive.addressesDialog.pagination.last')}
              icon={<ChevronsRight size={14} />}
            />
          </div>

          {/* Right: page size selector */}
          <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
            <label htmlFor="recv-addrs-page-size" style={{ fontSize: '11px', color: '#888' }}>
              {t('receive.addressesDialog.pagination.perPage')}:
            </label>
            <select
              id="recv-addrs-page-size"
              value={pageSize}
              onChange={(e) => setPageSize(Number(e.target.value) as RecvAddrsPageSize)}
              style={{
                backgroundColor: '#262626',
                border: '1px solid #3a3a3a',
                borderRadius: '4px',
                color: '#ddd',
                fontSize: '11px',
                padding: '3px 6px',
                cursor: 'pointer',
                outline: 'none',
              }}
            >
              {RECV_ADDRS_PAGE_SIZES.map((size) => (
                <option key={size} value={size}>
                  {size}
                </option>
              ))}
            </select>
          </div>
        </div>

        {/* Copy / Export Feedback Toast */}
        {copyFeedback && (
          <div
            role="status"
            aria-live="polite"
            style={{
              position: 'fixed',
              bottom: '36px',
              left: '50%',
              transform: 'translateX(-50%)',
              backgroundColor: '#333',
              color: '#ddd',
              padding: '8px 16px',
              borderRadius: '6px',
              boxShadow: '0 4px 12px rgba(0, 0, 0, 0.3)',
              fontSize: '12px',
              zIndex: 60,
              border: '1px solid #555',
            }}
          >
            {copyFeedback}
          </div>
        )}

        {/* New Address Label Prompt Modal */}
        {showLabelPrompt && (
          <div
            className="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
            onClick={(e) => {
              if (e.target === e.currentTarget) {
                handleCancelNewAddress();
              }
            }}
            role="presentation"
          >
            <div
              role="dialog"
              aria-modal="true"
              aria-labelledby="new-address-title"
              aria-describedby="new-address-description"
              style={{
                backgroundColor: '#2f2f2f',
                border: '1px solid #3a3a3a',
                borderRadius: '8px',
                boxShadow: '0 8px 32px rgba(0, 0, 0, 0.5)',
                width: '400px',
                padding: '20px',
              }}
            >
              <h3
                id="new-address-title"
                style={{ fontSize: '14px', fontWeight: 600, color: '#ddd', margin: '0 0 12px' }}
              >
                {t('receive.addressesDialog.newTitle')}
              </h3>
              <p
                id="new-address-description"
                style={{ fontSize: '12px', color: '#aaa', margin: '0 0 14px' }}
              >
                {t('receive.addressesDialog.newDescription')}
              </p>
              <input
                type="text"
                value={newAddressLabel}
                onChange={(e) => setNewAddressLabel(e.target.value)}
                placeholder={t('receive.addressesDialog.labelPlaceholder')}
                maxLength={100}
                autoFocus
                aria-label="Address label"
                style={{
                  width: '100%',
                  padding: '8px 12px',
                  fontSize: '12px',
                  backgroundColor: '#262626',
                  color: '#ddd',
                  border: '1px solid #3a3a3a',
                  borderRadius: '6px',
                  outline: 'none',
                  boxSizing: 'border-box',
                }}
              />
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'flex-end',
                  gap: '8px',
                  marginTop: '16px',
                }}
              >
                <button
                  type="button"
                  onClick={handleCancelNewAddress}
                  aria-label="Cancel creating address"
                  style={{
                    padding: '8px 16px',
                    fontSize: '12px',
                    backgroundColor: '#383838',
                    border: '1px solid #4a4a4a',
                    borderRadius: '6px',
                    color: '#ccc',
                    cursor: 'pointer',
                    transition: 'background-color 0.15s',
                  }}
                >
                  {t('receive.addressesDialog.cancel')}
                </button>
                <button
                  type="button"
                  onClick={handleCreateAddress}
                  disabled={isGeneratingAddress}
                  aria-label={
                    isGeneratingAddress
                      ? t('receive.addressesDialog.creating')
                      : t('receive.addressesDialog.ok')
                  }
                  style={{
                    padding: '8px 16px',
                    fontSize: '12px',
                    backgroundColor: '#4a7c59',
                    border: '1px solid #5a8c69',
                    borderRadius: '6px',
                    color: '#fff',
                    cursor: isGeneratingAddress ? 'wait' : 'pointer',
                    opacity: isGeneratingAddress ? 0.7 : 1,
                    transition: 'background-color 0.15s',
                  }}
                >
                  {isGeneratingAddress
                    ? t('receive.addressesDialog.creating')
                    : t('receive.addressesDialog.ok')}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
};

// ----------------------------------------------------------------------------
// Local pagination button component — keeps the JSX in the main render tree
// concise and centralizes the disabled/hover styling for the four nav buttons.
// ----------------------------------------------------------------------------
interface PaginationButtonProps {
  onClick: () => void;
  disabled: boolean;
  ariaLabel: string;
  icon: React.ReactNode;
}

const PaginationButton: React.FC<PaginationButtonProps> = ({
  onClick,
  disabled,
  ariaLabel,
  icon,
}) => (
  <button
    type="button"
    onClick={onClick}
    disabled={disabled}
    aria-label={ariaLabel}
    title={ariaLabel}
    style={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      width: '26px',
      height: '26px',
      backgroundColor: '#383838',
      border: '1px solid #4a4a4a',
      borderRadius: '4px',
      color: disabled ? '#555' : '#ddd',
      cursor: disabled ? 'not-allowed' : 'pointer',
      opacity: disabled ? 0.5 : 1,
      transition: 'background-color 0.15s, border-color 0.15s',
    }}
    onMouseEnter={(e) => {
      if (!disabled) {
        const el = e.currentTarget;
        el.style.backgroundColor = '#454545';
        el.style.borderColor = '#5a5a5a';
      }
    }}
    onMouseLeave={(e) => {
      const el = e.currentTarget;
      el.style.backgroundColor = '#383838';
      el.style.borderColor = '#4a4a4a';
    }}
  >
    {icon}
  </button>
);
