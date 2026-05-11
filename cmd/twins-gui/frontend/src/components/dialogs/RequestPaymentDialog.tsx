import React, { useEffect, useCallback, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { QRCodeCanvas } from 'qrcode.react';
import { X, Copy, Download } from 'lucide-react';
import { useReceive } from '@/store/useStore';
import { sanitizeText } from '@/shared/utils/sanitize';
import { useDisplayUnits } from '@/shared/hooks/useDisplayUnits';
import { buildTwinsURI, MAX_QR_DATA_LENGTH } from '@/shared/utils/twinsUri';
import { writeToClipboard } from '@/shared/utils/clipboard';
import { truncateAddress } from '@/shared/utils/format';
import { SaveQRImage } from '@wailsjs/go/main/App';
import { createCircularLogoDataURL } from '@/shared/utils/qrLogo';
import { buildQRFilename } from '@/shared/utils/qrFilename';
import { IconButton } from '@/shared/components/IconButton';
import { PillButton } from '@/shared/components/PillButton';

export const RequestPaymentDialog: React.FC = () => {
  const { t } = useTranslation('wallet');
  const { formatAmount } = useDisplayUnits();
  const {
    isRequestDialogOpen,
    selectedRequest,
    closeRequestDialog,
  } = useReceive();

  const [copyFeedback, setCopyFeedback] = useState<string | null>(null);
  const qrRef = useRef<HTMLDivElement>(null);
  const [qrLogoSrc, setQrLogoSrc] = useState<string | undefined>();

  // Generate circular-bordered logo for QR code
  useEffect(() => {
    createCircularLogoDataURL('/icons/twins-logo.png', 64, 4, '#27ae60')
      .then(setQrLogoSrc)
      .catch(() => {});
  }, []);

  // Auto-clear copy feedback
  useEffect(() => {
    if (!copyFeedback) return;
    const timeoutId = setTimeout(() => setCopyFeedback(null), 2000);
    return () => clearTimeout(timeoutId);
  }, [copyFeedback]);

  // Handle keyboard events
  useEffect(() => {
    if (!isRequestDialogOpen) return;
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        closeRequestDialog();
      }
    };
    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [isRequestDialogOpen, closeRequestDialog]);

  // Build URI from selected request
  const uri = selectedRequest
    ? buildTwinsURI(
        selectedRequest.address,
        selectedRequest.amount,
        selectedRequest.label,
        selectedRequest.message
      )
    : '';

  const isURITooLong = uri.length > MAX_QR_DATA_LENGTH;

  // Copy URI to clipboard
  const handleCopyURI = useCallback(async () => {
    if (!uri) return;
    const ok = await writeToClipboard(uri);
    setCopyFeedback(ok ? t('receive.requestDialog.uriCopied') : t('receive.requestDialog.copyFailed'));
  }, [uri, t]);

  // Copy address to clipboard
  const handleCopyAddress = useCallback(async () => {
    if (!selectedRequest?.address) return;
    const ok = await writeToClipboard(selectedRequest.address);
    setCopyFeedback(ok ? t('receive.requestDialog.addressCopied') : t('receive.requestDialog.copyFailed'));
  }, [selectedRequest?.address, t]);

  // Copy amount (numeric value) to clipboard
  const handleCopyAmount = useCallback(async () => {
    if (!selectedRequest?.amount) return;
    const ok = await writeToClipboard(String(selectedRequest.amount));
    setCopyFeedback(ok ? t('receive.requestDialog.amountCopied') : t('receive.requestDialog.copyFailed'));
  }, [selectedRequest?.amount, t]);

  // Copy label to clipboard
  const handleCopyLabel = useCallback(async () => {
    if (!selectedRequest?.label) return;
    const ok = await writeToClipboard(selectedRequest.label);
    setCopyFeedback(ok ? t('receive.requestDialog.labelCopied') : t('receive.requestDialog.copyFailed'));
  }, [selectedRequest?.label, t]);

  // Copy message to clipboard
  const handleCopyMessage = useCallback(async () => {
    if (!selectedRequest?.message) return;
    const ok = await writeToClipboard(selectedRequest.message);
    setCopyFeedback(ok ? t('receive.requestDialog.messageCopied') : t('receive.requestDialog.copyFailed'));
  }, [selectedRequest?.message, t]);

  // Save QR code as PNG image via native save dialog
  const handleSaveQR = useCallback(async () => {
    if (!qrRef.current) return;
    try {
      const canvas = qrRef.current.querySelector('canvas');
      if (!canvas) throw new Error('canvas not found');
      const pngBase64 = canvas.toDataURL('image/png');
      const defaultFilename = buildQRFilename(
        selectedRequest?.address,
        selectedRequest?.label,
        selectedRequest?.amount,
      );
      const saved = await SaveQRImage(pngBase64, defaultFilename);
      if (saved) {
        setCopyFeedback(t('receive.requestDialog.imageSaved'));
      }
      // If saved === false the user cancelled the dialog — show no feedback
    } catch {
      setCopyFeedback(t('receive.requestDialog.imageSaveFailed'));
    }
  }, [selectedRequest?.address, selectedRequest?.label, selectedRequest?.amount, t]);

  if (!isRequestDialogOpen || !selectedRequest) return null;

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        backgroundColor: 'rgba(0, 0, 0, 0.5)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 50,
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) {
          closeRequestDialog();
        }
      }}
      role="presentation"
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="request-payment-title"
        style={{
          width: '460px',
          maxHeight: '90vh',
          backgroundColor: '#2f2f2f',
          border: '1px solid #3a3a3a',
          borderRadius: '8px',
          boxShadow: '0 8px 24px rgba(0, 0, 0, 0.5)',
          display: 'flex',
          flexDirection: 'column',
          overflow: 'hidden',
        }}
      >
        {/* Header */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '14px 18px',
            borderBottom: '1px solid #3a3a3a',
            flexShrink: 0,
          }}
        >
          <h2
            id="request-payment-title"
            style={{ fontSize: '14px', fontWeight: 600, color: '#ddd', margin: 0 }}
          >
            {t('receive.requestDialog.title')}
          </h2>
          <button
            onClick={closeRequestDialog}
            style={{
              background: 'none',
              border: 'none',
              color: '#888',
              cursor: 'pointer',
              padding: '2px',
              display: 'flex',
              alignItems: 'center',
              transition: 'color 0.15s',
            }}
            onMouseEnter={(e) => { e.currentTarget.style.color = '#ddd'; }}
            onMouseLeave={(e) => { e.currentTarget.style.color = '#888'; }}
            aria-label="Close dialog"
          >
            <X size={18} />
          </button>
        </div>

        {/* Body */}
        <div
          style={{
            padding: '20px',
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            gap: '14px',
            overflowY: 'auto',
          }}
        >
          {/* QR Code (clickable to save; pill button below is the discoverable affordance) */}
          <div
            ref={qrRef}
            onClick={handleSaveQR}
            title={t('receive.requestDialog.clickQrToSave')}
            style={{
              padding: '12px',
              backgroundColor: '#ffffff',
              borderRadius: '8px',
              lineHeight: 0,
              cursor: 'pointer',
            }}
          >
            <QRCodeCanvas
              value={uri}
              size={200}
              level="H"
              includeMargin={false}
              bgColor="#ffffff"
              fgColor="#000000"
              imageSettings={qrLogoSrc ? {
                src: qrLogoSrc,
                height: 76,
                width: 76,
                excavate: true,
              } : undefined}
            />
          </div>

          {/* Save image pill button — always visible for discoverability */}
          <div style={{ marginTop: '-4px' }}>
            <PillButton
              onClick={handleSaveQR}
              title={t('receive.requestDialog.saveImage')}
              ariaLabel={t('receive.requestDialog.saveImage')}
              icon={<Download size={12} />}
              label={t('receive.requestDialog.saveImage')}
            />
          </div>

          {/* URI too long warning */}
          {isURITooLong && (
            <div
              style={{
                padding: '6px 12px',
                backgroundColor: '#4a3a2a',
                border: '1px solid #ff9966',
                borderRadius: '4px',
                color: '#ff9966',
                fontSize: '11px',
                textAlign: 'center',
              }}
            >
              {t('receive.requestDialog.uriTooLong')}
            </div>
          )}

          {/* Truncated address (always visible, monospace) with inline copy button */}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              padding: '8px 12px',
              backgroundColor: '#252525',
              border: '1px solid #3a3a3a',
              borderRadius: '6px',
              width: '100%',
            }}
          >
            <span
              title={selectedRequest.address}
              style={{
                flex: 1,
                fontFamily: 'monospace',
                fontSize: '13px',
                color: '#e0e0e0',
                letterSpacing: '0.3px',
                textAlign: 'center',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {truncateAddress(selectedRequest.address, 12, 10)}
            </span>
            <IconButton
              onClick={handleCopyAddress}
              title={t('receive.requestDialog.copyAddress')}
              ariaLabel={t('receive.requestDialog.copyAddress')}
              icon={<Copy size={12} />}
            />
          </div>

          {/* Payment information card */}
          <div
            style={{
              width: '100%',
              backgroundColor: '#252525',
              border: '1px solid #3a3a3a',
              borderRadius: '6px',
              padding: '12px 14px',
              display: 'flex',
              flexDirection: 'column',
              gap: '8px',
            }}
          >
            {/* Amount (always shown when > 0) */}
            {selectedRequest.amount > 0 && (
              <InfoRow
                label={t('receive.requestDialog.amount')}
                onCopy={handleCopyAmount}
                copyTitle={t('receive.requestDialog.copyAmount')}
                copyAriaLabel={t('receive.requestDialog.copyAmount')}
              >
                <span style={{ color: '#4a7c59', fontWeight: 500 }}>
                  {formatAmount(selectedRequest.amount, true)}
                </span>
              </InfoRow>
            )}

            {/* Label (conditional) */}
            {selectedRequest.label && (
              <InfoRow
                label={t('receive.requestDialog.label')}
                onCopy={handleCopyLabel}
                copyTitle={t('receive.requestDialog.copyLabel')}
                copyAriaLabel={t('receive.requestDialog.copyLabel')}
              >
                <span style={{ color: '#ddd' }}>{sanitizeText(selectedRequest.label)}</span>
              </InfoRow>
            )}

            {/* Message (conditional) */}
            {selectedRequest.message && (
              <InfoRow
                label={t('receive.requestDialog.message')}
                onCopy={handleCopyMessage}
                copyTitle={t('receive.requestDialog.copyMessage')}
                copyAriaLabel={t('receive.requestDialog.copyMessage')}
              >
                <span style={{ color: '#ddd' }}>{sanitizeText(selectedRequest.message)}</span>
              </InfoRow>
            )}

            {/* Divider before URI (only if there are fields above) */}
            {(selectedRequest.amount > 0 || selectedRequest.label || selectedRequest.message) && (
              <div style={{ height: '1px', backgroundColor: '#3a3a3a', margin: '2px 0' }} />
            )}

            {/* URI row with inline copy button */}
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              <span style={{ fontSize: '11px', color: '#888', minWidth: '60px', flexShrink: 0 }}>
                {t('receive.requestDialog.uri')}
              </span>
              <span
                title={uri}
                style={{
                  flex: 1,
                  fontSize: '11px',
                  color: '#6699cc',
                  fontFamily: 'monospace',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  minWidth: 0,
                }}
              >
                {uri}
              </span>
              <IconButton
                onClick={handleCopyURI}
                title={t('receive.requestDialog.copyUriTooltip')}
                ariaLabel={t('receive.requestDialog.copyUri')}
                icon={<Copy size={12} />}
              />
            </div>
          </div>
        </div>

        {/* Copy feedback toast */}
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
      </div>
    </div>
  );
};

// Reusable info row for the payment information card.
// Optionally renders a copy `IconButton` on the right when `onCopy` is provided.
const InfoRow: React.FC<{
  label: string;
  children: React.ReactNode;
  onCopy?: () => void;
  copyTitle?: string;
  copyAriaLabel?: string;
}> = ({ label, children, onCopy, copyTitle, copyAriaLabel }) => (
  <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontSize: '12px' }}>
    <span style={{ color: '#888', minWidth: '60px', flexShrink: 0 }}>{label}</span>
    <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }}>
      {children}
    </span>
    {onCopy && (
      <IconButton
        onClick={onCopy}
        title={copyTitle ?? ''}
        ariaLabel={copyAriaLabel ?? ''}
        icon={<Copy size={12} />}
      />
    )}
  </div>
);
