import React, { useState, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { X, AlertTriangle, CheckCircle, Copy, ExternalLink, Coins, MapPin, Layers } from 'lucide-react';
import { BrowserOpenURL } from '@wailsjs/runtime/runtime';
import { sanitizeErrorMessage } from '@/shared/utils/sanitize';
import { truncateAddress } from '@/shared/utils/format';
import { PassphraseInput } from '@/shared/components/PassphraseInput';

export interface Recipient {
  address: string;
  amount: string;
  label?: string;
}

// SendError represents a structured error from the backend
export interface SendError {
  code: string;
  message: string;
  details?: string;
}

// Result from SendTransaction/SendTransactionWithOptions
export interface SendTransactionResult {
  txid?: string;
  error?: SendError;
}

export interface ConfirmationDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: (passphrase?: string) => Promise<SendTransactionResult>;
  onSuccess?: () => void; // Called when transaction succeeds, use to clear form
  recipients: Recipient[];
  fee: number;
  total: number;
  isWalletEncrypted: boolean;
  // When true, shows a hint that the wallet will return to staking mode after the transaction
  isWalletStakingOnly?: boolean;
  // Coin control information
  coinControlSelectedCount?: number;
  coinControlSelectedAmount?: number;
  // Custom change address from send form
  customChangeAddress?: string;
  // Split UTXO information
  splitEnabled?: boolean;
  splitCount?: number;
  splitOutputSize?: number; // Size per output in TWINS
}

type DialogState = 'confirming' | 'sending' | 'success' | 'error';

