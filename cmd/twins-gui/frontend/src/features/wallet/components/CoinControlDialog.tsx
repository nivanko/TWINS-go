import React, { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { X, Lock, Unlock, List, GitBranch, Copy, ChevronRight, ChevronLeft, ChevronsLeft, ChevronsRight, ChevronDown } from 'lucide-react';
import { useCoinControl } from '@/store/useStore';
import { UTXO } from '@/shared/types/wallet.types';
import { IsMasternodeCollateral } from '@wailsjs/go/main/App';
import { SimpleConfirmDialog } from '@/shared/components/SimpleConfirmDialog';
import { IconButton } from '@/shared/components/IconButton';

interface CoinControlDialogProps {
  recipientAmount?: number;
  feeRate?: number;
  recipientCount?: number;
}

export const CoinControlDialog: React.FC<CoinControlDialogProps> = ({
  recipientAmount = 0,
  feeRate = 0.0001,
  recipientCount = 1,
}) => {
  const { t } = useTranslation('wallet');
  const {
    isDialogOpen,
    utxos,
    isLoadingUTXOs,
    coinControl,
    viewMode,
    filterMode,
    sortMode,
    sortAscending,
    summary,
    closeDialog,
    cancelDialog,
    loadUTXOs,
    toggleCoinSelection,
    selectAll,
    unselectAll,
    toggleAllLocks,
    toggleCoinLock,
    setViewMode,
    setFilterMode,
    setSortMode,
    calculateSummary,
    // Tree view actions
    toggleAddressExpanded,
    selectAddressCoins,
    unselectAddressCoins,
    buildTreeView,
  } = useCoinControl();

  const [contextMenu, setContextMenu] = useState<{
    x: number;
    y: number;
    utxo: UTXO;
  } | null>(null);

  // Copy feedback state
  const [copyFeedback, setCopyFeedback] = useState<string | null>(null);

  // Collateral unlock warning state
  const [collateralWarning, setCollateralWarning] = useState<{
    utxo: UTXO;
    alias: string;
    action: 'unlock' | 'select';
  } | null>(null);

  // Pagination state (list view only)
  const PAGE_SIZES = [25, 50, 100] as const;
  const [currentPage, setCurrentPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(50);

  // Load UTXOs when dialog opens
  useEffect(() => {
    if (isDialogOpen) {
      loadUTXOs();
    }
  }, [isDialogOpen, loadUTXOs]);

  // Recalculate summary when selection or amounts change
  useEffect(() => {
    if (isDialogOpen) {
      try {
        calculateSummary(recipientAmount, feeRate, recipientCount);
      } catch (error) {
        console.error('Failed to calculate summary:', error);
      }
    }
  }, [isDialogOpen, recipientAmount, feeRate, recipientCount, coinControl.selectedCoins, calculateSummary]);

  // Filter and sort UTXOs
  const filteredAndSortedUTXOs = useMemo(() => {
    let filtered = [...utxos];

    // Apply filter
    switch (filterMode) {
      case 'spendable':
        filtered = filtered.filter((u) => u.spendable && !u.locked);
        break;
      case 'locked':
        filtered = filtered.filter((u) => u.locked);
        break;
      case 'all':
      default:
        break;
    }

    // Apply sort
    filtered.sort((a, b) => {
      let comparison = 0;

      switch (sortMode) {
        case 'amount':
          comparison = a.amount - b.amount;
          break;
        case 'confirmations':
          comparison = a.confirmations - b.confirmations;
          break;
        case 'address':
          comparison = a.address.localeCompare(b.address);
          break;
        case 'priority':
          comparison = a.priority - b.priority;
          break;
        case 'date':
          comparison = a.date - b.date;
          break;
        default:
          break;
      }

      return sortAscending ? comparison : -comparison;
    });

    return filtered;
  }, [utxos, filterMode, sortMode, sortAscending]);

  // Reset page to 1 when filter/sort/view changes
  useEffect(() => {
    setCurrentPage(1);
  }, [filterMode, sortMode, sortAscending, viewMode]);

  // Paginate UTXOs for list view
  const totalPages = Math.max(1, Math.ceil(filteredAndSortedUTXOs.length / pageSize));

  // Clamp page when UTXO count changes (e.g., new block shrinks the list)
  useEffect(() => {
    setCurrentPage(prev => prev > totalPages ? totalPages : prev);
  }, [totalPages]);
  const paginatedUTXOs = useMemo(() => {
    if (viewMode !== 'list') return filteredAndSortedUTXOs;
    const start = (currentPage - 1) * pageSize;
    return filteredAndSortedUTXOs.slice(start, start + pageSize);
  }, [filteredAndSortedUTXOs, currentPage, pageSize, viewMode]);

  // Check if coin is selected
  const isCoinSelected = (utxo: UTXO): boolean => {
    const key = `${utxo.txid}:${utxo.vout}`;
    return coinControl.selectedCoins.has(key);
  };

  // Check if all selectable coins are selected (for toggle button)
  const allSelected = useMemo(() => {
    const selectable = filteredAndSortedUTXOs.filter(
      (u) => u.spendable || coinControl.lockedCoins.has(`${u.txid}:${u.vout}`)
    );
    return selectable.length > 0 && selectable.every((u) => coinControl.selectedCoins.has(`${u.txid}:${u.vout}`));
  }, [filteredAndSortedUTXOs, coinControl.selectedCoins, coinControl.lockedCoins]);

  // Sort column click handler
  const handleSortClick = (column: typeof sortMode) => {
    setSortMode(column);
  };

  // Sort direction indicator
  const sortIndicator = (column: typeof sortMode) => {
    if (sortMode !== column) return null;
    return <span style={{ marginLeft: '4px', fontSize: '10px' }}>{sortAscending ? '▲' : '▼'}</span>;
  };

  // Format amount
  const formatAmount = (amount: number): string => {
    return amount.toFixed(8);
  };

  // Format date
  const formatDate = (timestamp: number): string => {
    const date = new Date(timestamp * 1000);
    return date.toLocaleString('en-US', {
      month: '2-digit',
      day: '2-digit',
      year: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    });
  };

  const handleContextMenu = (e: React.MouseEvent, utxo: UTXO) => {
    e.preventDefault();

    // Calculate position with viewport boundary checking
    const menuWidth = 200; // Approximate menu width
    const menuHeight = 190; // Approximate menu height
    const padding = 10;

    let x = e.clientX;
    let y = e.clientY;

    // Check right boundary
    if (x + menuWidth + padding > window.innerWidth) {
      x = window.innerWidth - menuWidth - padding;
    }

    // Check bottom boundary
    if (y + menuHeight + padding > window.innerHeight) {
      y = window.innerHeight - menuHeight - padding;
    }

    // Ensure not negative
    x = Math.max(padding, x);
    y = Math.max(padding, y);

    setContextMenu({ x, y, utxo });
  };

  const copyToClipboard = async (text: string, label: string) => {
    let success = false;
    try {
      await navigator.clipboard.writeText(text);
      success = true;
    } catch (error) {
      console.error('Failed to copy to clipboard:', error);
      // Fallback: Try using older execCommand method
      try {
        const textArea = document.createElement('textarea');
        textArea.value = text;
        textArea.style.position = 'fixed';
        textArea.style.opacity = '0';
        document.body.appendChild(textArea);
        textArea.select();
        document.execCommand('copy');
        document.body.removeChild(textArea);
        success = true;
      } catch (fallbackError) {
        console.error('Fallback copy also failed:', fallbackError);
      }
    }

    // Show feedback
    setCopyFeedback(success ? `${label} copied` : 'Copy failed');
    setTimeout(() => setCopyFeedback(null), 2000);
    setContextMenu(null);
  };

  useEffect(() => {
    if (contextMenu) {
      const handleClick = () => setContextMenu(null);
      document.addEventListener('click', handleClick);
      return () => document.removeEventListener('click', handleClick);
    }
  }, [contextMenu]);

  if (!isDialogOpen) {
    return null;
  }

  return (
    <div
      className="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
      onClick={(e) => {
        if (e.target === e.currentTarget) {
          cancelDialog();
        }
      }}
    >
      <div
        className="bg-[#2f2f2f] rounded-lg shadow-xl w-[950px] max-h-[600px] flex flex-col"
        style={{ border: '1px solid #3a3a3a' }}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-[#3a3a3a]">
          <h2 style={{ fontSize: '13px', fontWeight: 600, color: '#ccc', margin: 0 }}>
            {t('coinControl.dialogTitle')}
          </h2>
          <IconButton
            icon={<X size={14} />}
            title={t('coinControl.close', 'Close')}
            ariaLabel={t('coinControl.close', 'Close')}
            onClick={cancelDialog}
          />
        </div>

        {/* Summary Statistics */}
        <div className="grid grid-cols-4 gap-4 px-6 py-3 border-b border-[#3a3a3a] bg-[#2a2a2a]">
          <div>
            <div className="text-xs text-[#888]">{t('coinControl.quantity')}:</div>
            <div className="text-sm text-[#ddd]">{summary?.quantity || 0}</div>
          </div>
          <div>
            <div className="text-xs text-[#888]">{t('coinControl.amount')}:</div>
            <div className="text-sm text-[#ddd]">
              {summary ? formatAmount(summary.amount) : '0.00000000'}
            </div>
          </div>
          <div>
            <div className="text-xs text-[#888]">{t('coinControl.fee')}:</div>
            <div className="text-sm text-[#ddd]">
              {summary ? formatAmount(summary.fee) : '0.00000000'}
            </div>
          </div>
          <div>
            <div className="text-xs text-[#888]">{t('coinControl.afterFee')}:</div>
            <div className="text-sm text-[#ddd]">
              {summary ? formatAmount(summary.afterFee) : '0.00000000'}
            </div>
          </div>
          <div>
            <div className="text-xs text-[#888]">{t('coinControl.bytes')}:</div>
            <div className="text-sm text-[#ddd]">{summary?.bytes || 0}</div>
          </div>
          <div>
            <div className="text-xs text-[#888]">{t('coinControl.priority')}:</div>
            <div className="text-sm text-[#ddd]">{summary?.priority || 'medium'}</div>
          </div>
          <div>
            <div className="text-xs text-[#888]">{t('coinControl.dust')}:</div>
            <div className="text-sm text-[#ddd]">{summary?.dust ? t('coinControl.yes') : t('coinControl.no')}</div>
          </div>
          <div>
            <div className="text-xs text-[#888]">{t('coinControl.change')}:</div>
            <div className="text-sm text-[#ddd]">
              {summary ? formatAmount(summary.change) : '0.00000000'}
            </div>
          </div>
        </div>

        {/* Toolbar */}
        <div className="flex items-center justify-between px-6 py-3 border-b border-[#3a3a3a]">
          <div className="flex items-center gap-2">
            <button
              onClick={() => setViewMode(viewMode === 'tree' ? 'list' : 'tree')}
              className="px-3 py-1 text-sm bg-[#383838] border border-[#4a4a4a] text-[#ccc] rounded-md hover:bg-[#444444] transition-colors flex items-center gap-1"
            >
              {viewMode === 'tree' ? (
                <>
                  <GitBranch size={14} />
                  {t('coinControl.tree')}
                </>
              ) : (
                <>
                  <List size={14} />
                  {t('coinControl.list')}
                </>
              )}
            </button>

            <select
              value={filterMode}
              onChange={(e) => setFilterMode(e.target.value as any)}
              className="px-3 py-1 text-sm bg-[#252525] text-[#ddd] rounded border border-[#3a3a3a]"
            >
              <option value="all">{t('coinControl.filterAll')}</option>
              <option value="spendable">{t('coinControl.filterSpendable')}</option>
              <option value="locked">{t('coinControl.filterLocked')}</option>
            </select>

          </div>

          <div className="flex items-center gap-2">
            <button
              onClick={allSelected ? unselectAll : selectAll}
              className="px-3 py-1 text-sm bg-[#383838] border border-[#4a4a4a] text-[#ccc] rounded-md hover:bg-[#444444] transition-colors"
            >
              {allSelected ? t('coinControl.deselectAll') : t('coinControl.selectAll')}
            </button>
            <button
              disabled={!filteredAndSortedUTXOs.some(u => coinControl.selectedCoins.has(`${u.txid}:${u.vout}`))}
              onClick={() => {
                const selectedUTXOs = filteredAndSortedUTXOs.filter(u =>
                  coinControl.selectedCoins.has(`${u.txid}:${u.vout}`)
                );
                toggleAllLocks(selectedUTXOs);
              }}
              className="px-3 py-1 text-sm bg-[#383838] border border-[#4a4a4a] text-[#ccc] rounded-md hover:bg-[#444444] transition-colors flex items-center gap-1 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <Lock size={14} />
              {t('coinControl.toggleLock')}
            </button>
          </div>
        </div>

        {/* UTXO List/Tree */}
        <div className="flex-1 overflow-auto px-6 py-3">
          {isLoadingUTXOs ? (
            <div className="text-center py-8 text-[#999]">{t('coinControl.loadingUTXOs')}</div>
          ) : filteredAndSortedUTXOs.length === 0 ? (
            <div className="text-center py-8 text-[#999]">{t('coinControl.noUTXOs')}</div>
          ) : viewMode === 'list' ? (
            /* List View */
            <table className="w-full text-sm">
              <thead className="text-[#888] border-b border-[#3a3a3a]">
                <tr>
                  <th className="text-left py-2 w-14"></th>
                  <th
                    className="text-right py-2 w-32 cursor-pointer hover:text-[#ddd] select-none"
                    onClick={() => handleSortClick('amount')}
                  >
                    {t('coinControl.amount')}{sortIndicator('amount')}
                  </th>
                  <th className="text-left py-2">{t('coinControl.label')}</th>
                  <th
                    className="text-left py-2 cursor-pointer hover:text-[#ddd] select-none"
                    onClick={() => handleSortClick('address')}
                  >
                    {t('coinControl.address')}{sortIndicator('address')}
                  </th>
                  <th
                    className="text-center py-2 w-24 cursor-pointer hover:text-[#ddd] select-none"
                    onClick={() => handleSortClick('confirmations')}
                  >
                    {t('coinControl.confirmations')}{sortIndicator('confirmations')}
                  </th>
                  <th
                    className="text-center py-2 w-32 cursor-pointer hover:text-[#ddd] select-none"
                    onClick={() => handleSortClick('date')}
                  >
                    {t('coinControl.date')}{sortIndicator('date')}
                  </th>
                  <th className="py-2 w-8"></th>
                </tr>
              </thead>
              <tbody>
                {paginatedUTXOs.map((utxo) => {
                  const selected = isCoinSelected(utxo);
                  return (
                    <tr
                      key={`${utxo.txid}:${utxo.vout}`}
                      onContextMenu={(e) => handleContextMenu(e, utxo)}
                      className={`border-b border-[#3a3a3a] hover:bg-[#333] transition-colors cursor-pointer ${
                        utxo.locked ? 'opacity-50' : ''
                      }`}
                    >
                      <td className="py-2">
                        <div className="flex items-center gap-1">
                          <input
                            type="checkbox"
                            checked={selected}
                            onChange={async () => {
                              // Check if it's masternode collateral before selecting
                              if (!selected) {
                                try {
                                  const info = await IsMasternodeCollateral(utxo.txid, utxo.vout);
                                  if (info.isCollateral && info.alias) {
                                    setCollateralWarning({ utxo, alias: info.alias, action: 'select' });
                                    return;
                                  }
                                } catch (err) {
                                  console.error('Failed to check collateral status:', err);
                                }
                              }
                              toggleCoinSelection(utxo.txid, utxo.vout);
                            }}
                            className="cursor-pointer"
                          />
                          {utxo.locked && <Lock size={12} className="text-[#ff6b6b]" />}
                        </div>
                      </td>
                      <td className="text-right py-2 font-mono text-[#ddd]">
                        {formatAmount(utxo.amount)}
                      </td>
                      <td className="py-2 text-[#ddd]">{utxo.label || ''}</td>
                      <td className="py-2 font-mono text-[#ddd]">{utxo.address}</td>
                      <td className="text-center py-2 text-[#ddd]">{utxo.confirmations}</td>
                      <td className="text-center py-2 text-[#999]">
                        {formatDate(utxo.date)}
                      </td>
                      <td className="py-2 text-center">
                        <button
                          onClick={(e) => {
                            e.stopPropagation();
                            copyToClipboard(`${utxo.txid}:${utxo.vout}`, 'UTXO');
                          }}
                          className="text-[#666] hover:text-[#ddd] transition-colors"
                          title={t('coinControl.copyUtxo', 'Copy UTXO')}
                        >
                          <Copy size={12} />
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          ) : (
            /* Tree View - Grouped by Address */
            <div className="space-y-1">
              {buildTreeView().map((node) => (
                <div key={node.address} className="border border-[#3a3a3a] rounded-md">
                  {/* Address Header Row */}
                  <div
                    className="flex items-center gap-2 px-3 py-2 bg-[#2a2a2a] hover:bg-[#333] cursor-pointer"
                    onClick={() => node.address && toggleAddressExpanded(node.address)}
                  >
                    <button className="text-[#999] hover:text-[#ddd]">
                      {node.expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                    </button>
                    <input
                      type="checkbox"
                      checked={node.selected}
                      onChange={(e) => {
                        e.stopPropagation();
                        if (node.address) {
                          if (node.selected) {
                            unselectAddressCoins(node.address);
                          } else {
                            selectAddressCoins(node.address);
                          }
                        }
                      }}
                      className="cursor-pointer"
                    />
                    <span className="font-mono text-[#ddd] text-sm flex-1 truncate">
                      {node.address}
                    </span>
                    {node.label && (
                      <span className="text-[#999] text-sm">({node.label})</span>
                    )}
                    <span className="font-mono text-[#ddd] text-sm font-semibold">
                      {formatAmount(node.totalAmount || 0)}
                    </span>
                    <span className="text-[#999] text-xs">
                      ({node.utxos?.length || 0} UTXO{(node.utxos?.length || 0) !== 1 ? 's' : ''})
                    </span>
                  </div>

                  {/* Child UTXOs */}
                  {node.expanded && node.utxos && (
                    <div className="border-t border-[#3a3a3a]">
                      {node.utxos.map((utxo) => {
                        const selected = isCoinSelected(utxo);
                        return (
                          <div
                            key={`${utxo.txid}:${utxo.vout}`}
                            onContextMenu={(e) => handleContextMenu(e, utxo)}
                            className={`flex items-center gap-2 px-3 py-2 pl-10 hover:bg-[#333] cursor-pointer border-b border-[#3a3a3a] last:border-b-0 ${
                              utxo.locked ? 'opacity-50' : ''
                            }`}
                          >
                            <input
                              type="checkbox"
                              checked={selected}
                              onChange={async () => {
                                // Check if it's masternode collateral before selecting
                                if (!selected) {
                                  try {
                                    const info = await IsMasternodeCollateral(utxo.txid, utxo.vout);
                                    if (info.isCollateral && info.alias) {
                                      setCollateralWarning({ utxo, alias: info.alias, action: 'select' });
                                      return;
                                    }
                                  } catch (err) {
                                    console.error('Failed to check collateral status:', err);
                                  }
                                }
                                toggleCoinSelection(utxo.txid, utxo.vout);
                              }}
                              className="cursor-pointer"
                            />
                            {utxo.locked && <Lock size={12} className="text-[#ff6b6b]" />}
                            <span className="font-mono text-[#ddd] text-xs truncate flex-1" title={utxo.txid}>
                              tx:{utxo.txid.substring(0, 8)}...{utxo.txid.substring(utxo.txid.length - 8)}
                            </span>
                            <span className="text-[#ddd] text-xs w-20 text-center">
                              {t('coinControl.confirmationsShort', { count: utxo.confirmations })}
                            </span>
                            <span className="font-mono text-[#ddd] text-sm w-40 text-right">
                              {formatAmount(utxo.amount)}
                            </span>
                            <button
                              onClick={(e) => {
                                e.stopPropagation();
                                copyToClipboard(`${utxo.txid}:${utxo.vout}`, 'UTXO');
                              }}
                              className="text-[#666] hover:text-[#ddd] transition-colors ml-1"
                              title={t('coinControl.copyUtxo', 'Copy UTXO')}
                            >
                              <Copy size={12} />
                            </button>
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Pagination (list view only) */}
        {viewMode === 'list' && filteredAndSortedUTXOs.length > 0 && (
          <div className="flex items-center justify-between px-6 py-2 border-t border-[#3a3a3a]">
            <span style={{ fontSize: '11px', color: '#999' }}>
              Showing {Math.min((currentPage - 1) * pageSize + 1, filteredAndSortedUTXOs.length)}-{Math.min(currentPage * pageSize, filteredAndSortedUTXOs.length)} of {filteredAndSortedUTXOs.length} UTXOs
            </span>
            <div className="flex items-center gap-1">
              {/* First page */}
              <button
                type="button"
                onClick={() => setCurrentPage(1)}
                disabled={currentPage <= 1}
                style={{
                  padding: '2px 6px',
                  backgroundColor: 'transparent',
                  border: '1px solid #3a3a3a',
                  borderRadius: '4px',
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
                onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
                disabled={currentPage <= 1}
                style={{
                  padding: '2px 6px',
                  backgroundColor: 'transparent',
                  border: '1px solid #3a3a3a',
                  borderRadius: '4px',
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
                onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))}
                disabled={currentPage >= totalPages}
                style={{
                  padding: '2px 6px',
                  backgroundColor: 'transparent',
                  border: '1px solid #3a3a3a',
                  borderRadius: '4px',
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
                onClick={() => setCurrentPage(totalPages)}
                disabled={currentPage >= totalPages}
                style={{
                  padding: '2px 6px',
                  backgroundColor: 'transparent',
                  border: '1px solid #3a3a3a',
                  borderRadius: '4px',
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
                onChange={(e) => {
                  setPageSize(Number(e.target.value));
                  setCurrentPage(1);
                }}
                style={{
                  marginLeft: '12px',
                  padding: '2px 4px',
                  fontSize: '11px',
                  backgroundColor: '#252525',
                  border: '1px solid #3a3a3a',
                  borderRadius: '4px',
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
          </div>
        )}

        {/* Action Buttons */}
        <div className="flex items-center justify-end gap-3 px-6 py-4 border-t border-[#3a3a3a]">
          <button
            onClick={cancelDialog}
            className="px-4 py-2 text-sm bg-[#383838] border border-[#4a4a4a] text-[#ccc] rounded-md hover:bg-[#444444] transition-colors"
          >
            {t('coinControl.cancel')}
          </button>
          <button
            onClick={closeDialog}
            className="px-4 py-2 text-sm bg-[#4a7c59] border border-[#5a8c69] text-white font-medium rounded-md hover:bg-[#5a8c69] transition-colors"
          >
            {t('coinControl.applySelection')}
          </button>
        </div>

        {/* Copy feedback toast */}
        {copyFeedback && (
          <div
            className="fixed left-1/2 transform -translate-x-1/2 bg-[#333] text-[#ddd] px-4 py-2 rounded shadow-lg text-sm z-50 border border-[#555]"
            style={{ bottom: 'calc(var(--qt-statusbar-height) + 12px)' }}
          >
            {copyFeedback}
          </div>
        )}

        {/* Masternode Collateral Warning Dialog */}
        {collateralWarning && (
          <SimpleConfirmDialog
            isOpen={true}
            onCancel={() => setCollateralWarning(null)}
            onConfirm={() => {
              if (collateralWarning.action === 'unlock') {
                toggleCoinLock(collateralWarning.utxo.txid, collateralWarning.utxo.vout);
              } else {
                toggleCoinSelection(collateralWarning.utxo.txid, collateralWarning.utxo.vout);
              }
              setCollateralWarning(null);
            }}
            title={t('coinControl.collateralWarningTitle', 'Masternode Collateral Warning')}
            message={
              collateralWarning.action === 'unlock'
                ? `This UTXO is the collateral for masternode "${collateralWarning.alias}". Unlocking it allows it to be spent, which will disable your masternode. Are you sure you want to unlock it?`
                : `This UTXO is the collateral for masternode "${collateralWarning.alias}". Spending it will disable your masternode and stop rewards. Are you sure you want to select it for spending?`
            }
            confirmText={
              collateralWarning.action === 'unlock'
                ? t('coinControl.unlockAnyway', 'Unlock Anyway')
                : t('coinControl.selectAnyway', 'Select Anyway')
            }
            cancelText={t('common.cancel', 'Cancel')}
            isDestructive={true}
          />
        )}

        {contextMenu && (
          <div
            className="fixed bg-[#2f2f2f] border border-[#3a3a3a] rounded-md shadow-lg py-1 z-50"
            style={{ left: `${contextMenu.x}px`, top: `${contextMenu.y}px` }}
          >
            <button
              onClick={() => copyToClipboard(contextMenu.utxo.address, 'Address')}
              className="w-full text-left px-4 py-2 text-sm text-[#ddd] hover:bg-[#444] flex items-center gap-2"
            >
              <Copy size={14} />
              {t('coinControl.copyAddress')}
            </button>
            <button
              onClick={() => copyToClipboard(formatAmount(contextMenu.utxo.amount), 'Amount')}
              className="w-full text-left px-4 py-2 text-sm text-[#ddd] hover:bg-[#444] flex items-center gap-2"
            >
              <Copy size={14} />
              {t('coinControl.copyAmount')}
            </button>
            <button
              onClick={() => copyToClipboard(contextMenu.utxo.txid, 'Transaction ID')}
              className="w-full text-left px-4 py-2 text-sm text-[#ddd] hover:bg-[#444] flex items-center gap-2"
            >
              <Copy size={14} />
              {t('coinControl.copyTxid')}
            </button>
            <button
              onClick={() => copyToClipboard(`${contextMenu.utxo.txid}:${contextMenu.utxo.vout}`, 'UTXO')}
              className="w-full text-left px-4 py-2 text-sm text-[#ddd] hover:bg-[#444] flex items-center gap-2"
            >
              <Copy size={14} />
              {t('coinControl.copyUtxo', 'Copy UTXO')}
            </button>
            <div className="border-t border-[#3a3a3a] my-1"></div>
            <button
              onClick={async () => {
                // If unlocking, check if it's masternode collateral first
                if (contextMenu.utxo.locked) {
                  try {
                    const info = await IsMasternodeCollateral(contextMenu.utxo.txid, contextMenu.utxo.vout);
                    if (info.isCollateral && info.alias) {
                      // Show warning dialog instead of unlocking directly
                      setCollateralWarning({ utxo: contextMenu.utxo, alias: info.alias, action: 'unlock' });
                      setContextMenu(null);
                      return;
                    }
                  } catch (err) {
                    console.error('Failed to check collateral status:', err);
                  }
                }
                toggleCoinLock(contextMenu.utxo.txid, contextMenu.utxo.vout);
                setContextMenu(null);
              }}
              className="w-full text-left px-4 py-2 text-sm text-[#ddd] hover:bg-[#444] flex items-center gap-2"
            >
              {contextMenu.utxo.locked ? (
                <>
                  <Unlock size={14} />
                  {t('coinControl.unlockCoin')}
                </>
              ) : (
                <>
                  <Lock size={14} />
                  {t('coinControl.lockCoin')}
                </>
              )}
            </button>
          </div>
        )}
      </div>
    </div>
  );
};
