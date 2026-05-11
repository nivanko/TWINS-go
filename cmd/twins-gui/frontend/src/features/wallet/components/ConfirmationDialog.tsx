import React, { useState, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { X, AlertTriangle, CheckCircle, Copy, ExternalLink } from 'lucide-react';
import { BrowserOpenURL } from '@wailsjs/runtime/runtime';
import { sanitizeErrorMessage } from '@/shared/utils/sanitize';
import { truncateAddress } from '@/shared/utils/format';
import { PassphraseInput } from '@/shared/components/PassphraseInput';
import { Banner } from '@/shared/components/Banner';
import { IconButton } from '@/shared/components/IconButton';

export interface Recipient {
  address: string;
  amount: string;
  label?: string;
}

export interface SendError {
  code: string;
  message: string;
  details?: string;
}

export interface SendTransactionResult {
  txid?: string;
  error?: SendError;
}

export interface ConfirmationDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: (passphrase?: string) => Promise<SendTransactionResult>;
  onSuccess?: () => void;
  recipients: Recipient[];
  fee: number;
  total: number;
  isWalletEncrypted: boolean;
  isWalletStakingOnly?: boolean;
  coinControlSelectedCount?: number;
  coinControlSelectedAmount?: number;
  customChangeAddress?: string;
  splitEnabled?: boolean;
  splitCount?: number;
  splitOutputSize?: number;
}

type DialogState = 'confirming' | 'sending' | 'success' | 'error';

