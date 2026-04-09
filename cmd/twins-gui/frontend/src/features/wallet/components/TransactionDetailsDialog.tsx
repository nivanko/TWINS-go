/**
 * TransactionDetailsDialog Component
 * Displays comprehensive transaction details matching Qt's TransactionDescDialog
 * Based on Qt wallet transactiondescdialog.cpp and transactiondesc.cpp
 */

import React, { useState, useEffect, useCallback } from 'react';
import { X, Copy, ExternalLink, Clock, Check, AlertTriangle, Info } from 'lucide-react';
import { BrowserOpenURL } from '@wailsjs/runtime/runtime';
import { core } from '@/shared/types/wallet.types';
import {
  getTransactionTypeIcon,
  getTransactionTypeLabel,
  formatTransactionDate,
  formatTransactionDateUTC,
} from '@/shared/utils/transactionIcons';
import { ConfirmationRing } from '@/shared/components/ConfirmationRing';
import { useDisplayUnits } from '@/shared/hooks/useDisplayUnits';
import { sanitizeText } from '@/shared/utils/sanitize';

interface TransactionDetailsDialogProps {
  isOpen: boolean;
  onClose: () => void;
  transaction: core.Transaction | null;
}

/**
 * Get human-readable status text based on confirmations and transaction state
 * Matches Qt's FormatTxStatus() in transactiondesc.cpp
 */
function getStatusText(
  confirmations: number,
  isConflicted = false,
  isCoinbase = false,
  isCoinstake = false,
  maturesIn = 0
): string {
  if (isConflicted) {
    return 'Conflicted';
  }
  if (confirmations === 0) {
    return 'Unconfirmed (0 confirmations)';
  }
  if ((isCoinbase || isCoinstake) && maturesIn > 0) {
    return `Immature (${confirmations} confirmations, matures in ${maturesIn} more blocks)`;
  }
  if (confirmations < 6) {
    return `Confirming (${confirmations}/6 confirmations)`;
  }
  return `Confirmed (${confirmations} confirmations)`;
}

/**
 * Get status color class based on confirmations
 */
function getStatusColorClass(
  confirmations: number,
  isConflicted = false,
  maturesIn = 0
): string {
  if (isConflicted) {
    return 'text-red-500';
  }
  if (maturesIn > 0) {
    return 'text-orange-400';
  }
  if (confirmations === 0) {
    return 'text-yellow-500';
  }
  if (confirmations < 6) {
    return 'text-blue-400';
  }
  return 'text-green-500';
}

/**
 * Format amount with proper sign, display unit conversion, and configured decimals.
 * Requires displayUnit and displayDigits from useDisplayUnits hook.
 */
function formatAmountWithSign(amount: number, fmtAmount: (n: number) => string): string {
  const sign = amount >= 0 ? '+' : '';
  // fmtAmount handles conversion, digits, and unit label; strip leading minus for negative to avoid double sign
  const formatted = fmtAmount(Math.abs(amount));
  return amount < 0 ? `-${formatted}` : `${sign}${formatted}`;
}

/**
 * Get amount color based on value
 */
function getAmountColorClass(amount: number): string {
  return amount < 0 ? 'text-red-400' : 'text-green-400';
}

/**
 * Truncate transaction ID for display with ellipsis
 */
function truncateTxId(txid: string, chars = 12): string {
  if (txid.length <= chars * 2) return txid;
  return `${txid.slice(0, chars)}...${txid.slice(-chars)}`;
}

/**
 * Determine if this is a receive transaction based on type
 */
function isReceiveTransaction(type: string): boolean {
  return type.startsWith('receive') ||
         type === 'generated' ||
         type === 'stake' ||
         type === 'masternode';
}

