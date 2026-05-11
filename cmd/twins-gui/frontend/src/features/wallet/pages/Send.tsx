import React, { useState, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useForm, useFieldArray, useWatch } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Plus } from 'lucide-react';
import { useStore, useCoinControl, useAddressBook, useNotifications } from '@/store/useStore';
import { useDisplayUnits } from '@/shared/hooks/useDisplayUnits';
import { Banner } from '@/shared/components/Banner';
import { PillButton } from '@/shared/components/PillButton';
import { ConfirmationDialog, Recipient, SendTransactionResult } from '../components/ConfirmationDialog';
import { CoinControlDialog } from '../components/CoinControlDialog';
import { SendCoinControl } from '../components/SendCoinControl';
import { SendRecipients } from '../components/SendRecipients';
import { SendFeeControls } from '../components/SendFeeControls';
import { CustomFeeDialog } from '../components/CustomFeeDialog';
import { SendTransactionTotals } from '../components/SendTransactionTotals';
import { SendTransactionMulti, GetWalletEncryptionStatus, UnlockWallet, ValidateAddress, EstimateFee, EstimateTransactionFee } from '@wailsjs/go/main/App';
import { restoreWalletState } from '@/shared/utils/walletState';
import { useAddressBookPicker } from '../components/AddressBookDialog';
import { main } from '@wailsjs/go/models';
import { sanitizeErrorMessage } from '@/shared/utils/sanitize';
import { useWalletActions } from '@/shared/hooks/useWalletActions';
import {
  DUST_THRESHOLD,
  SATOSHIS_PER_TWINS,
  parseAmount,
  calculateTotals,
  formatAmountDisplay,
  formatAmountForInput,
  calculateMaxSendable,
  formatAmountInput
} from '@/utils/amountValidation';
import { calculateTotalFee, MIN_TX_FEE } from '@/utils/feeCalculation';

// Design tokens (mirroring Receive page; see frontend/CLAUDE.md "Design Tokens")
const CARD_PADDING_STANDARD = '20px';
const cardStyle: React.CSSProperties = {
  backgroundColor: '#2f2f2f',
  border: '1px solid #3a3a3a',
  borderRadius: '8px',
  padding: CARD_PADDING_STANDARD,
};

// TWINS address validation regex
// MainNet: W (P2PKH, prefix 0x49) and a (P2SH, prefix 0x53)
// TestNet: m or n (P2PKH, prefix 0x6F)
const TWINS_ADDRESS_REGEX = /^[Wamn][a-km-zA-HJ-NP-Z1-9]{33}$/;

// Soft UX cap. The daemon's hard bound is the 100 KB MAX_STANDARD_TX_SIZE;
// 100 recipients stays well under that ceiling.
const MAX_RECIPIENTS = 100;

// Form validation schemas
const recipientSchema = z.object({
  address: z.string()
    .min(1, 'Address is required')
    .regex(TWINS_ADDRESS_REGEX, 'Invalid TWINS address format'),
  amount: z.string()
    .min(1, 'Amount is required')
    .transform((val) => formatAmountInput(val))
    .refine((val) => {
      const num = parseAmount(val);
      return num > 0;
    }, 'Amount must be greater than 0')
    .refine((val) => {
      const num = parseAmount(val);
      return num >= DUST_THRESHOLD;
    }, `Amount must be at least ${DUST_THRESHOLD} TWINS to avoid dust`),
  label: z.string().optional(),
});

const sendFormSchema = z.object({
  recipients: z.array(recipientSchema).min(1, 'At least one recipient is required'),
  customChangeAddress: z.boolean().optional(),
  changeAddress: z.string().optional(),
  splitUTXO: z.boolean().optional(),
  splitOutputs: z.string().optional(),
});

type SendFormData = z.infer<typeof sendFormSchema>;