const innerCardStyle: React.CSSProperties = {
  backgroundColor: '#2a2a2a',
  border: '1px solid #3a3a3a',
  borderRadius: '6px',
  padding: '12px 16px',
};
const labelStyle: React.CSSProperties = { fontSize: '11px', color: '#888' };
const valueStyle: React.CSSProperties = { fontSize: '12px', color: '#ddd' };
const sectionTitleStyle: React.CSSProperties = { fontSize: '13px', fontWeight: 600, color: '#ccc' };
const rowStyle: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'center',
};
const monoAddressStyle: React.CSSProperties = {
  fontSize: '12px',
  fontFamily: 'monospace',
  color: '#e0e0e0',
  letterSpacing: '0.3px',
};

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
  const hasCoinControlSelection =
    coinControlSelectedCount !== undefined && coinControlSelectedCount > 0;
  const hasCustomChangeAddress = customChangeAddress !== undefined && customChangeAddress !== '';
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

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    const justOpened = isOpen && !wasOpenRef.current;
    wasOpenRef.current = isOpen;

    if (justOpened) {
      setDialogState('confirming');
      setPassphrase('');
      setErrorMessage('');
      setTxid('');
      setCopiedTxid(false);
      setCopyError(false);

      setTimeout(() => {
        if (isWalletEncrypted && passphraseInputRef.current) {
          passphraseInputRef.current.focus();
        } else if (confirmButtonRef.current) {
          confirmButtonRef.current.focus();
        }
      }, 100);
    }
  }, [isOpen, isWalletEncrypted]);

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
    if (dialogState === 'sending') return;
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

      if (!isMountedRef.current) return;

      if (result.txid && !result.error) {
        setTxid(result.txid);
        setDialogState('success');
        if (onSuccess) {
          onSuccess();
        }
      } else if (result.error) {
        const sanitizedError = sanitizeErrorMessage(result.error.message);
        setErrorMessage(sanitizedError);
        setDialogState('error');
      } else {
        setErrorMessage(t('send.errors.unknown'));
        setDialogState('error');
      }
    } catch (error) {
      if (!isMountedRef.current) return;

      const errorMsg = error instanceof Error ? error.message : 'Unknown error occurred';
      const sanitizedError = sanitizeErrorMessage(errorMsg);
      setErrorMessage(sanitizedError);
      setDialogState('error');
    } finally {
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
    const explorerUrl = `https://explorer.win.win/tx/${txid}`;
    BrowserOpenURL(explorerUrl);
  };

  const formatAmount = (amount: number | string): string => {
    const num = typeof amount === 'string' ? parseFloat(amount) : amount;
    return num.toFixed(8);
  };

  if (!isOpen) return null;

  // Compose Coin Control / custom-change-address Banner message
  const ccParts: string[] = [];
  if (hasCoinControlSelection) {
    ccParts.push(
      `${coinControlSelectedCount} coin${coinControlSelectedCount !== 1 ? 's' : ''} manually selected${
        coinControlSelectedAmount !== undefined
          ? ` (${formatAmount(coinControlSelectedAmount)} ${t('common:units.twins')})`
          : ''
      }`,
    );
  }
  if (hasCustomChangeAddress) {
    ccParts.push(`Custom change address: ${truncateAddress(customChangeAddress!)}`);
  }
  const composedCoinControlMessage = ccParts.join(' — ');

  const splitMessage = hasSplitUTXO
    ? `Split UTXO Active — ${splitCount} outputs @ ${formatAmount(splitOutputSize || 0)} ${t('common:units.twins')} each`
    : '';

  const isConfirmDisabled = dialogState === 'sending' || (isWalletEncrypted && !passphrase);

  return (
    <>
      <div className="fixed inset-0 bg-black/60 z-50" onClick={handleClose} />

      <div className="fixed inset-0 flex items-center justify-center z-50 pointer-events-none">
        <div
          className="pointer-events-auto"
          style={{
            width: '600px',
            maxWidth: '90vw',
            maxHeight: '90vh',
            overflow: 'auto',
            backgroundColor: '#2f2f2f',
            border: '1px solid #3a3a3a',
            borderRadius: '8px',
            boxShadow: '0 8px 32px rgba(0, 0, 0, 0.8)',
          }}
          onClick={(e) => e.stopPropagation()}
        >
          {/* Success State */}
          {dialogState === 'success' && (
            <div style={{ display: 'flex', flexDirection: 'column', padding: '24px', gap: '16px' }}>
              <div style={rowStyle}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                  <CheckCircle size={20} style={{ color: '#27ae60' }} />
                  <span style={sectionTitleStyle}>{t('send.success.title')}</span>
                </div>
                <IconButton
                  icon={<X size={14} />}
                  title={t('common:buttons.close')}
                  ariaLabel={t('common:buttons.close')}
                  onClick={handleClose}
                />
              </div>

              <div style={innerCardStyle}>
                <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
                  <span style={labelStyle}>{t('send.success.txid')}:</span>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                    <span style={{ ...monoAddressStyle, flex: 1 }} title={txid}>
                      {truncateAddress(txid, 12, 10)}
                    </span>
                    <IconButton
                      icon={<Copy size={12} />}
                      title={t('send.success.copyTxid')}
                      ariaLabel={t('send.success.copyTxid')}
                      onClick={handleCopyTxid}
                    />
                    <IconButton
                      icon={<ExternalLink size={12} />}
                      title={t('send.success.viewInExplorer')}
                      ariaLabel={t('send.success.viewInExplorer')}
                      onClick={handleViewInExplorer}
                    />
                  </div>
                  {copiedTxid && (
                    <span style={{ fontSize: '10px', color: '#27ae60' }}>
                      {t('send.success.copiedToClipboard')}
                    </span>
                  )}
                  {copyError && (
                    <span style={{ fontSize: '10px', color: '#ff6666' }}>
                      {t('send.success.copyFailed')}
                    </span>
                  )}
                </div>
              </div>

              <div style={innerCardStyle}>
                <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
                  <div style={rowStyle}>
                    <span style={labelStyle}>{t('send.confirmation.recipients')}:</span>
                    <span style={valueStyle}>{recipients.length}</span>
                  </div>
                  <div style={rowStyle}>
                    <span style={labelStyle}>{t('send.confirmation.totalAmount')}:</span>
                    <span style={valueStyle}>
                      {formatAmount(total)} {t('common:units.twins')}
                    </span>
                  </div>
                  <div style={rowStyle}>
                    <span style={labelStyle}>{t('send.confirmation.fee')}:</span>
                    <span style={valueStyle}>
                      {formatAmount(fee)} {t('common:units.twins')}
                    </span>
                  </div>
                </div>
              </div>

              <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
                <button
                  onClick={handleClose}
                  style={{
                    padding: '8px 16px',
                    fontSize: '12px',
                    fontWeight: 500,
                    backgroundColor: '#4a7c59',
                    border: '1px solid #5a8c69',
                    borderRadius: '6px',
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
          {(dialogState === 'confirming' ||
            dialogState === 'sending' ||
            dialogState === 'error') && (
            <div style={{ display: 'flex', flexDirection: 'column', padding: '24px', gap: '16px' }}>
              <div style={rowStyle}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                  {total > 10000 && <AlertTriangle size={20} style={{ color: '#ff9966' }} />}
                  <span style={sectionTitleStyle}>{t('send.confirmation.title')}</span>
                </div>
                <IconButton
                  icon={<X size={14} />}
                  title={t('common:buttons.close')}
                  ariaLabel={t('common:buttons.close')}
                  onClick={handleClose}
                  disabled={dialogState === 'sending'}
                />
              </div>

              {dialogState === 'error' && errorMessage && (
                <Banner variant="error" message={errorMessage} />
              )}

              {(hasCoinControlSelection || hasCustomChangeAddress) && (
                <Banner variant="warning" message={composedCoinControlMessage} />
              )}

              {hasSplitUTXO && <Banner variant="info" message={splitMessage} />}

              <div style={innerCardStyle}>
                <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
                  <span style={sectionTitleStyle}>
                    {t('send.confirmation.recipients')}{' '}
                    {recipients.length > 1 && `(${recipients.length})`}
                  </span>
                  {recipients.map((recipient, index) => (
                    <div
                      key={index}
                      style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}
                    >
                      {recipient.label && <span style={labelStyle}>{recipient.label}</span>}
                      <div style={rowStyle}>
                        <span style={monoAddressStyle} title={recipient.address}>
                          {truncateAddress(recipient.address)}
                        </span>
                        <span style={{ fontSize: '12px', fontWeight: 600, color: '#ddd' }}>
                          {formatAmount(recipient.amount)} {t('common:units.twins')}
                        </span>
                      </div>
                      {index < recipients.length - 1 && (
                        <div style={{ borderTop: '1px solid #3a3a3a', marginTop: '8px' }} />
                      )}
                    </div>
                  ))}
                </div>
              </div>

              <div style={innerCardStyle}>
                <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
                  <div style={rowStyle}>
                    <span style={labelStyle}>{t('send.confirmation.transactionFee')}:</span>
                    <span style={valueStyle}>
                      {formatAmount(fee)} {t('common:units.twins')}
                    </span>
                  </div>
                  <div style={{ borderTop: '1px solid #3a3a3a' }} />
                  <div style={rowStyle}>
                    <span style={{ fontSize: '13px', fontWeight: 600, color: '#ddd' }}>
                      {t('send.confirmation.grandTotal')}:
                    </span>
                    <span style={{ fontSize: '13px', fontWeight: 600, color: '#27ae60' }}>
                      {formatAmount(total + fee)} {t('common:units.twins')}
                    </span>
                  </div>
                </div>
              </div>

              {isWalletEncrypted && (
                <div style={innerCardStyle}>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
                    <span style={sectionTitleStyle}>
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
                      <span style={{ fontSize: '11px', color: '#888' }}>
                        {t('send.confirmation.stakingOnlyHint')}
                      </span>
                    )}
                  </div>
                </div>
              )}

              {total > 10000 && (
                <Banner variant="warning" message={t('send.confirmation.largeTransactionWarning')} />
              )}

              <div
                style={{
                  display: 'flex',
                  gap: '8px',
                  justifyContent: 'flex-end',
                  marginTop: '8px',
                }}
              >
                <button
                  onClick={handleClose}
                  disabled={dialogState === 'sending'}
                  style={{
                    padding: '8px 16px',
                    fontSize: '12px',
                    backgroundColor: '#383838',
                    border: '1px solid #4a4a4a',
                    borderRadius: '6px',
                    color: '#ccc',
                    cursor: dialogState === 'sending' ? 'not-allowed' : 'pointer',
                    opacity: dialogState === 'sending' ? 0.5 : 1,
                  }}
                >
                  {t('common:buttons.cancel')}
                </button>
                <button
                  ref={confirmButtonRef}
                  onClick={handleConfirmSend}
                  disabled={isConfirmDisabled}
                  style={{
                    padding: '8px 16px',
                    fontSize: '12px',
                    fontWeight: 500,
                    backgroundColor: isConfirmDisabled ? '#3a3a3a' : '#4a7c59',
                    border: isConfirmDisabled ? '1px solid #444' : '1px solid #5a8c69',
                    borderRadius: '6px',
                    color: isConfirmDisabled ? '#888' : '#fff',
                    cursor: isConfirmDisabled ? 'not-allowed' : 'pointer',
                    opacity: isConfirmDisabled ? 0.7 : 1,
                  }}
                >
                  {dialogState === 'sending'
                    ? t('send.confirmation.sending')
                    : t('send.confirmation.confirmSend')}
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </>
  );
};