export const TransactionDetailsDialog: React.FC<TransactionDetailsDialogProps> = ({
  isOpen,
  onClose,
  transaction,
}) => {
  const [copiedField, setCopiedField] = useState<string | null>(null);
  const { formatAmount } = useDisplayUnits();

  // Handle escape key to close
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        onClose();
      }
    };

    if (isOpen) {
      document.addEventListener('keydown', handleKeyDown);
    }

    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [isOpen, onClose]);

  // Handle copy to clipboard
  const handleCopy = useCallback(async (text: string, field: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedField(field);
      setTimeout(() => setCopiedField(null), 2000);
    } catch {
      // Clipboard copy failed - UI won't show success state
    }
  }, []);

  // Handle view in explorer
  const handleViewInExplorer = useCallback(() => {
    if (transaction?.txid) {
      // Validate txid format (64 hex characters for SHA256 hash)
      const txidRegex = /^[a-fA-F0-9]{64}$/;
      if (!txidRegex.test(transaction.txid)) {
        // Invalid txid - prevent opening potentially malicious URL
        return;
      }
      // TODO: Use actual block explorer URL from config
      const explorerUrl = `https://explorer.win.win/tx/${encodeURIComponent(transaction.txid)}`;
      BrowserOpenURL(explorerUrl);
    }
  }, [transaction?.txid]);

  if (!isOpen || !transaction) return null;

  const typeIcon = getTransactionTypeIcon(transaction.type);
  const typeLabel = getTransactionTypeLabel(transaction.type);

  // Get transaction state flags
  const isConflicted = transaction.is_conflicted || false;
  const isCoinbase = transaction.is_coinbase || false;
  const isCoinstake = transaction.is_coinstake || false;
  const maturesIn = transaction.matures_in || 0;
  const isWatchOnly = transaction.is_watch_only || false;

  const statusText = getStatusText(
    transaction.confirmations || 0,
    isConflicted,
    isCoinbase,
    isCoinstake,
    maturesIn
  );
  const statusColorClass = getStatusColorClass(
    transaction.confirmations || 0,
    isConflicted,
    maturesIn
  );
  const formattedDate = formatTransactionDate(transaction.time);
  const formattedDateUTC = formatTransactionDateUTC(transaction.time);
  // send_to_self and consolidation net amount equals -(fee), which is technically correct but
  // not a loss — use neutral color instead of red to avoid confusion.
  const isSelfTransfer = transaction.type === 'send_to_self' || transaction.type === 'consolidation';
  const amountColorClass = isSelfTransfer
    ? 'text-gray-400'
    : getAmountColorClass(transaction.amount);

  // Calculate net amount (for display purposes)
  const netAmount = transaction.amount;
  const fee = transaction.fee || 0;

  // Determine transaction direction based on type
  const isReceive = isReceiveTransaction(transaction.type);
  const isSend = !isReceive;

  return (
    <>
      {/* Overlay */}
      <div
        className="fixed inset-0 bg-black/60 z-50"
        onClick={onClose}
      />

      {/* Modal */}
      <div className="fixed inset-0 flex items-center justify-center z-50 pointer-events-none">
        <div
          className="qt-frame pointer-events-auto"
          style={{
            width: '550px',
            maxWidth: '90vw',
            maxHeight: '90vh',
            overflow: 'auto',
            backgroundColor: '#2b2b2b',
            border: '1px solid #4a4a4a',
            borderRadius: '4px',
            boxShadow: '0 8px 32px rgba(0, 0, 0, 0.8)',
          }}
          onClick={(e) => e.stopPropagation()}
        >
          <div className="qt-vbox" style={{ padding: '20px', gap: '16px' }}>
            {/* Header */}
            <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
              <div className="qt-hbox" style={{ gap: '12px', alignItems: 'center' }}>
                {/* Transaction Type Icon with Confirmation Ring */}
                <ConfirmationRing
                  typeIcon={typeIcon}
                  confirmations={transaction.confirmations || 0}
                  isConflicted={isConflicted}
                  isCoinstake={isCoinstake}
                  maturesIn={maturesIn}
                  size={40}
                />
                <div className="qt-vbox" style={{ gap: '2px' }}>
                  <span className="qt-header-label" style={{ fontSize: '16px' }}>
                    Transaction Details
                  </span>
                  <span className="qt-label" style={{ fontSize: '12px', color: '#999' }}>
                    {typeLabel}
                  </span>
                </div>
              </div>
              <button
                type="button"
                onClick={onClose}
                className="qt-button-icon"
                style={{
                  padding: '4px',
                  backgroundColor: 'transparent',
                  border: 'none',
                  cursor: 'pointer',
                }}
              >
                <X size={20} style={{ color: '#999' }} />
              </button>
            </div>

            {/* Status Section */}
            <div
              className="qt-frame-secondary"
              style={{
                padding: '12px',
                backgroundColor: '#3a3a3a',
                border: '1px solid #4a4a4a',
                borderRadius: '2px',
              }}
            >
              <div className="qt-hbox" style={{ gap: '12px', alignItems: 'center' }}>
                {maturesIn > 0 ? (
                  <Clock size={20} className="text-orange-400" />
                ) : (transaction.confirmations || 0) >= 6 ? (
                  <Check size={20} className="text-green-500" />
                ) : (transaction.confirmations || 0) === 0 ? (
                  <Clock size={20} className="text-yellow-500" />
                ) : (
                  <Clock size={20} className="text-blue-400" />
                )}
                <div className="qt-vbox" style={{ gap: '2px', flex: 1 }}>
                  <span className={`qt-label ${statusColorClass}`} style={{ fontSize: '13px', fontWeight: 'bold' }}>
                    {statusText}
                  </span>
                  <span className="qt-label" style={{ fontSize: '11px', color: '#888' }}>
                    {transaction.confirmations || 0} confirmation{(transaction.confirmations || 0) !== 1 ? 's' : ''}
                  </span>
                </div>
              </div>
            </div>

            {/* Transaction Info Grid */}
            <div
              className="qt-frame-secondary"
              style={{
                padding: '12px',
                backgroundColor: '#3a3a3a',
                border: '1px solid #4a4a4a',
                borderRadius: '2px',
              }}
            >
              <div className="qt-vbox" style={{ gap: '10px' }}>
                {/* Date */}
                <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                  <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Date</span>
                  <span
                    className="qt-label"
                    style={{ fontSize: '12px', cursor: 'help' }}
                    title={`UTC: ${formattedDateUTC}`}
                  >
                    {formattedDate}
                  </span>
                </div>

                {/* Type */}
                <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                  <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Type</span>
                  <span className="qt-label" style={{ fontSize: '12px' }}>{typeLabel}</span>
                </div>

                {/* Source - for coinbase/coinstake/masternode transactions */}
                {(isCoinbase || isCoinstake) && (
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Source</span>
                    <span className="qt-label" style={{ fontSize: '12px' }}>
                      {isCoinbase ? 'Mined' : transaction.type === 'masternode' ? 'Masternode Reward' : 'Staking Reward'}
                    </span>
                  </div>
                )}

                {/* From Address - for receive transactions (sender if known) */}
                {isReceive && !isCoinbase && !isCoinstake && (
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>From</span>
                    <div className="qt-hbox" style={{ gap: '6px', alignItems: 'center' }}>
                      <span
                        className="qt-label"
                        style={{ fontSize: '11px', fontFamily: 'monospace', wordBreak: 'break-all', textAlign: 'right', maxWidth: '280px', color: transaction.from_address ? '#ddd' : '#888' }}
                        title={transaction.from_address ? sanitizeText(transaction.from_address) : 'Sender address unknown'}
                      >
                        {transaction.from_address ? sanitizeText(transaction.from_address) : 'unknown'}
                      </span>
                      {transaction.from_address && (
                        <button
                          type="button"
                          onClick={() => handleCopy(transaction.from_address || '', 'fromAddress')}
                          className="qt-button-icon"
                          style={{
                            padding: '3px',
                            backgroundColor: copiedField === 'fromAddress' ? '#4a4a4a' : 'transparent',
                            border: '1px solid #555',
                            borderRadius: '2px',
                            cursor: 'pointer',
                            flexShrink: 0,
                          }}
                          title="Copy sender address"
                        >
                          <Copy size={12} style={{ color: copiedField === 'fromAddress' ? '#00ff00' : '#ddd' }} />
                        </button>
                      )}
                    </div>
                  </div>
                )}

                {/* To Address - your receiving address (for receive) or recipient (for send) */}
                {transaction.address && (
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>
                      {transaction.type === 'consolidation'
                        ? 'Consolidated to'
                        : transaction.type === 'send_to_self'
                        ? 'To yourself'
                        : (isSend ? 'To' : (isReceive ? 'Received with' : 'Address'))}
                    </span>
                    <div className="qt-hbox" style={{ gap: '6px', alignItems: 'center' }}>
                      <span
                        className="qt-label"
                        style={{ fontSize: '11px', fontFamily: 'monospace', wordBreak: 'break-all', textAlign: 'right', maxWidth: '260px' }}
                        title={sanitizeText(transaction.address)}
                      >
                        {sanitizeText(transaction.address)}
                        {/* Show ownership indicator for receive transactions and watch-only addresses. */}
                        {(isReceive) && (
                          <span style={{ color: '#888', fontFamily: 'inherit' }}>
                            {' '}({isWatchOnly ? 'watch-only' : 'own address'})
                          </span>
                        )}
                      </span>
                      <button
                        type="button"
                        onClick={() => handleCopy(transaction.address, 'address')}
                        className="qt-button-icon"
                        style={{
                          padding: '3px',
                          backgroundColor: copiedField === 'address' ? '#4a4a4a' : 'transparent',
                          border: '1px solid #555',
                          borderRadius: '2px',
                          cursor: 'pointer',
                          flexShrink: 0,
                        }}
                        title="Copy address"
                      >
                        <Copy size={12} style={{ color: copiedField === 'address' ? '#00ff00' : '#ddd' }} />
                      </button>
                    </div>
                  </div>
                )}

                {/* Output Index (vout) */}
                {transaction.vout !== undefined && transaction.vout >= 0 && (
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Output Index</span>
                    <span className="qt-label" style={{ fontSize: '12px', fontFamily: 'monospace' }}>{transaction.vout}</span>
                  </div>
                )}

                {/* Label */}
                {transaction.label && (
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Label</span>
                    <span className="qt-label" style={{ fontSize: '12px' }}>{sanitizeText(transaction.label)}</span>
                  </div>
                )}
              </div>
            </div>

            {/* Amount Section */}
            <div
              className="qt-frame-secondary"
              style={{
                padding: '12px',
                backgroundColor: '#3a3a3a',
                border: '1px solid #4a4a4a',
                borderRadius: '2px',
              }}
            >
              <div className="qt-vbox" style={{ gap: '8px' }}>
                {/* Debit/Credit */}
                {/* For send_to_self/consolidation, debit equals the fee which is already shown in the
                    Transaction Fee row below — suppress the redundant debit entry. */}
                {transaction.debit !== undefined && transaction.debit !== 0 && !isSelfTransfer && (
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Debit</span>
                    <span className="qt-label text-red-400" style={{ fontSize: '12px', fontFamily: 'monospace' }}>
                      -{formatAmount(Math.abs(transaction.debit))}
                    </span>
                  </div>
                )}

                {transaction.credit !== undefined && transaction.credit !== 0 && (
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Credit</span>
                    <span className="qt-label text-green-400" style={{ fontSize: '12px', fontFamily: 'monospace' }}>
                      +{formatAmount(transaction.credit)}
                    </span>
                  </div>
                )}

                {/* Transaction Fee (for sent transactions) */}
                {isSend && fee > 0 && (
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Transaction Fee</span>
                    <span className="qt-label" style={{ fontSize: '12px', fontFamily: 'monospace', color: '#999' }}>
                      -{formatAmount(fee)}
                    </span>
                  </div>
                )}

                {/* Separator */}
                <div style={{ borderTop: '1px solid #4a4a4a', marginTop: '4px', paddingTop: '8px' }}>
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                    <span className="qt-label" style={{ fontSize: '13px', fontWeight: 'bold' }}>Net Amount</span>
                    <span className={`qt-label ${amountColorClass}`} style={{ fontSize: '14px', fontFamily: 'monospace', fontWeight: 'bold' }}>
                      {formatAmountWithSign(netAmount, formatAmount)}
                    </span>
                  </div>
                </div>
              </div>
            </div>

            {/* Comment/Message (if present) */}
            {transaction.comment && (
              <div
                className="qt-frame-secondary"
                style={{
                  padding: '12px',
                  backgroundColor: '#3a3a3a',
                  border: '1px solid #4a4a4a',
                  borderRadius: '2px',
                }}
              >
                <div className="qt-vbox" style={{ gap: '6px' }}>
                  <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Message</span>
                  <span className="qt-label" style={{ fontSize: '12px', whiteSpace: 'pre-wrap' }}>
                    {sanitizeText(transaction.comment)}
                  </span>
                </div>
              </div>
            )}

            {/* Coinbase/Coinstake Maturity Notice */}
            {(isCoinbase || isCoinstake) && maturesIn > 0 && (
              <div
                className="qt-frame-secondary"
                style={{
                  padding: '12px',
                  backgroundColor: '#3d3520',
                  border: '1px solid #5a4a2a',
                  borderRadius: '2px',
                }}
              >
                <div className="qt-hbox" style={{ gap: '10px', alignItems: 'flex-start' }}>
                  <Info size={18} style={{ color: '#f0a000', flexShrink: 0, marginTop: '2px' }} />
                  <div className="qt-vbox" style={{ gap: '4px' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#f0a000', fontWeight: 'bold' }}>
                      Maturity Notice
                    </span>
                    <span className="qt-label" style={{ fontSize: '11px', color: '#ccc', lineHeight: '1.4' }}>
                      {/* isCoinbase/isCoinstake gate uses transaction.is_coinstake (set for both
                          stake and masternode in go_client.go). The inner type check uses
                          transaction.type which comes from mapCategoryToType — both are kept in
                          sync by the same mapping; if a new coinstake-like category is added,
                          update mapCategoryToType and add a branch here accordingly. */}
                      {isCoinbase
                        ? 'Generated coins must mature 60 blocks before they can be spent. When you generated this block, it was broadcast to the network to be added to the block chain. If it fails to get into the chain, its state will change to "not accepted" and it won\'t be spendable. This may occasionally happen if another node generates a block within a few seconds of yours.'
                        : transaction.type === 'masternode'
                          ? 'Masternode rewards must mature 60 blocks before they can be spent. This transaction represents your masternode reward which is currently maturing.'
                          : 'Staking rewards must mature 60 blocks before they can be spent. This transaction represents your staking reward which is currently maturing.'}
                    </span>
                  </div>
                </div>
              </div>
            )}

            {/* Conflicted Transaction Warning */}
            {isConflicted && (
              <div
                className="qt-frame-secondary"
                style={{
                  padding: '12px',
                  backgroundColor: '#3d2020',
                  border: '1px solid #5a2a2a',
                  borderRadius: '2px',
                }}
              >
                <div className="qt-hbox" style={{ gap: '10px', alignItems: 'flex-start' }}>
                  <AlertTriangle size={18} style={{ color: '#ff4444', flexShrink: 0, marginTop: '2px' }} />
                  <div className="qt-vbox" style={{ gap: '4px' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#ff4444', fontWeight: 'bold' }}>
                      Transaction Conflicted
                    </span>
                    <span className="qt-label" style={{ fontSize: '11px', color: '#ccc', lineHeight: '1.4' }}>
                      This transaction conflicts with another transaction and will not be confirmed. The conflicting transaction may have been double-spent or replaced.
                    </span>
                  </div>
                </div>
              </div>
            )}

            {/* Transaction ID Section */}
            <div
              className="qt-frame-secondary"
              style={{
                padding: '12px',
                backgroundColor: '#3a3a3a',
                border: '1px solid #4a4a4a',
                borderRadius: '2px',
              }}
            >
              <div className="qt-vbox" style={{ gap: '8px' }}>
                <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Transaction ID</span>
                <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center' }}>
                  <span
                    className="qt-label"
                    style={{
                      fontSize: '11px',
                      fontFamily: 'monospace',
                      wordBreak: 'break-all',
                      flex: 1,
                    }}
                  >
                    {transaction.txid}
                  </span>
                  <button
                    type="button"
                    onClick={() => handleCopy(transaction.txid, 'txid')}
                    className="qt-button-icon"
                    style={{
                      padding: '4px',
                      backgroundColor: copiedField === 'txid' ? '#4a4a4a' : 'transparent',
                      border: '1px solid #555',
                      borderRadius: '2px',
                      cursor: 'pointer',
                      flexShrink: 0,
                    }}
                    title="Copy Transaction ID"
                  >
                    <Copy size={14} style={{ color: copiedField === 'txid' ? '#00ff00' : '#ddd' }} />
                  </button>
                  <button
                    type="button"
                    onClick={handleViewInExplorer}
                    className="qt-button-icon"
                    style={{
                      padding: '4px',
                      backgroundColor: 'transparent',
                      border: '1px solid #555',
                      borderRadius: '2px',
                      cursor: 'pointer',
                      flexShrink: 0,
                    }}
                    title="View in Explorer"
                  >
                    <ExternalLink size={14} style={{ color: '#ddd' }} />
                  </button>
                </div>
                {copiedField === 'txid' && (
                  <span className="qt-label" style={{ fontSize: '10px', color: '#00ff00' }}>
                    Copied to clipboard!
                  </span>
                )}
              </div>
            </div>

            {/* Block Info (if confirmed) */}
            {transaction.block_hash && (
              <div
                className="qt-frame-secondary"
                style={{
                  padding: '12px',
                  backgroundColor: '#3a3a3a',
                  border: '1px solid #4a4a4a',
                  borderRadius: '2px',
                }}
              >
                <div className="qt-vbox" style={{ gap: '8px' }}>
                  {transaction.block_height !== undefined && transaction.block_height > 0 && (
                    <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                      <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Block Height</span>
                      <span className="qt-label" style={{ fontSize: '12px', fontFamily: 'monospace' }}>
                        {transaction.block_height.toLocaleString()}
                      </span>
                    </div>
                  )}
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
                    <span className="qt-label" style={{ fontSize: '12px', color: '#888' }}>Block Hash</span>
                    <div className="qt-hbox" style={{ gap: '6px', alignItems: 'center' }}>
                      <span
                        className="qt-label"
                        style={{ fontSize: '10px', fontFamily: 'monospace', maxWidth: '200px' }}
                        title={transaction.block_hash}
                      >
                        {truncateTxId(transaction.block_hash, 8)}
                      </span>
                      <button
                        type="button"
                        onClick={() => handleCopy(transaction.block_hash || '', 'blockhash')}
                        className="qt-button-icon"
                        style={{
                          padding: '3px',
                          backgroundColor: copiedField === 'blockhash' ? '#4a4a4a' : 'transparent',
                          border: '1px solid #555',
                          borderRadius: '2px',
                          cursor: 'pointer',
                          flexShrink: 0,
                        }}
                        title="Copy Block Hash"
                      >
                        <Copy size={12} style={{ color: copiedField === 'blockhash' ? '#00ff00' : '#ddd' }} />
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            )}

            {/* Close Button */}
            <div className="qt-hbox" style={{ justifyContent: 'flex-end', marginTop: '8px' }}>
              <button
                type="button"
                onClick={onClose}
                className="qt-button"
                style={{
                  padding: '8px 24px',
                  fontSize: '13px',
                  backgroundColor: '#5a5a5a',
                  border: '1px solid #666',
                  borderRadius: '3px',
                  color: '#fff',
                  cursor: 'pointer',
                }}
              >
                Close
              </button>
            </div>
          </div>
        </div>
      </div>
    </>
  );
};