export const Send: React.FC = () => {
  const { t } = useTranslation('wallet');
  const balance = useStore((s) => s.balance);
  const { formatAmount } = useDisplayUnits();
  const { openDialog, closeDialog, coinControl, utxos, summary, resetCoinControl } = useCoinControl();
  const { refreshBalance } = useWalletActions();
  const { openPicker: openAddressBookPicker } = useAddressBookPicker();
  const { addContact, contacts, fetchContacts } = useAddressBook();
  const { addNotification } = useNotifications();

  // Calculate coin control state for display
  const coinControlSelectedCount = coinControl.selectedCoins.size;
  const coinControlSelectedAmount = coinControlSelectedCount > 0
    ? utxos
        .filter(utxo => coinControl.selectedCoins.has(`${utxo.txid}:${utxo.vout}`))
        .reduce((sum, utxo) => sum + utxo.amount, 0)
    : 0;

  // Coin control features enabled (from Expert settings, reactive via store)
  const coinControlEnabled = useStore((s) => s.coinControlEnabled);
  const syncCoinControlEnabled = useStore((s) => s.syncCoinControlEnabled);

  // Fee rate state (matches old GUI behavior)
  const [feeRate, setFeeRate] = useState(0.0001); // Default fee rate in TWINS/kB
  const [sliderPosition, setSliderPosition] = useState(0); // 0-100 slider position
  const [estimateFeeAvailable, setEstimateFeeAvailable] = useState(true); // EstimateFee API availability
  const [showCustomFeeDialog, setShowCustomFeeDialog] = useState(false); // Custom fee dialog visibility

  // Blockchain info from shared store (populated by useP2PEvents in MainLayout)
  const blockchainInfo = useStore((state) => state.blockchainInfo);

  const [transactionTotals, setTransactionTotals] = useState<ReturnType<typeof calculateTotals> | null>(null);
  const [showConfirmDialog, setShowConfirmDialog] = useState(false);
  const [isWalletEncrypted, setIsWalletEncrypted] = useState(false);
  // True when wallet is in staking-only mode (drives ConfirmationDialog hint)
  const [walletWasStakingOnly, setWalletWasStakingOnly] = useState(false);
  // Track wallet status before unlock so we can restore it correctly after send
  const priorWalletStatusRef = useRef<string>('');
  const [formError, setFormError] = useState<string | null>(null);
  const [pendingTransaction, setPendingTransaction] = useState<{
    recipients: Recipient[];
    fee: number;
    total: number;
    changeAddress?: string;
  } | null>(null);

  const {
    register,
    control,
    handleSubmit,
    setValue,
    trigger,
    reset,
    formState: { errors }
  } = useForm<SendFormData>({
    resolver: zodResolver(sendFormSchema),
    defaultValues: {
      recipients: [{ address: '', amount: '', label: '' }],
      customChangeAddress: false,
      splitUTXO: false,
      splitOutputs: '2',
    },
  });

  const { fields, append, remove } = useFieldArray({
    control,
    name: 'recipients',
  });

  // Use useWatch for reactive updates - properly subscribes to field changes
  // This ensures transaction totals update when any recipient field changes
  const watchedRecipients = useWatch({ control, name: 'recipients' }) ?? [];
  const watchedSplitUTXO = useWatch({ control, name: 'splitUTXO' }) ?? false;
  const watchedCustomChangeAddress = useWatch({ control, name: 'customChangeAddress' }) ?? false;
  const watchedSplitOutputs = useWatch({ control, name: 'splitOutputs' }) ?? '2';

  // Development assertions to catch form registration issues early
  if (import.meta.env.DEV) {
    console.assert(watchedRecipients !== undefined, 'recipients field not registered');
    console.assert(watchedSplitUTXO !== undefined, 'splitUTXO field not registered');
    console.assert(watchedCustomChangeAddress !== undefined, 'customChangeAddress field not registered');
  }

  // Clear coin control selection when leaving the Send page
  useEffect(() => {
    return () => {
      closeDialog();      // Close first to gate any in-flight loadUTXOs async work
      resetCoinControl(); // Then clear selection state
    };
  }, [resetCoinControl, closeDialog]);

  // Fetch balance, contacts, and coin control setting on component mount
  useEffect(() => {
    refreshBalance();
    fetchContacts();
    syncCoinControlEnabled();
  }, [refreshBalance, fetchContacts, syncCoinControlEnabled]);

  // Check wallet encryption status on mount and periodically
  useEffect(() => {
    const checkEncryptionStatus = async () => {
      try {
        const status = await GetWalletEncryptionStatus();
        // Show passphrase input when wallet is locked OR in staking-only mode
        // (staking-only wallets need a temporary full unlock to send)
        const needsUnlock = status?.encrypted === true &&
          (status?.locked === true || status?.status === 'unlocked_staking');
        setIsWalletEncrypted(needsUnlock);
        setWalletWasStakingOnly(status?.status === 'unlocked_staking');
        // Capture prior status so we can restore correctly after the send
        priorWalletStatusRef.current = status?.status || '';
      } catch (error) {
        console.error('Failed to check wallet encryption status:', error);
        setIsWalletEncrypted(false);
        setWalletWasStakingOnly(false);
        priorWalletStatusRef.current = '';
      }
    };

    checkEncryptionStatus();

    // Poll periodically in case wallet gets locked/unlocked externally
    const intervalId = setInterval(checkEncryptionStatus, 5000);

    return () => clearInterval(intervalId);
  }, []);

  // Check EstimateFee API availability on mount
  useEffect(() => {
    const checkEstimateFeeAvailability = async () => {
      try {
        // Try to estimate fee for 6 blocks confirmation target
        const feeEstimate = await EstimateFee(6);
        // If fee is <= 0, estimation is not available (not enough data)
        setEstimateFeeAvailable(feeEstimate > 0);
      } catch (error) {
        console.warn('EstimateFee unavailable:', error);
        setEstimateFeeAvailable(false);
      }
    };

    checkEstimateFeeAvailability();
  }, []);

  // State for backend fee estimation results (includes inputCount and txSize for potential UI display)
  const [_backendFeeEstimate, setBackendFeeEstimate] = useState<main.FeeEstimateResult | null>(null);

  // Calculate transaction totals whenever recipients or fees change
  // Uses backend EstimateTransactionFee for accurate fee based on actual UTXO selection
  useEffect(() => {
    const calculateFeeFromBackend = async () => {
      const availableBalance = balance?.spendable || 0;

      // Build recipients map for backend call
      const recipientsMap: Record<string, number> = {};
      let hasValidRecipients = false;

      for (const recipient of watchedRecipients) {
        const amount = parseAmount(recipient.amount || '0');
        if (recipient.address && amount > 0) {
          recipientsMap[recipient.address] = amount;
          hasValidRecipients = true;
        }
      }

      // If no valid recipients, use frontend-only estimation (fallback)
      if (!hasValidRecipients) {
        const recipientCount = watchedRecipients.length || 1;
        // Use actual coin control input count when available so the Max button
        // calculates the correct amount even before the user types a value.
        const inputCount = coinControlSelectedCount > 0 ? coinControlSelectedCount : 2;
        const rawFee = calculateTotalFee(recipientCount, feeRate, inputCount);
        // Apply minimum fee clamp to match backend behaviour (MinTxFee = 10000 sat)
        const fallbackFee = Math.max(rawFee, MIN_TX_FEE);
        const totals = calculateTotals(watchedRecipients, fallbackFee, availableBalance);
        setTransactionTotals(totals);
        setBackendFeeEstimate(null);
        return;
      }

      try {
        // Build options for fee estimation
        const options: main.SendTransactionOptions = {
          feeRate: feeRate,
          splitCount: watchedSplitUTXO ? parseInt(watchedSplitOutputs || '2') : 0,
          selectedUtxos: coinControlSelectedCount > 0
            ? Array.from(coinControl.selectedCoins)
            : [],
          changeAddress: '',
        };

        // Call backend for accurate fee estimation
        // Cast to any to bypass Wails-generated type checking - runtime handles conversion
        const feeResult = await EstimateTransactionFee({
          recipients: recipientsMap,
          options: options,
        } as any);

        const estimatedFee = feeResult.fee;

        setBackendFeeEstimate(feeResult);
        const totals = calculateTotals(watchedRecipients, estimatedFee, availableBalance);
        setTransactionTotals(totals);
      } catch (error) {
        // Fallback to frontend calculation on error
        console.warn('Backend fee estimation failed, using frontend calculation:', error);
        const recipientCount = watchedRecipients.length || 1;
        const inputCount = coinControlSelectedCount > 0 ? coinControlSelectedCount : 2;
        const rawFee = calculateTotalFee(recipientCount, feeRate, inputCount);
        const fallbackFee = Math.max(rawFee, MIN_TX_FEE);
        const totals = calculateTotals(watchedRecipients, fallbackFee, availableBalance);
        setTransactionTotals(totals);
        setBackendFeeEstimate(null);
      }
    };

    calculateFeeFromBackend();
    // Clear form error when user changes input (they might be correcting the issue)
    setFormError(null);
  }, [watchedRecipients, feeRate, balance?.spendable, watchedSplitUTXO, watchedSplitOutputs, coinControlSelectedCount, coinControl.selectedCoins]);

  const onSubmit = async (data: SendFormData) => {
    setFormError(null); // Clear previous errors

    // Defensive guard: the Send button is already disabled when isOutOfSync is true,
    // so this path is unreachable via normal UI. It protects against programmatic
    // form submission (e.g. test automation) bypassing the disabled button state.
    if (isOutOfSync) {
      setFormError(t('send.warnings.outOfSync'));
      return;
    }

    if (!balance) {
      setFormError('Unable to verify balance. Please wait for wallet to sync.');
      return;
    }

    // Split UTXO validations (matches legacy C++ behavior from sendcoinsdialog.cpp)
    // Legacy validates own-address first (line 235-244), then multiple recipients (line 262-269)
    const isSplitUTXOEnabled = data.splitUTXO && parseInt(data.splitOutputs || '0') > 0;

    // Validation 1: Split UTXO only works when sending to your own address (legacy line 235-244)
    if (isSplitUTXOEnabled && data.recipients.length >= 1) {
      const address = data.recipients[0].address?.trim();
      if (!address) {
        setFormError('Please enter a recipient address before using Split UTXO.');
        return;
      }
      try {
        const validation = await ValidateAddress(address);
        if (!validation || validation.ismine !== true) {
          setFormError('The split block tool does not work when sending to outside addresses. Try again.');
          return;
        }
      } catch (error) {
        console.error('Failed to validate address for split UTXO:', error);
        const errorMsg = error instanceof Error ? error.message : String(error);
        if (errorMsg.includes('wallet not initialized')) {
          setFormError('Wallet is still loading. Please wait and try again.');
        } else {
          setFormError('Failed to validate address. Please try again.');
        }
        return;
      }
    }

    // Validation 2: Split UTXO does not work with multiple recipients (legacy line 262-269)
    if (isSplitUTXOEnabled && data.recipients.length > 1) {
      setFormError('The split block tool does not work with multiple addresses. Try again.');
      return;
    }

    // Use pre-calculated transactionTotals from backend fee estimation (useEffect above)
    // This ensures the fee shown to user matches the fee used for validation
    const availableBalance = balance?.spendable || 0;
    const actualTotals = transactionTotals || (() => {
      // Fallback to frontend calculation only if backend estimate not available
      const recipientCount = data.recipients.length;
      const fallbackFee = calculateTotalFee(recipientCount, feeRate);
      return calculateTotals(data.recipients, fallbackFee, availableBalance);
    })();

    if (!actualTotals.canSend) {
      const needed = actualTotals.grandTotal;
      const available = availableBalance;
      // Check if collateral is locked and provide more helpful message
      const locked = balance.locked || 0;
      if (locked > 0 && needed > available && needed <= (available + locked)) {
        // User is trying to spend locked collateral
        setFormError(
          `Insufficient available balance. You need ${formatAmountDisplay(needed)} but only have ${formatAmountDisplay(available)} available. ` +
          `${formatAmountDisplay(locked)} TWINS is locked as masternode collateral and cannot be spent.`
        );
      } else {
        setFormError(`Insufficient balance. You need ${formatAmountDisplay(needed)} but only have ${formatAmountDisplay(available)} available.`);
      }
      return;
    }

    const recipients: Recipient[] = data.recipients.map(r => ({
      address: r.address,
      amount: r.amount,
      label: r.label,
    }));

    // Include custom change address if enabled and specified
    const changeAddress = data.customChangeAddress && data.changeAddress
      ? data.changeAddress
      : undefined;

    setPendingTransaction({
      recipients,
      fee: actualTotals.estimatedFee,
      total: actualTotals.recipientsTotal,
      changeAddress,
    });
    setShowConfirmDialog(true);
  };

  const handleConfirmTransaction = async (passphrase?: string): Promise<SendTransactionResult> => {
    if (!pendingTransaction) {
      return { error: { code: 'NO_PENDING', message: 'No pending transaction' } };
    }

    let didUnlock = false;
    try {
      if (isWalletEncrypted && !passphrase) {
        return {
          error: { code: 'PASSPHRASE_REQUIRED', message: 'Passphrase is required for encrypted wallets' },
        };
      }

      // Unlock wallet if passphrase was provided
      if (passphrase) {
        // Capture fresh wallet status immediately before unlocking to avoid stale poll data
        try {
          const currentStatus = await GetWalletEncryptionStatus();
          priorWalletStatusRef.current = currentStatus?.status || '';
        } catch {
          // Keep the polled value as fallback
        }

        const unlockResult = await UnlockWallet({
          passphrase,
          timeout: 60, // Unlock for 60 seconds - enough time for the transaction
          stakingOnly: false, // Need full unlock for sending
        });

        if (!unlockResult.success) {
          return {
            error: {
              code: 'UNLOCK_FAILED',
              message: unlockResult.error || 'Failed to unlock wallet',
            },
          };
        }
        didUnlock = true;
      }

      // Build recipients map for SendTransactionMulti
      const recipients: Record<string, number> = {};
      for (const recipient of pendingTransaction.recipients) {
        const amount = parseFloat(recipient.amount);

        if (isNaN(amount) || amount <= 0) {
          return {
            error: { code: 'INVALID_AMOUNT', message: sanitizeErrorMessage('Invalid transaction amount') },
          };
        }

        recipients[recipient.address] = amount;
      }

      // Build options if any advanced features are used
      const hasSelectedUtxos = coinControl.selectedCoins.size > 0;
      const hasSplitUTXO = watchedSplitUTXO && parseInt(watchedSplitOutputs) > 0;
      const hasAdvancedOptions = pendingTransaction.changeAddress || hasSelectedUtxos || hasSplitUTXO;

      // Always pass options with feeRate; include other fields only when used
      const options = {
        changeAddress: hasAdvancedOptions ? (pendingTransaction.changeAddress || '') : '',
        selectedUtxos: hasSelectedUtxos
          ? Array.from(coinControl.selectedCoins)
          : [],
        splitCount: hasSplitUTXO ? parseInt(watchedSplitOutputs) : 0,
        feeRate: feeRate, // User-selected fee rate in TWINS/kB
      };

      // Use SendTransactionMulti to create a single transaction with all recipients
      // Cast to any to bypass Wails-generated type checking - runtime handles conversion
      const result = await SendTransactionMulti({
        recipients,
        options,
      } as any);

      // Check if backend returned an error
      if (result.error) {
        return {
          error: {
            code: result.error.code || 'SEND_ERROR',
            message: result.error.message || 'Transaction failed',
            details: result.error.details,
          },
        };
      }

      return {
        txid: result.txid || '',
      };
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : 'Unknown error occurred';
      return {
        error: { code: 'UNKNOWN_ERROR', message: sanitizeErrorMessage(errorMsg) },
      };
    } finally {
      // If we temporarily unlocked the wallet, restore it to its prior state
      if (didUnlock) {
        try {
          await restoreWalletState(priorWalletStatusRef.current);
        } catch {
          // Ignore restore errors
        }
      }
    }
  };

  const handleCloseConfirmDialog = () => {
    setShowConfirmDialog(false);
    setPendingTransaction(null);
  };

  const handleClearAll = () => {
    // Reset form to default values without triggering validation
    reset();
    // Reset non-form state
    setFeeRate(0.0001);
    setSliderPosition(0);
    setFormError(null);
    resetCoinControl();
  };

  const handleAddRecipient = () => {
    if (watchedRecipients.length >= MAX_RECIPIENTS) return;
    append({ address: '', amount: '', label: '' });
  };

  const handleUseMaximum = (recipientIndex: number) => {
    const recipientCount = watchedRecipients.length;
    // Use backend fee estimate when available, fall back to frontend approximation.
    // Apply the same input-count and MIN_TX_FEE clamp as the useEffect fallback paths
    // so the Max button is accurate even when transactionTotals is null.
    const inputCount = coinControlSelectedCount > 0 ? coinControlSelectedCount : 2;
    const rawFee = transactionTotals?.estimatedFee ?? calculateTotalFee(recipientCount, feeRate, inputCount);
    const estimatedFee = Math.max(rawFee, MIN_TX_FEE);
    // When coins are manually selected, max is capped by selected UTXO total, not full wallet balance
    const availableBalance = coinControlSelectedCount > 0
      ? coinControlSelectedAmount
      : (balance?.spendable || 0);

    const otherRecipientsSatoshis = watchedRecipients.reduce((sum, recipient, index) => {
      if (index === recipientIndex) return sum;
      const amount = parseAmount(recipient.amount || '0');
      return sum + (amount * SATOSHIS_PER_TWINS);
    }, 0);

    const otherRecipientsTotal = otherRecipientsSatoshis / SATOSHIS_PER_TWINS;

    const maxForThisRecipient = calculateMaxSendable(
      availableBalance - otherRecipientsTotal,
      estimatedFee
    );

    if (maxForThisRecipient > 0) {
      setValue(`recipients.${recipientIndex}.amount`, formatAmountForInput(maxForThisRecipient));
      trigger(`recipients.${recipientIndex}.amount`);
    }
  };

  const handleSliderChange = (position: number, rate: number) => {
    setSliderPosition(position);
    setFeeRate(rate);
  };

  const handleChooseCustomFee = () => {
    setShowCustomFeeDialog(true);
  };

  const handleCustomFeeConfirm = (customRate: number) => {
    setFeeRate(customRate);
    // Calculate slider position for the custom rate (inverse of slider calculation)
    const minRate = 0.0001;
    const maxRate = 0.001;
    const position = Math.round(((customRate - minRate) / (maxRate - minRate)) * 100);
    setSliderPosition(Math.max(0, Math.min(100, position)));
  };

  const handleAddressBookPick = (recipientIndex: number) => {
    openAddressBookPicker((address: string, label: string) => {
      setValue(`recipients.${recipientIndex}.address`, address);
      setValue(`recipients.${recipientIndex}.label`, label);
      trigger(`recipients.${recipientIndex}.address`);
    });
  };

  const handleSaveToAddressBook = async (address: string, label: string) => {
    const trimAddr = address.trim();
    const trimLabel = label.trim();
    // Check if address already exists in contacts to show a friendly message
    if (contacts.some((c) => c.address === trimAddr)) {
      addNotification({ type: 'info', title: t('send.contactExists'), message: t('send.contactExistsMessage'), duration: 3000 });
      return;
    }
    try {
      await addContact(trimLabel, trimAddr);
      await fetchContacts();
      addNotification({ type: 'success', title: t('send.contactSaved'), message: t('send.contactSavedMessage', { label: trimLabel }), duration: 3000 });
    } catch (err: any) {
      addNotification({ type: 'error', title: t('send.contactSaveFailed'), message: err?.message || String(err), duration: 5000 });
    }
  };

  // Calculate UTXO size for display (matches legacy C++ behavior)
  // Uses the "After Fee" amount from Coin Control divided by number of outputs
  const calculateUTXOSize = () => {
    if (!watchedSplitUTXO || !watchedSplitOutputs) return '0';
    const outputs = parseInt(watchedSplitOutputs) || 0;
    if (outputs === 0) return '0';

    // Get the "after fee" amount - either from coin control summary or recipient total
    let afterFeeAmount = 0;
    if (summary && summary.afterFee > 0) {
      // Use coin control summary's afterFee when coins are manually selected
      afterFeeAmount = summary.afterFee;
    } else if (transactionTotals && transactionTotals.recipientsTotal > 0) {
      // Fallback to recipient total minus fee for automatic coin selection
      afterFeeAmount = transactionTotals.recipientsTotal;
    }

    if (afterFeeAmount <= 0) return '0';

    // Calculate size per output (same as legacy: nAfterFee / nBlocks)
    const sizePerOutput = afterFeeAmount / outputs;
    return formatAmountDisplay(sizePerOutput, false);
  };

  // Disable send button only when user has entered an amount but has insufficient balance
  const isInsufficientBalance = transactionTotals !== null &&
    transactionTotals.recipientsTotal > 0 &&
    !transactionTotals.canSend;

  const isMaxRecipientsReached = watchedRecipients.length >= MAX_RECIPIENTS;

  // Block send while wallet is out of sync — displayed balance may be stale
  // Pessimistic default: treat as out-of-sync until first blockchain info arrives.
  // Also covers is_connecting (no peers yet) where is_syncing/is_out_of_sync may be false
  // but the balance is still stale.
  const isOutOfSync = blockchainInfo === null ||
    !!(blockchainInfo.is_syncing || blockchainInfo.is_out_of_sync || blockchainInfo.is_connecting);
  const isSendDisabled = isInsufficientBalance || isOutOfSync;

  return (
    <div className="qt-frame" style={{ height: '100%', display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
      <div style={{ flex: 1, overflow: 'auto', padding: '12px 16px', display: 'flex', flexDirection: 'column', gap: '12px' }}>
        <form onSubmit={handleSubmit(onSubmit)} style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
          {/* Coin Control card (gated by Expert setting) */}
          {coinControlEnabled && (
            <div style={cardStyle}>
              <SendCoinControl
                register={register}
                control={control}
                watchedCustomChangeAddress={watchedCustomChangeAddress}
                watchedSplitUTXO={watchedSplitUTXO}
                calculateUTXOSize={calculateUTXOSize}
                onOpenCoinControl={openDialog}
              />
            </div>
          )}

          {/* Recipients card */}
          <div style={cardStyle}>
            <SendRecipients
              fields={fields}
              register={register}
              watchedRecipients={watchedRecipients}
              onRemove={remove}
              onUseMaximum={handleUseMaximum}
              onAddressBookPick={handleAddressBookPick}
              onSaveToAddressBook={handleSaveToAddressBook}
              errors={errors}
            />
            <div style={{ marginTop: '12px', display: 'flex' }}>
              <PillButton
                onClick={handleAddRecipient}
                disabled={isMaxRecipientsReached}
                title={isMaxRecipientsReached ? t('send.recipients.maxReached', { max: MAX_RECIPIENTS }) : t('send.recipients.add')}
                ariaLabel={isMaxRecipientsReached ? t('send.recipients.maxReached', { max: MAX_RECIPIENTS }) : t('send.recipients.add')}
                icon={<Plus size={12} />}
                label={t('send.recipients.add')}
              />
            </div>
          </div>

          {/* Fee + Send card */}
          <div style={cardStyle}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
            <SendFeeControls
              feeRate={feeRate}
              sliderPosition={sliderPosition}
              onSliderChange={handleSliderChange}
              estimateFeeAvailable={estimateFeeAvailable}
              onChooseCustomFee={handleChooseCustomFee}
            />

            <SendTransactionTotals
              transactionTotals={transactionTotals}
              recipientCount={watchedRecipients.length}
            />

            {isOutOfSync && (
              <Banner variant="warning" message={t('send.warnings.outOfSync')} />
            )}

            {formError && (
              <Banner variant="error" message={formError} />
            )}

            <div style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              gap: '8px',
            }}>
              <div style={{ display: 'flex', gap: '8px' }}>
                <button
                  type="submit"
                  disabled={isSendDisabled}
                  style={{
                    padding: '8px 16px',
                    fontSize: '12px',
                    fontWeight: 500,
                    backgroundColor: isSendDisabled ? '#3a3a3a' : '#4a7c59',
                    border: `1px solid ${isSendDisabled ? '#444' : '#5a8c69'}`,
                    borderRadius: '6px',
                    color: isSendDisabled ? '#888' : '#fff',
                    cursor: isSendDisabled ? 'not-allowed' : 'pointer',
                    opacity: isSendDisabled ? 0.7 : 1,
                  }}
                >
                  {t('send.buttons.send').toUpperCase()}
                </button>
                <button
                  type="button"
                  onClick={handleClearAll}
                  style={{
                    padding: '8px 16px',
                    fontSize: '12px',
                    backgroundColor: '#383838',
                    border: '1px solid #4a4a4a',
                    borderRadius: '6px',
                    color: '#ccc',
                    cursor: 'pointer',
                  }}
                >
                  {t('send.buttons.clearAll')}
                </button>
              </div>

              <div style={{ fontSize: '12px', color: '#999' }}>
                {t('send.totals.balance', { amount: formatAmount(balance?.spendable || 0) })}
              </div>
            </div>
            </div>
          </div>
        </form>

        {/* Confirmation Dialog */}
        {pendingTransaction && (
          <ConfirmationDialog
            isOpen={showConfirmDialog}
            onClose={handleCloseConfirmDialog}
            onConfirm={handleConfirmTransaction}
            onSuccess={handleClearAll}
            recipients={pendingTransaction.recipients}
            fee={pendingTransaction.fee}
            total={pendingTransaction.total}
            isWalletEncrypted={isWalletEncrypted}
            isWalletStakingOnly={walletWasStakingOnly}
            coinControlSelectedCount={coinControlSelectedCount}
            coinControlSelectedAmount={coinControlSelectedAmount}
            customChangeAddress={pendingTransaction.changeAddress}
            splitEnabled={watchedSplitUTXO}
            splitCount={parseInt(watchedSplitOutputs) || 0}
            splitOutputSize={parseFloat(calculateUTXOSize().replace(/,/g, '')) || 0}
          />
        )}

        {/* Coin Control Dialog */}
        <CoinControlDialog
          recipientAmount={transactionTotals?.recipientsTotal || 0}
          feeRate={feeRate}
          recipientCount={watchedRecipients.length || 1}
        />

        {/* Custom Fee Dialog */}
        <CustomFeeDialog
          isOpen={showCustomFeeDialog}
          currentFeeRate={feeRate}
          onClose={() => setShowCustomFeeDialog(false)}
          onConfirm={handleCustomFeeConfirm}
        />
      </div>
    </div>
  );
};
