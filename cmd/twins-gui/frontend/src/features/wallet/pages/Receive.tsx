import React, { useEffect, useState, useCallback, useRef, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { QRCodeCanvas } from 'qrcode.react';
import { useReceive } from '@/store/useStore';
import { Copy, RefreshCw, ChevronDown, Eye, Trash2, ExternalLink, Download } from 'lucide-react';
import { sanitizeText } from '@/shared/utils/sanitize';
import { useDisplayUnits } from '@/shared/hooks/useDisplayUnits';
import { buildTwinsURI, MAX_QR_DATA_LENGTH } from '@/shared/utils/twinsUri';
import { writeToClipboard } from '@/shared/utils/clipboard';
import { truncateAddress } from '@/shared/utils/format';
import { SaveQRImage } from '@wailsjs/go/main/App';
import { createCircularLogoDataURL } from '@/shared/utils/qrLogo';
import { buildQRFilename } from '@/shared/utils/qrFilename';
import { SimpleConfirmDialog } from '@/shared/components/SimpleConfirmDialog';
import { ReceivingAddressesDialog, RequestPaymentDialog } from '@/components/dialogs';

// Amount unit options
const UNIT_OPTIONS = ['TWINS', 'mTWINS', 'uTWINS'] as const;
type AmountUnit = typeof UNIT_OPTIONS[number];

// Helper to generate unique key for payment request (ID is per-address, not global)
const getRequestKey = (request: { address: string; id: number }): string =>
  `${request.address}_${request.id}`;

export const Receive: React.FC = () => {
  const { t } = useTranslation('wallet');
  const { formatAmount } = useDisplayUnits();
  const {
    currentAddress,
    paymentRequests,
    reuseAddress,
    formState,
    isLoading,
    isCreatingRequest,
    isGeneratingAddress,
    error,
    setReuseAddress,
    updateFormField,
    clearForm,
    fetchCurrentAddress,
    fetchPaymentRequests,
    createPaymentRequest,
    deletePaymentRequest,
    generateNewAddress,
    isAddressesDialogOpen,
    openAddressesDialog,
    closeAddressesDialog,
    openRequestDialog,
    clearError,
    addressJustSelected,
    clearAddressJustSelected,
  } = useReceive();

  // Local state
  const [selectedUnit, setSelectedUnit] = useState<AmountUnit>('TWINS');
  const [copyFeedback, setCopyFeedback] = useState<string | null>(null);
  const [confirmRemoveKey, setConfirmRemoveKey] = useState<string | null>(null);
  const [sortColumn, setSortColumn] = useState<'date' | 'label' | 'amount'>('date');
  const [sortAscending, setSortAscending] = useState(false);

  // Brief highlight on the address row after picker selection
  const [addressHighlight, setAddressHighlight] = useState(false);

  const qrRef = useRef<HTMLDivElement>(null);
  const [qrLogoSrc, setQrLogoSrc] = useState<string | undefined>();

  // Show toast + highlight when an address is picked from the dialog.
  // Split into two effects: one to consume the flag, one to manage the
  // highlight timer. Combining them caused clearAddressJustSelected() to
  // change the dependency, triggering cleanup which cancelled the timer.
  useEffect(() => {
    if (!addressJustSelected) return;
    setCopyFeedback(t('receive.addressSelectedFeedback'));
    setAddressHighlight(true);
    clearAddressJustSelected();
  }, [addressJustSelected, clearAddressJustSelected, t]);

  // Auto-clear address highlight after 1.5s
  useEffect(() => {
    if (!addressHighlight) return;
    const timer = setTimeout(() => setAddressHighlight(false), 1500);
    return () => clearTimeout(timer);
  }, [addressHighlight]);

  // Generate circular-bordered logo for QR code
  useEffect(() => {
    createCircularLogoDataURL('/icons/twins-logo.png', 64, 4, '#27ae60')
      .then(setQrLogoSrc)
      .catch(() => {}); // Falls back to no logo if image fails to load
  }, []);

  // Fetch data on mount only
  useEffect(() => {
    fetchCurrentAddress();
    fetchPaymentRequests();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Auto-clear copy feedback
  useEffect(() => {
    if (!copyFeedback) return;
    const timeoutId = setTimeout(() => setCopyFeedback(null), 2000);
    return () => clearTimeout(timeoutId);
  }, [copyFeedback]);

  // Converted amount in TWINS — shared by liveURI and handleSaveQR
  const convertedAmount = useMemo((): number | undefined => {
    if (!formState.amount) return undefined;
    const parsed = parseFloat(formState.amount);
    if (isNaN(parsed) || parsed <= 0) return undefined;
    switch (selectedUnit) {
      case 'mTWINS': return parsed / 1000;
      case 'uTWINS': return parsed / 1000000;
      default: return parsed;
    }
  }, [formState.amount, selectedUnit]);

  // Live QR code URI — updates as form fields change
  const liveURI = useMemo(() => {
    if (!currentAddress) return '';
    return buildTwinsURI(
      currentAddress,
      convertedAmount,
      formState.label || undefined,
      formState.message || undefined,
    );
  }, [currentAddress, convertedAmount, formState.label, formState.message]);

  const isURITooLong = liveURI.length > MAX_QR_DATA_LENGTH;

  // Copy address to clipboard
  const handleCopyAddress = useCallback(async () => {
    if (!currentAddress) return;
    const ok = await writeToClipboard(currentAddress);
    setCopyFeedback(ok ? t('receive.copied') : t('receive.copyFailed'));
  }, [currentAddress, t]);

  // Copy URI to clipboard. Falls back to a bare `twins:<address>` URI when
  // `liveURI` is not yet built so the handler always copies whatever the UI
  // is currently displaying in the URI row.
  const handleCopyURI = useCallback(async () => {
    const uri = liveURI || (currentAddress ? `twins:${currentAddress}` : '');
    if (!uri) return;
    const ok = await writeToClipboard(uri);
    setCopyFeedback(ok ? t('receive.uriCopied') : t('receive.copyFailed'));
  }, [liveURI, currentAddress, t]);

  // Generate new address
  const handleNewAddress = useCallback(async () => {
    await generateNewAddress('');
  }, [generateNewAddress]);

  // Handle form submission
  const handleCreateRequest = useCallback(async () => {
    await createPaymentRequest(selectedUnit);
  }, [createPaymentRequest, selectedUnit]);

  // Handle clear button
  const handleClear = useCallback(() => {
    clearForm();
    clearError();
    setSelectedUnit('TWINS');
  }, [clearForm, clearError]);

  // Sort payment requests
  const sortedRequests = useMemo(() => {
    return [...paymentRequests].sort((a, b) => {
      let comparison = 0;
      switch (sortColumn) {
        case 'date':
          comparison = new Date(a.date).getTime() - new Date(b.date).getTime();
          break;
        case 'label':
          comparison = (a.label || '').localeCompare(b.label || '');
          break;
        case 'amount':
          comparison = (a.amount || 0) - (b.amount || 0);
          break;
      }
      return sortAscending ? comparison : -comparison;
    });
  }, [paymentRequests, sortColumn, sortAscending]);

  // Handle column header click for sorting
  const handleSort = useCallback((column: typeof sortColumn) => {
    if (sortColumn === column) {
      setSortAscending(!sortAscending);
    } else {
      setSortColumn(column);
      setSortAscending(column === 'date' ? false : true);
    }
  }, [sortColumn, sortAscending]);

  // Format date for display
  const formatDate = (dateStr: string): string => {
    const date = new Date(dateStr);
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
  };

  // Render sort indicator
  const renderSortIndicator = (column: typeof sortColumn) => {
    if (sortColumn !== column) return null;
    return (
      <ChevronDown
        size={10}
        style={{
          display: 'inline-block',
          marginLeft: '3px',
          transform: sortAscending ? 'rotate(180deg)' : 'rotate(0deg)',
          transition: 'transform 0.2s',
        }}
      />
    );
  };

  // Handle View button on history row
  const handleViewRequest = useCallback((key: string) => {
    const request = paymentRequests.find(r => getRequestKey(r) === key);
    if (request) openRequestDialog(request);
  }, [paymentRequests, openRequestDialog]);

  // Handle Remove button - show confirmation first
  const handleRemoveClick = useCallback((key: string) => {
    setConfirmRemoveKey(key);
  }, []);

  const handleConfirmRemove = useCallback(async () => {
    if (confirmRemoveKey !== null) {
      const request = paymentRequests.find(r => getRequestKey(r) === confirmRemoveKey);
      if (request) {
        await deletePaymentRequest(request.address, request.id);
      }
    }
    setConfirmRemoveKey(null);
  }, [confirmRemoveKey, paymentRequests, deletePaymentRequest]);

  // Save QR code as image via native save dialog
  const handleSaveQR = useCallback(async () => {
    if (!qrRef.current) return;
    try {
      const canvas = qrRef.current.querySelector('canvas');
      if (!canvas) throw new Error('canvas not found');
      const pngBase64 = canvas.toDataURL('image/png');
      const defaultFilename = buildQRFilename(currentAddress, formState.label, convertedAmount);
      const saved = await SaveQRImage(pngBase64, defaultFilename);
      if (saved) {
        setCopyFeedback(t('receive.qrSaved'));
      }
      // If saved === false the user cancelled — show no feedback
    } catch {
      setCopyFeedback(t('receive.copyFailed'));
    }
  }, [currentAddress, formState.label, convertedAmount, t]);

  return (
    <div className="qt-frame" style={{ height: '100%', display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
      <div style={{ padding: '12px 16px', display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0, gap: '12px' }}>

        {/* Two-column hero section */}
        <div style={{ display: 'flex', gap: '16px', flexShrink: 0 }}>

          {/* LEFT COLUMN — QR Code Hero */}
          {/*
            minWidth: 0 + maxWidth: 340px pin the column at exactly its
            flex-basis (340px) regardless of inner content. Without these,
            `min-width: auto` on the flex item lets the URI row's intrinsic
            content width override the basis, causing the entire column to
            grow when the URI gets long. With the column pinned, the URI
            text inside the URI row truncates correctly via its existing
            overflow/textOverflow/whiteSpace styles.
          */}
          <div style={{
            flex: '0 0 340px',
            minWidth: 0,
            maxWidth: '340px',
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            padding: '24px 20px',
            backgroundColor: '#2f2f2f',
            borderRadius: '8px',
            border: '1px solid #3a3a3a',
          }}>
            {/* QR Code */}
            <div
              ref={qrRef}
              style={{
                padding: '12px',
                backgroundColor: '#ffffff',
                borderRadius: '8px',
                lineHeight: 0,
                cursor: 'pointer',
              }}
              onClick={handleSaveQR}
              title={t('receive.clickToSaveQR')}
            >
              {currentAddress ? (
                <QRCodeCanvas
                  value={liveURI || `twins:${currentAddress}`}
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
              ) : (
                <div style={{ width: 200, height: 200, display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#999', fontSize: '12px' }}>
                  {t('common:status.loading')}
                </div>
              )}
            </div>

            {/* Action pills (Save image + New address) — discoverable affordances under the QR */}
            <div style={{ display: 'flex', gap: '8px', marginTop: '12px' }}>
              <PillButton
                onClick={handleSaveQR}
                disabled={!currentAddress || isGeneratingAddress}
                title={t('receive.saveImage')}
                ariaLabel={t('receive.saveImage')}
                icon={<Download size={12} />}
                label={t('receive.saveImage')}
              />
              <PillButton
                onClick={handleNewAddress}
                disabled={isGeneratingAddress || isLoading}
                title={t('receive.newAddress')}
                ariaLabel={t('receive.newAddress')}
                icon={<RefreshCw size={12} style={isGeneratingAddress ? { animation: 'spin 1s linear infinite' } : undefined} />}
                label={t('receive.newAddress')}
                cursor={isGeneratingAddress ? 'wait' : undefined}
              />
            </div>

            {/* URI too long warning */}
            {isURITooLong && (
              <div style={{
                marginTop: '12px',
                padding: '4px 10px',
                backgroundColor: '#4a3a2a',
                border: '1px solid #ff9966',
                borderRadius: '4px',
                color: '#ff9966',
                fontSize: '10px',
                textAlign: 'center',
              }}>
                {t('receive.uriTooLong')}
              </div>
            )}

            {/* Address row with inline copy icon */}
            <div style={{
              marginTop: '16px',
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              padding: '8px 12px',
              backgroundColor: '#252525',
              border: addressHighlight ? '1px solid #27ae60' : '1px solid #3a3a3a',
              borderRadius: '6px',
              width: '100%',
              transition: 'border-color 0.3s ease-in-out',
            }}>
              <span
                title={currentAddress}
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
                  minWidth: 0,
                }}
              >
                {currentAddress ? truncateAddress(currentAddress, 12, 10) : '...'}
              </span>
              <CopyIconButton
                onClick={handleCopyAddress}
                disabled={!currentAddress}
                title={t('receive.copyAddress')}
                ariaLabel={t('receive.copyAddress')}
              />
            </div>

            {/* URI row with inline copy icon — always visible */}
            <div style={{
              marginTop: '8px',
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              padding: '8px 12px',
              backgroundColor: '#252525',
              border: '1px solid #3a3a3a',
              borderRadius: '6px',
              width: '100%',
            }}>
              <span style={{ fontSize: '11px', color: '#888', flexShrink: 0 }}>
                {t('receive.uri')}
              </span>
              <span
                title={liveURI || (currentAddress ? `twins:${currentAddress}` : '')}
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
                {liveURI || (currentAddress ? `twins:${currentAddress}` : '...')}
              </span>
              <CopyIconButton
                onClick={handleCopyURI}
                disabled={!liveURI && !currentAddress}
                title={t('receive.copyUri')}
                ariaLabel={t('receive.copyUri')}
              />
            </div>
          </div>

          {/* RIGHT COLUMN — Request Payment Form */}
          <div style={{
            flex: 1,
            display: 'flex',
            flexDirection: 'column',
            padding: '20px',
            backgroundColor: '#2f2f2f',
            borderRadius: '8px',
            border: '1px solid #3a3a3a',
          }}>
            <div style={{ fontSize: '13px', fontWeight: 600, color: '#ccc', marginBottom: '4px' }}>
              {t('receive.requestPaymentTitle')}
            </div>
            <div style={{ fontSize: '11px', color: '#777', marginBottom: '16px' }}>
              {t('receive.requestPaymentSubtitle')}
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: '10px', flex: 1 }}>
              {/* Label */}
              <div>
                <label htmlFor="receive-label" style={{ display: 'block', fontSize: '11px', color: '#888', marginBottom: '4px' }}>
                  {t('receive.label')}
                </label>
                <input
                  id="receive-label"
                  type="text"
                  value={formState.label}
                  onChange={(e) => updateFormField('label', e.target.value)}
                  maxLength={100}
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck={false}
                  placeholder={t('receive.labelPlaceholder')}
                  style={{
                    width: '100%',
                    padding: '7px 10px',
                    fontSize: '12px',
                    backgroundColor: '#252525',
                    border: '1px solid #3a3a3a',
                    borderRadius: '4px',
                    color: '#ddd',
                    outline: 'none',
                  }}
                />
              </div>

              {/* Amount + unit */}
              <div>
                <label htmlFor="receive-amount" style={{ display: 'block', fontSize: '11px', color: '#888', marginBottom: '4px' }}>
                  {t('receive.amount')}
                </label>
                <div style={{ display: 'flex', gap: '6px' }}>
                  <input
                    id="receive-amount"
                    type="text"
                    value={formState.amount}
                    onChange={(e) => {
                      const value = e.target.value;
                      if (value === '' || /^\d*$/.test(value) || /^\d*\.\d*$/.test(value)) {
                        updateFormField('amount', value);
                      }
                    }}
                    placeholder="0.00"
                    style={{
                      flex: 1,
                      padding: '7px 10px',
                      fontSize: '12px',
                      backgroundColor: '#252525',
                      border: '1px solid #3a3a3a',
                      borderRadius: '4px',
                      color: '#ddd',
                      outline: 'none',
                    }}
                  />
                  <select
                    id="receive-unit"
                    value={selectedUnit}
                    onChange={(e) => setSelectedUnit(e.target.value as AmountUnit)}
                    style={{
                      minWidth: '85px',
                      padding: '7px 24px 7px 10px',
                      fontSize: '12px',
                      backgroundColor: '#252525',
                      border: '1px solid #3a3a3a',
                      borderRadius: '4px',
                      color: '#ddd',
                      cursor: 'pointer',
                      outline: 'none',
                    }}
                  >
                    {UNIT_OPTIONS.map(unit => (
                      <option key={unit} value={unit}>{unit}</option>
                    ))}
                  </select>
                </div>
              </div>

              {/* Message */}
              <div>
                <label htmlFor="receive-message" style={{ display: 'block', fontSize: '11px', color: '#888', marginBottom: '4px' }}>
                  {t('receive.message')}
                </label>
                <input
                  id="receive-message"
                  type="text"
                  value={formState.message}
                  onChange={(e) => updateFormField('message', e.target.value)}
                  maxLength={120}
                  autoCapitalize="off"
                  autoCorrect="off"
                  spellCheck={false}
                  placeholder={t('receive.messagePlaceholder')}
                  style={{
                    width: '100%',
                    padding: '7px 10px',
                    fontSize: '12px',
                    backgroundColor: '#252525',
                    border: '1px solid #3a3a3a',
                    borderRadius: '4px',
                    color: '#ddd',
                    outline: 'none',
                  }}
                />
              </div>

              {/* New address per request checkbox */}
              <label style={{
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
                cursor: 'pointer',
                fontSize: '11px',
                color: '#888',
                marginTop: '2px',
              }}>
                <input
                  type="checkbox"
                  checked={!reuseAddress}
                  onChange={(e) => setReuseAddress(!e.target.checked)}
                  className="qt-checkbox"
                  style={{ width: '13px', height: '13px' }}
                />
                {t('receive.newAddressPerRequest')}
              </label>

              {/* Error display */}
              {error && (
                <div style={{
                  padding: '6px 10px',
                  backgroundColor: '#4a2a2a',
                  border: '1px solid #ff6666',
                  borderRadius: '4px',
                  color: '#ff6666',
                  fontSize: '11px',
                }}>
                  {sanitizeText(error)}
                </div>
              )}

              {/* Spacer */}
              <div style={{ flex: 1 }} />

              {/* Action buttons */}
              <div style={{ display: 'flex', gap: '8px', alignItems: 'center' }}>
                <button
                  type="button"
                  onClick={handleCreateRequest}
                  disabled={isCreatingRequest || isLoading}
                  style={{
                    flex: 1,
                    padding: '8px 16px',
                    fontSize: '12px',
                    fontWeight: 500,
                    backgroundColor: '#4a7c59',
                    border: '1px solid #5a8c69',
                    borderRadius: '6px',
                    color: '#fff',
                    cursor: isCreatingRequest ? 'wait' : 'pointer',
                    opacity: isCreatingRequest ? 0.7 : 1,
                    transition: 'background-color 0.15s',
                  }}
                >
                  {isCreatingRequest ? t('receive.requestingPayment') : t('receive.createRequest')}
                </button>
                <button
                  type="button"
                  onClick={handleClear}
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
                  {t('receive.clear')}
                </button>
              </div>

              {/* Receiving addresses link */}
              <button
                type="button"
                onClick={openAddressesDialog}
                style={{
                  background: 'none',
                  border: 'none',
                  color: '#6699cc',
                  fontSize: '11px',
                  cursor: 'pointer',
                  padding: '0',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '4px',
                  alignSelf: 'flex-start',
                }}
              >
                <ExternalLink size={11} />
                {t('receive.receivingAddresses')}
              </button>
            </div>
          </div>
        </div>

        {/* BOTTOM — Recent Requests History */}
        <div style={{
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          minHeight: 0,
          backgroundColor: '#2f2f2f',
          borderRadius: '8px',
          border: '1px solid #3a3a3a',
          padding: '12px 16px',
        }}>
          {/* Header with sort options */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: '8px', flexShrink: 0 }}>
            <span style={{ fontSize: '12px', fontWeight: 600, color: '#aaa' }}>
              {t('receive.recentRequests')} ({paymentRequests.length})
            </span>
            <div style={{ display: 'flex', gap: '12px', fontSize: '11px', color: '#666' }}>
              <button
                onClick={() => handleSort('date')}
                style={{
                  background: 'none', border: 'none', cursor: 'pointer',
                  color: sortColumn === 'date' ? '#aaa' : '#666', fontSize: '11px', padding: 0,
                }}
              >
                {t('receive.table.date')}{renderSortIndicator('date')}
              </button>
              <button
                onClick={() => handleSort('label')}
                style={{
                  background: 'none', border: 'none', cursor: 'pointer',
                  color: sortColumn === 'label' ? '#aaa' : '#666', fontSize: '11px', padding: 0,
                }}
              >
                {t('receive.table.label')}{renderSortIndicator('label')}
              </button>
              <button
                onClick={() => handleSort('amount')}
                style={{
                  background: 'none', border: 'none', cursor: 'pointer',
                  color: sortColumn === 'amount' ? '#aaa' : '#666', fontSize: '11px', padding: 0,
                }}
              >
                {t('receive.table.amount')}{renderSortIndicator('amount')}
              </button>
            </div>
          </div>

          {/* Scrollable card list */}
          <div style={{ flex: 1, overflowY: 'auto', overflowX: 'hidden', minHeight: 0 }}>
            {sortedRequests.length === 0 ? (
              <div style={{ textAlign: 'center', color: '#555', padding: '24px', fontSize: '12px' }}>
                {isLoading ? t('common:status.loading') : t('receive.noRequests')}
              </div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                {sortedRequests.map((request) => {
                  const rowKey = getRequestKey(request);
                  return (
                    <div
                      key={rowKey}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: '12px',
                        padding: '8px 12px',
                        backgroundColor: '#2a2a2a',
                        borderRadius: '6px',
                        border: '1px solid transparent',
                        cursor: 'default',
                        transition: 'border-color 0.15s',
                      }}
                      onMouseEnter={(e) => { (e.currentTarget as HTMLDivElement).style.borderColor = '#444'; }}
                      onMouseLeave={(e) => { (e.currentTarget as HTMLDivElement).style.borderColor = 'transparent'; }}
                    >
                      {/* Date */}
                      <span style={{ fontSize: '11px', color: '#666', minWidth: '50px', flexShrink: 0 }}>
                        {formatDate(request.date)}
                      </span>

                      {/* Label + message */}
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: '12px', color: '#ddd', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                          {sanitizeText(request.label || t('receive.noLabel'))}
                        </div>
                        {request.message && (
                          <div style={{ fontSize: '10px', color: '#666', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginTop: '1px' }}>
                            {sanitizeText(request.message)}
                          </div>
                        )}
                      </div>

                      {/* Amount */}
                      <span style={{ fontSize: '12px', color: '#4a7c59', fontWeight: 500, flexShrink: 0, minWidth: '80px', textAlign: 'right' }}>
                        {request.amount ? formatAmount(request.amount, true) : '-'}
                      </span>

                      {/* Action buttons */}
                      <div style={{ display: 'flex', gap: '4px', flexShrink: 0 }}>
                        <button
                          type="button"
                          onClick={() => handleViewRequest(rowKey)}
                          title={t('receive.show')}
                          style={{
                            display: 'flex', alignItems: 'center', justifyContent: 'center',
                            width: '26px', height: '26px',
                            background: 'none', border: '1px solid #3a3a3a', borderRadius: '4px',
                            color: '#888', cursor: 'pointer', transition: 'color 0.15s, border-color 0.15s',
                          }}
                          onMouseEnter={(e) => { const el = e.currentTarget; el.style.color = '#ddd'; el.style.borderColor = '#555'; }}
                          onMouseLeave={(e) => { const el = e.currentTarget; el.style.color = '#888'; el.style.borderColor = '#3a3a3a'; }}
                        >
                          <Eye size={13} />
                        </button>
                        <button
                          type="button"
                          onClick={() => handleRemoveClick(rowKey)}
                          title={t('receive.remove')}
                          style={{
                            display: 'flex', alignItems: 'center', justifyContent: 'center',
                            width: '26px', height: '26px',
                            background: 'none', border: '1px solid #3a3a3a', borderRadius: '4px',
                            color: '#888', cursor: 'pointer', transition: 'color 0.15s, border-color 0.15s',
                          }}
                          onMouseEnter={(e) => { const el = e.currentTarget; el.style.color = '#ff6666'; el.style.borderColor = '#555'; }}
                          onMouseLeave={(e) => { const el = e.currentTarget; el.style.color = '#888'; el.style.borderColor = '#3a3a3a'; }}
                        >
                          <Trash2 size={13} />
                        </button>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Receiving Addresses Dialog */}
      <ReceivingAddressesDialog
        isOpen={isAddressesDialogOpen}
        onClose={closeAddressesDialog}
      />

      {/* Request Payment Dialog (for viewing saved requests) */}
      <RequestPaymentDialog />

      {/* Remove Confirmation Dialog */}
      <SimpleConfirmDialog
        isOpen={confirmRemoveKey !== null}
        title={t('receive.removeConfirmTitle')}
        message={t('receive.removeConfirmMessage')}
        confirmText={t('receive.remove')}
        onConfirm={handleConfirmRemove}
        onCancel={() => setConfirmRemoveKey(null)}
        isDestructive
      />

      {/* Copy Feedback Toast */}
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
            zIndex: 50,
            border: '1px solid #555',
          }}
        >
          {copyFeedback}
        </div>
      )}

    </div>
  );
};

// Reusable inline copy icon button — matches the CopyIconButton pattern from
// RequestPaymentDialog.tsx so the Receive page and Payment Request dialog
// share the same affordance for copy actions.
const CopyIconButton: React.FC<{
  onClick: () => void;
  title: string;
  ariaLabel: string;
  disabled?: boolean;
}> = ({ onClick, title, ariaLabel, disabled }) => (
  <button
    type="button"
    onClick={onClick}
    disabled={disabled}
    title={title}
    aria-label={ariaLabel}
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
      cursor: disabled ? 'not-allowed' : 'pointer',
      flexShrink: 0,
      opacity: disabled ? 0.5 : 1,
      transition: 'color 0.15s, border-color 0.15s',
    }}
    onMouseEnter={(e) => {
      if (disabled) return;
      e.currentTarget.style.color = '#ddd';
      e.currentTarget.style.borderColor = '#555';
    }}
    onMouseLeave={(e) => {
      e.currentTarget.style.color = '#888';
      e.currentTarget.style.borderColor = '#3a3a3a';
    }}
  >
    <Copy size={12} />
  </button>
);