export const ConfirmationDialog: React.FC<ConfirmationDialogProps> = ({
  isOpen,
  onClose,
  onConfirm,
  onSuccess,
  recipients,
  fee,
  total,
  isWalletEncrypted,
  isWalletStakingOnly = false,
  coinControlSelectedCount,
  coinControlSelectedAmount,
  customChangeAddress,
  splitEnabled = false,
  splitCount,
  splitOutputSize,
}) => {
  // Determine if coin control is active
  const hasCoinControlSelection = coinControlSelectedCount !== undefined && coinControlSelectedCount > 0;
  const hasCustomChangeAddress = customChangeAddress !== undefined && customChangeAddress !== '';
  // Determine if split UTXO is active
  const hasSplitUTXO = splitEnabled && splitCount !== undefined && splitCount > 1;
  const { t } = useTranslation('wallet');
  const [dialogState, setDialogState] = useState<DialogState>('confirming');
  const [passphrase, setPassphrase] = useState('');
  const [errorMessage, setErrorMessage] = useState('');
  const [txid, setTxid] = useState('');
  const [copiedTxid, setCopiedTxid] = useState(false);
  const [copyError, setCopyError] = useState(false);

  const passphraseInputRef = useRef<HTMLInputElement>(null);
  const confirmButtonRef = useRef<HTMLButtonElement>(null);
  const isMountedRef = useRef(true);
  const wasOpenRef = useRef(false);

  // Setup component mount tracking for async operation cleanup
  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  // Reset state when dialog opens (only on transition from closed to open)
  useEffect(() => {
    // Only reset state when transitioning from closed to open
    // This prevents resetting when isWalletEncrypted changes while dialog is showing success
    const justOpened = isOpen && !wasOpenRef.current;
    wasOpenRef.current = isOpen;

    if (justOpened) {
      setDialogState('confirming');
      setPassphrase('');
      setErrorMessage('');
      setTxid('');
      setCopiedTxid(false);
      setCopyError(false);

      // Focus passphrase input if wallet is encrypted, otherwise focus confirm button
      setTimeout(() => {
        if (isWalletEncrypted && passphraseInputRef.current) {
          passphraseInputRef.current.focus();
        } else if (confirmButtonRef.current) {
          confirmButtonRef.current.focus();
        }
      }, 100);
    }
  }, [isOpen, isWalletEncrypted]);

  // Handle keyboard shortcuts (Escape to close, Enter to confirm)
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (!isOpen) return;

      if (e.key === 'Escape' && dialogState !== 'sending') {
        handleClose();
      }

      if (e.key === 'Enter' && dialogState === 'confirming') {
        if (!isWalletEncrypted || passphrase) {
          handleConfirmSend();
        }
      }
    };

    if (isOpen) {
      document.addEventListener('keydown', handleKeyDown);
    }

    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [isOpen, dialogState, passphrase, isWalletEncrypted]);

  const handleClose = () => {
    if (dialogState === 'sending') return; // Don't allow closing while sending
    onClose();
  };

  const handleConfirmSend = async () => {
    if (isWalletEncrypted && !passphrase) {
      setErrorMessage(t('send.confirmation.passphraseRequired'));
      return;
    }

    setDialogState('sending');
    setErrorMessage('');

    try {
      const result = await onConfirm(isWalletEncrypted ? passphrase : undefined);

      // Only update state if component is still mounted
      if (!isMountedRef.current) return;

      if (result.txid && !result.error) {
        setTxid(result.txid);
        setDialogState('success');
        // Notify parent to clear the form
        if (onSuccess) {
          onSuccess();
        }
      } else if (result.error) {
        // Use the user-friendly message from SendError
        // SECURITY: Sanitize error message to prevent XSS
        const sanitizedError = sanitizeErrorMessage(result.error.message);
        setErrorMessage(sanitizedError);
        setDialogState('error');
      } else {
        // Fallback for unexpected response format
        setErrorMessage(t('send.errors.unknown'));
        setDialogState('error');
      }
    } catch (error) {
      // Only update state if component is still mounted
      if (!isMountedRef.current) return;

      // SECURITY: Sanitize error message to prevent XSS
      const errorMsg = error instanceof Error ? error.message : 'Unknown error occurred';
      const sanitizedError = sanitizeErrorMessage(errorMsg);
      setErrorMessage(sanitizedError);
      setDialogState('error');
    } finally {
      // SECURITY: Always clear passphrase from memory after transaction attempt
      // This ensures passphrase doesn't persist in memory on error or exception paths
      if (passphrase) {
        setPassphrase('');
      }
    }
  };

  const handleCopyTxid = async () => {
    if (txid) {
      try {
        await navigator.clipboard.writeText(txid);
        setCopiedTxid(true);
        setCopyError(false);
        setTimeout(() => setCopiedTxid(false), 2000);
      } catch (err) {
        console.error('Failed to copy TXID:', err);
        setCopyError(true);
        setTimeout(() => setCopyError(false), 3000);
      }
    }
  };

  const handleViewInExplorer = () => {
    // TODO: Use actual block explorer URL from config
    const explorerUrl = `https://explorer.win.win/tx/${txid}`;
    BrowserOpenURL(explorerUrl);
  };

  const formatAmount = (amount: number | string): string => {
    const num = typeof amount === 'string' ? parseFloat(amount) : amount;
    return num.toFixed(8);
  };

  if (!isOpen) return null;

  return (
    <>
      {/* Overlay */}
      <div
        className="fixed inset-0 bg-black/60 z-50"
        onClick={handleClose}
      />

      {/* Modal */}
      <div className="fixed inset-0 flex items-center justify-center z-50 pointer-events-none">
        <div
          className="qt-frame pointer-events-auto"
          style={{
            width: '600px',
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
          {/* Success State */}
          {dialogState === 'success' && (
            <div className="qt-vbox" style={{ padding: '24px', gap: '16px' }}>
              {/* Header */}
              <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center' }}>
                  <CheckCircle size={24} style={{ color: '#00ff00' }} />
                  <span className="qt-header-label" style={{ fontSize: '16px' }}>
                    {t('send.success.title')}
                  </span>
                </div>
                <button
                  onClick={handleClose}
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

              {/* TXID Display */}
              <div className="qt-frame-secondary" style={{
                padding: '12px',
                backgroundColor: '#3a3a3a',
                border: '1px solid #4a4a4a',
                borderRadius: '2px',
              }}>
                <div className="qt-vbox" style={{ gap: '8px' }}>
                  <span className="qt-label" style={{ fontSize: '12px', color: '#999' }}>
                    {t('send.success.txid')}:
                  </span>
                  <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center' }}>
                    <span className="qt-label" style={{
                      fontSize: '11px',
                      fontFamily: 'monospace',
                      wordBreak: 'break-all',
                      flex: 1,
                    }}>
                      {txid}
                    </span>
                    <button
                      onClick={handleCopyTxid}
                      className="qt-button-icon"
                      style={{
                        padding: '4px',
                        backgroundColor: copiedTxid ? '#4a4a4a' : 'transparent',
                        border: '1px solid #555',
                        borderRadius: '2px',
                        cursor: 'pointer',
                      }}
                      title={t('send.success.copyTxid')}
                    >
                      <Copy size={16} style={{ color: copiedTxid ? '#00ff00' : '#ddd' }} />
                    </button>
                    <button
                      onClick={handleViewInExplorer}
                      className="qt-button-icon"
                      style={{
                        padding: '4px',
                        backgroundColor: 'transparent',
                        border: '1px solid #555',
                        borderRadius: '2px',
                        cursor: 'pointer',
                      }}
                      title={t('send.success.viewInExplorer')}
                    >
                      <ExternalLink size={16} style={{ color: '#ddd' }} />
                    </button>
                  </div>
                  {copiedTxid && (
                    <span className="qt-label" style={{ fontSize: '10px', color: '#00ff00' }}>
                      {t('send.success.copiedToClipboard')}
                    </span>
                  )}
                  {copyError && (
                    <span className="qt-label" style={{ fontSize: '10px', color: '#ff6666' }}>
                      {t('send.success.copyFailed')}
                    </span>
                  )}
                </div>
              </div>

              {/* Transaction Summary */}
              <div className="qt-frame-secondary" style={{
                padding: '12px',
                backgroundColor: '#3a3a3a',
                border: '1px solid #4a4a4a',
                borderRadius: '2px',
              }}>
                <div className="qt-vbox" style={{ gap: '6px' }}>
                  <div className="qt-hbox" style={{ justifyContent: 'space-between' }}>
                    <span className="qt-label" style={{ fontSize: '12px' }}>{t('send.confirmation.recipients')}:</span>
                    <span className="qt-label" style={{ fontSize: '12px' }}>{recipients.length}</span>
                  </div>
                  <div className="qt-hbox" style={{ justifyContent: 'space-between' }}>
                    <span className="qt-label" style={{ fontSize: '12px' }}>{t('send.confirmation.totalAmount')}:</span>
                    <span className="qt-label" style={{ fontSize: '12px', fontWeight: 'bold' }}>
                      {formatAmount(total)} {t('common:units.twins')}
                    </span>
                  </div>
                  <div className="qt-hbox" style={{ justifyContent: 'space-between' }}>
                    <span className="qt-label" style={{ fontSize: '12px' }}>{t('send.confirmation.fee')}:</span>
                    <span className="qt-label" style={{ fontSize: '12px' }}>
                      {formatAmount(fee)} {t('common:units.twins')}
                    </span>
                  </div>
                </div>
              </div>

              {/* Actions */}
              <div className="qt-hbox" style={{ gap: '8px', justifyContent: 'flex-end' }}>
                <button
                  onClick={handleClose}
                  className="qt-button-primary"
                  style={{
                    padding: '8px 20px',
                    fontSize: '13px',
                    backgroundColor: '#5a5a5a',
                    border: '1px solid #666',
                    borderRadius: '3px',
                    color: '#fff',
                    cursor: 'pointer',
                  }}
                >
                  {t('common:buttons.close')}
                </button>
              </div>
            </div>
          )}

          {/* Confirming/Sending/Error State */}
          {(dialogState === 'confirming' || dialogState === 'sending' || dialogState === 'error') && (
            <div className="qt-vbox" style={{ padding: '24px', gap: '16px' }}>
              {/* Header */}
              <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center' }}>
                  {total > 10000 && (
                    <AlertTriangle size={20} style={{ color: '#ffaa00' }} />
                  )}
                  <span className="qt-header-label" style={{ fontSize: '16px' }}>
                    {t('send.confirmation.title')}
                  </span>
                </div>
                <button
                  onClick={handleClose}
                  className="qt-button-icon"
                  disabled={dialogState === 'sending'}
                  style={{
                    padding: '4px',
                    backgroundColor: 'transparent',
                    border: 'none',
                    cursor: dialogState === 'sending' ? 'not-allowed' : 'pointer',
                    opacity: dialogState === 'sending' ? 0.5 : 1,
                  }}
                >
                  <X size={20} style={{ color: '#999' }} />
                </button>
              </div>

              {/* Error Message */}
              {dialogState === 'error' && errorMessage && (
                <div className="qt-frame-secondary" style={{
                  padding: '12px',
                  backgroundColor: '#4a2a2a',
                  border: '1px solid #ff6666',
                  borderRadius: '2px',
                }}>
                  <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center' }}>
                    <AlertTriangle size={18} style={{ color: '#ff6666' }} />
                    <span className="qt-label" style={{ fontSize: '12px', color: '#ff6666' }}>
                      {errorMessage}
                    </span>
                  </div>
                </div>
              )}

              {/* Coin Control Section - shown when manual selection or custom change address is active */}
              {(hasCoinControlSelection || hasCustomChangeAddress) && (
                <div className="qt-frame-secondary" style={{
                  padding: '12px',
                  backgroundColor: '#3a3a2a',
                  border: '1px solid #ffaa00',
                  borderRadius: '2px',
                }}>
                  <div className="qt-vbox" style={{ gap: '8px' }}>
                    <span className="qt-label" style={{ fontSize: '12px', fontWeight: 'bold', color: '#ffaa00' }}>
                      {hasCoinControlSelection ? 'Coin Control Active' : 'Custom Change Address'}
                    </span>

                    {hasCoinControlSelection && (
                      <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center' }}>
                        <Coins size={14} style={{ color: '#ffaa00' }} />
                        <span className="qt-label" style={{ fontSize: '11px' }}>
                          {coinControlSelectedCount} coin{coinControlSelectedCount !== 1 ? 's' : ''} manually selected
                          {coinControlSelectedAmount !== undefined && ` (${formatAmount(coinControlSelectedAmount)} ${t('common:units.twins')})`}
                        </span>
                      </div>
                    )}

                    {hasCustomChangeAddress && (
                      <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center' }}>
                        <MapPin size={14} style={{ color: '#00aaff' }} />
                        <span className="qt-label" style={{ fontSize: '11px' }}>
                          Custom change address:
                          <span style={{ fontFamily: 'monospace', marginLeft: '4px' }}>
                            {truncateAddress(customChangeAddress!)}
                          </span>
                        </span>
                      </div>
                    )}
                  </div>
                </div>
              )}

              {/* Split UTXO Section - shown when split is enabled */}
              {hasSplitUTXO && (
                <div className="qt-frame-secondary" style={{
                  padding: '12px',
                  backgroundColor: '#2a3a3a',
                  border: '1px solid #00aaff',
                  borderRadius: '2px',
                }}>
                  <div className="qt-vbox" style={{ gap: '8px' }}>
                    <span className="qt-label" style={{ fontSize: '12px', fontWeight: 'bold', color: '#00aaff' }}>
                      Split UTXO Active
                    </span>
                    <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center' }}>
                      <Layers size={14} style={{ color: '#00aaff' }} />
                      <span className="qt-label" style={{ fontSize: '11px' }}>
                        {splitCount} outputs @ {formatAmount(splitOutputSize || 0)} {t('common:units.twins')} each
                      </span>
                    </div>
                  </div>
                </div>
              )}

              {/* Recipients Section */}
              <div className="qt-frame-secondary" style={{
                padding: '12px',
                backgroundColor: '#3a3a3a',
                border: '1px solid #4a4a4a',
                borderRadius: '2px',
              }}>
                <div className="qt-vbox" style={{ gap: '12px' }}>
                  <span className="qt-label" style={{ fontSize: '12px', fontWeight: 'bold' }}>
                    {t('send.confirmation.recipients')} {recipients.length > 1 && `(${recipients.length})`}
                  </span>
                  {recipients.map((recipient, index) => (
                    <div key={index} className="qt-vbox" style={{ gap: '4px' }}>
                      {recipient.label && (
                        <span className="qt-label" style={{ fontSize: '11px', color: '#999' }}>
                          {recipient.label}
                        </span>
                      )}
                      <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                        <span
                          className="qt-label"
                          style={{ fontSize: '11px', fontFamily: 'monospace' }}
                          title={recipient.address}
                        >
                          {truncateAddress(recipient.address)}
                        </span>
                        <span className="qt-label" style={{ fontSize: '12px', fontWeight: 'bold' }}>
                          {formatAmount(recipient.amount)} {t('common:units.twins')}
                        </span>
                      </div>
                      {index < recipients.length - 1 && (
                        <div style={{ borderTop: '1px solid #4a4a4a', marginTop: '8px' }} />
                      )}
                    </div>
                  ))}
                </div>
              </div>

              {/* Fee and Total Section */}
              <div className="qt-frame-secondary" style={{
                padding: '12px',
                backgroundColor: '#3a3a3a',
                border: '1px solid #4a4a4a',
                borderRadius: '2px',
              }}>
                <div className="qt-vbox" style={{ gap: '8px' }}>
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                    <span className="qt-label" style={{ fontSize: '12px' }}>{t('send.confirmation.transactionFee')}:</span>
                    <span className="qt-label" style={{ fontSize: '12px' }}>
                      {formatAmount(fee)} {t('common:units.twins')}
                    </span>
                  </div>
                  <div style={{ borderTop: '1px solid #4a4a4a' }} />
                  <div className="qt-hbox" style={{ justifyContent: 'space-between', alignItems: 'center' }}>
                    <span className="qt-label" style={{ fontSize: '13px', fontWeight: 'bold' }}>
                      {t('send.confirmation.grandTotal')}:
                    </span>
                    <span className="qt-label" style={{
                      fontSize: '14px',
                      fontWeight: 'bold',
                      color: '#00ff00',
                    }}>
                      {formatAmount(total + fee)} {t('common:units.twins')}
                    </span>
                  </div>
                </div>
              </div>

              {/* Passphrase Section (if wallet is encrypted) */}
              {isWalletEncrypted && (
                <div className="qt-frame-secondary" style={{
                  padding: '12px',
                  backgroundColor: '#3a3a3a',
                  border: '1px solid #4a4a4a',
                  borderRadius: '2px',
                }}>
                  <div className="qt-vbox" style={{ gap: '8px' }}>
                    <span className="qt-label" style={{ fontSize: '12px', fontWeight: 'bold' }}>
                      {t('send.confirmation.walletPassphrase')}
                    </span>
                    <PassphraseInput
                      ref={passphraseInputRef}
                      value={passphrase}
                      onChange={setPassphrase}
                      disabled={dialogState === 'sending'}
                      placeholder={t('send.confirmation.passphrasePlaceholder')}
                    />
                    {isWalletStakingOnly && (
                      <span className="qt-label" style={{ fontSize: '11px', color: '#888' }}>
                        {t('send.confirmation.stakingOnlyHint')}
                      </span>
                    )}
                  </div>
                </div>
              )}

              {/* Warning for large transactions */}
              {total > 10000 && (
                <div className="qt-frame-secondary" style={{
                  padding: '10px',
                  backgroundColor: '#4a3a2a',
                  border: '1px solid #ffaa00',
                  borderRadius: '2px',
                }}>
                  <div className="qt-hbox" style={{ gap: '8px', alignItems: 'center' }}>
                    <AlertTriangle size={16} style={{ color: '#ffaa00' }} />
                    <span className="qt-label" style={{ fontSize: '11px', color: '#ffaa00' }}>
                      {t('send.confirmation.largeTransactionWarning')}
                    </span>
                  </div>
                </div>
              )}

              {/* Action Buttons */}
              <div className="qt-hbox" style={{ gap: '8px', justifyContent: 'flex-end', marginTop: '8px' }}>
                <button
                  onClick={handleClose}
                  disabled={dialogState === 'sending'}
                  className="qt-button"
                  style={{
                    padding: '8px 20px',
                    fontSize: '13px',
                    backgroundColor: '#404040',
                    border: '1px solid #555',
                    borderRadius: '3px',
                    color: '#ddd',
                    cursor: dialogState === 'sending' ? 'not-allowed' : 'pointer',
                    opacity: dialogState === 'sending' ? 0.5 : 1,
                  }}
                >
                  {t('common:buttons.cancel')}
                </button>
                <button
                  ref={confirmButtonRef}
                  onClick={handleConfirmSend}
                  disabled={dialogState === 'sending' || (isWalletEncrypted && !passphrase)}
                  className="qt-button-primary"
                  style={{
                    padding: '8px 20px',
                    fontSize: '13px',
                    backgroundColor: dialogState === 'sending' || (isWalletEncrypted && !passphrase)
                      ? '#3a3a3a'
                      : '#5a5a5a',
                    border: '1px solid #666',
                    borderRadius: '3px',
                    color: '#fff',
                    cursor: dialogState === 'sending' || (isWalletEncrypted && !passphrase)
                      ? 'not-allowed'
                      : 'pointer',
                    opacity: dialogState === 'sending' || (isWalletEncrypted && !passphrase) ? 0.5 : 1,
                  }}
                >
                  {dialogState === 'sending' ? t('send.confirmation.sending') : t('send.confirmation.confirmSend')}
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </>
  );
};
