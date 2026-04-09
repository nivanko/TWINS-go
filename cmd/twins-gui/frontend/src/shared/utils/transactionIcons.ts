/**
 * Transaction Icon Mapping Utilities
 * Maps transaction types and statuses to their corresponding icons
 * Based on Qt wallet implementation in transactiontablemodel.cpp
 */

import { convertToDisplayUnit, getUnitLabel } from '@/shared/utils/format';

// Import all transaction icons
import txMinedIcon from '@/assets/icons/transactions/tx_mined.png';
import txInputIcon from '@/assets/icons/transactions/tx_input.png';
import txOutputIcon from '@/assets/icons/transactions/tx_output.png';
import txInOutIcon from '@/assets/icons/transactions/tx_inout.png';

/**
 * Transaction status enum matching Qt implementation
 */
export enum TransactionStatus {
  Confirmed = 'confirmed',
  Unconfirmed = 'unconfirmed',
  Confirming = 'confirming',
  Conflicted = 'conflicted',
  Immature = 'immature',
  MaturesWarning = 'matures_warning',
  NotAccepted = 'not_accepted',
}

/**
 * Get the transaction type icon based on transaction type
 * Maps to Qt's txAddressDecoration() logic
 */
export function getTransactionTypeIcon(type: string): string {
  switch (type) {
    // Mining and staking rewards use pickaxe icon
    case 'generated':
    case 'stake':
    case 'masternode':
      return txMinedIcon;

    // Receive transactions use green arrow down
    case 'receive':
    case 'receive_from_other':
    case 'receive_with_obfuscation':
      return txInputIcon;

    // Send transactions use red arrow up
    case 'send':
    case 'send_to_other':
      return txOutputIcon;

    // UTXO consolidation uses bidirectional arrows
    case 'consolidation':

    // Self-transfers and other types use bidirectional arrows
    case 'send_to_self':
    case 'obfuscation_denominate':
    case 'obfuscation_collateral_payment':
    case 'obfuscation_make_collaterals':
    case 'obfuscation_create_denominations':
    case 'obfuscated':
    case 'other':
    default:
      return txInOutIcon;
  }
}

/**
 * Determine transaction status from confirmations
 */
export function getTransactionStatus(confirmations: number, isConflicted: boolean = false): TransactionStatus {
  if (isConflicted) {
    return TransactionStatus.Conflicted;
  }

  if (confirmations === 0) {
    return TransactionStatus.Unconfirmed;
  }

  if (confirmations >= 1 && confirmations < 6) {
    return TransactionStatus.Confirming;
  }

  return TransactionStatus.Confirmed;
}

/**
 * Format amount for display with brackets if unconfirmed
 * Matches Qt's formatTxAmount() behavior
 */
export function formatTransactionAmount(
  amount: number,
  confirmations: number,
  displayUnit: number = 0,
  displayDigits: number = 8
): string {
  const formattedAmount = formatAmount(amount, displayUnit, displayDigits);

  // Wrap unconfirmed amounts in brackets
  if (confirmations === 0) {
    return `[${formattedAmount}]`;
  }

  return formattedAmount;
}

/**
 * Format amount with sign, decimal places, and unit label
 */
export function formatAmount(
  amount: number,
  displayUnit: number = 0,
  displayDigits: number = 8
): string {
  const converted = convertToDisplayUnit(amount, displayUnit);
  const unitLabel = getUnitLabel(displayUnit);
  const sign = converted >= 0 ? '+' : '';
  return `${sign}${converted.toFixed(displayDigits)} ${unitLabel}`;
}

/**
 * Get text color class for amount based on value
 */
export function getAmountColorClass(amount: number): string {
  return amount < 0 ? 'text-red-500' : 'text-white';
}

/**
 * Format transaction date/time with timezone abbreviation
 * Shows local time with timezone indicator for clarity
 * Example: "Nov 03, 2025 14:30 PST"
 */
export function formatTransactionDate(timestamp: number | string | Date): string {
  const date = new Date(timestamp);

  // Format: "Nov 03, 2025 14:30 PST"
  const options: Intl.DateTimeFormatOptions = {
    month: 'short',
    day: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
    timeZoneName: 'short',
  };

  return date.toLocaleString('en-US', options);
}

/**
 * Format transaction date/time in UTC for tooltips
 * Provides unambiguous time reference
 * Example: "Nov 03, 2025 22:30 UTC"
 */
export function formatTransactionDateUTC(timestamp: number | string | Date): string {
  const date = new Date(timestamp);

  // Format in UTC: "Nov 03, 2025 22:30 UTC"
  const options: Intl.DateTimeFormatOptions = {
    month: 'short',
    day: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
    timeZone: 'UTC',
    timeZoneName: 'short',
  };

  return date.toLocaleString('en-US', options);
}

/**
 * Get human-readable transaction type label
 */
export function getTransactionTypeLabel(type: string): string {
  // Labels match legacy C++ Qt wallet (transactionrecord.cpp)
  const labels: Record<string, string> = {
    'generated': 'Mined',
    'stake': 'TWINS Stake',
    'masternode': 'Masternode Reward',
    'send': 'Sent to',
    'send_to_other': 'Sent to',
    'send_to_self': 'Payment to yourself',
    'consolidation': 'UTXO Consolidation',
    'receive': 'Received with',
    'receive_from_other': 'Received with',
    'receive_with_obfuscation': 'Obfuscation',
    'obfuscation_denominate': 'Obfuscation Denominate',
    'obfuscation_collateral_payment': 'Obfuscation Collateral Payment',
    'obfuscation_make_collaterals': 'Obfuscation Make Collaterals',
    'obfuscation_create_denominations': 'Obfuscation Create Denominations',
    'obfuscated': 'Obfuscated',
    'other': 'Other',
  };

  return labels[type] || 'Unknown';
}