// Reusable outline pill button — used for the Save image and New address
// affordances rendered below the QR code. Matches the visual style of the
// "Save image" pill in RequestPaymentDialog.tsx.
const PillButton: React.FC<{
  onClick: () => void;
  title: string;
  ariaLabel: string;
  icon: React.ReactNode;
  label: string;
  disabled?: boolean;
  cursor?: React.CSSProperties['cursor'];
}> = ({ onClick, title, ariaLabel, icon, label, disabled, cursor }) => (
  <button
    type="button"
    onClick={onClick}
    disabled={disabled}
    title={title}
    aria-label={ariaLabel}
    style={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      gap: '6px',
      padding: '6px 14px',
      fontSize: '11px',
      fontWeight: 500,
      backgroundColor: 'transparent',
      border: '1px solid #4a4a4a',
      borderRadius: '999px',
      color: '#ccc',
      cursor: disabled ? 'not-allowed' : (cursor ?? 'pointer'),
      opacity: disabled ? 0.5 : 1,
      transition: 'background-color 0.15s, border-color 0.15s, color 0.15s',
    }}
    onMouseEnter={(e) => {
      if (disabled) return;
      e.currentTarget.style.backgroundColor = '#383838';
      e.currentTarget.style.borderColor = '#5a5a5a';
      e.currentTarget.style.color = '#fff';
    }}
    onMouseLeave={(e) => {
      e.currentTarget.style.backgroundColor = 'transparent';
      e.currentTarget.style.borderColor = '#4a4a4a';
      e.currentTarget.style.color = '#ccc';
    }}
  >
    {icon}
    {label}
  </button>
);
