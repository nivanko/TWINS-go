// Formatting utilities for the TWINS wallet

// Display unit constants matching backend preferences.DisplayUnit*
export const DISPLAY_UNIT_TWINS = 0;
export const DISPLAY_UNIT_MTWINS = 1;
export const DISPLAY_UNIT_UTWINS = 2;

/**
 * Convert an amount from base TWINS to the selected display unit.
 * 1 TWINS = 1,000 mTWINS = 1,000,000 uTWINS
 */
export function convertToDisplayUnit(amount: number, displayUnit: number): number {
  switch (displayUnit) {
    case DISPLAY_UNIT_MTWINS:
      return amount * 1000;
    case DISPLAY_UNIT_UTWINS:
      return amount * 1000000;
    default:
      return amount;
  }
}

/**
 * Get the label string for a display unit
 */
export function getUnitLabel(displayUnit: number): string {
  switch (displayUnit) {
    case DISPLAY_UNIT_MTWINS:
      return 'mTWINS';
    case DISPLAY_UNIT_UTWINS:
      return 'µTWINS';
    default:
      return 'TWINS';
  }
}

/**
 * Add thousands separators to an integer string without regex backtracking.
 */
export function addThousandsSeparators(intStr: string): string {
  const start = intStr.startsWith('-') ? 1 : 0;
  const digits = intStr.length - start;
  if (digits <= 3) return intStr;
  const parts: string[] = [];
  const firstGroupLen = ((digits - 1) % 3) + 1;
  parts.push(intStr.slice(start, start + firstGroupLen));
  for (let i = start + firstGroupLen; i < intStr.length; i += 3) {
    parts.push(intStr.slice(i, i + 3));
  }
  return (start === 1 ? '-' : '') + parts.join(',');
}

/**
 * Format a balance amount with proper decimal places and thousands separators
 * @param amount - The amount to format
 * @param decimals - Number of decimal places (default: 8)
 * @param includeUnit - Whether to include "TWINS" suffix (default: true)
 */
export function formatBalance(
  amount: number,
  decimals: number = 8,
  includeUnit: boolean = true
): string {
  // Format with fixed decimals
  const formatted = amount.toFixed(decimals);

  // Split into integer and decimal parts
  const [integer, decimal] = formatted.split('.');

  // Add thousands separators to integer part
  const withSeparators = addThousandsSeparators(integer);

  // Combine parts
  const result = decimal ? `${withSeparators}.${decimal}` : withSeparators;

  return includeUnit ? `${result} TWINS` : result;
}

/**
 * Format a percentage value
 * @param percentage - The percentage value (0-100)
 * @param decimals - Number of decimal places (default: 2)
 */
export function formatPercentage(percentage: number, decimals: number = 2): string {
  return `${percentage.toFixed(decimals)} %`;
}

/**
 * Format a date for transaction display (matches Qt wallet format)
 * @param date - The date to format
 */
export function formatTransactionDate(date: Date | string): string {
  const d = typeof date === 'string' ? new Date(date) : date;

  // Format as: MM/DD/YY HH:MM
  const month = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  const year = String(d.getFullYear()).slice(-2);
  const hours = String(d.getHours()).padStart(2, '0');
  const minutes = String(d.getMinutes()).padStart(2, '0');

  return `${month}/${day}/${year} ${hours}:${minutes}`;
}

/**
 * Truncate an address for display by keeping the beginning and end with an ellipsis in the middle.
 * @param address - The full address
 * @param startChars - Number of leading characters to keep (default: 10)
 * @param endChars - Number of trailing characters to keep (default: 10)
 */
export function truncateAddress(address: string, startChars: number = 10, endChars: number = 10): string {
  if (address.length <= startChars + endChars + 3) return address;
  return `${address.slice(0, startChars)}...${address.slice(-endChars)}`;
}

/**
 * Get color for transaction amount (green for positive, red for negative)
 * @param amount - The transaction amount
 */
export function getTransactionColor(amount: number): string {
  if (amount > 0) return '#00ff00'; // Green for incoming
  if (amount < 0) return '#ff0000'; // Red for outgoing
  return '#ffffff'; // White for zero
}

/**
 * Format byte count to human-readable string (B, KB, MB, GB)
 */
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1073741824) return `${(bytes / 1048576).toFixed(1)} MB`;
  return `${(bytes / 1073741824).toFixed(2)} GB`;
}

/**
 * Format bytes-per-second rate with adaptive units (B/s, KB/s, MB/s)
 */
export function formatRate(bytesPerSec: number): string {
  if (bytesPerSec < 1024) return `${bytesPerSec.toFixed(0)} B/s`;
  if (bytesPerSec < 1048576) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`;
  return `${(bytesPerSec / 1048576).toFixed(2)} MB/s`;
}
