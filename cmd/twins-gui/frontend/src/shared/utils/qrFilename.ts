/**
 * Builds a descriptive default filename for saving QR code images.
 *
 * Priority: label > amount > address-prefix fallback.
 * The label is sanitized for filesystem safety (non-alphanumeric replaced
 * with hyphens, collapsed, trimmed, truncated to 40 chars).
 */

const MAX_LABEL_LENGTH = 40;

/** Replace non-alphanumeric characters with hyphens, collapse runs, trim edges. */
function sanitizeForFilename(raw: string): string {
  return raw
    .replace(/[^a-zA-Z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, MAX_LABEL_LENGTH);
}

/** Format amount as a compact string (e.g. "50TWINS", "0.5TWINS"). */
function formatAmountTag(amount: number): string {
  // Strip trailing zeros: 50.00 → "50", 0.50 → "0.5"
  const formatted = parseFloat(amount.toFixed(8)).toString();
  return `${formatted}TWINS`;
}

export function buildQRFilename(
  address?: string,
  label?: string,
  amount?: number,
): string {
  const parts: string[] = ['twins-qr'];

  const sanitizedLabel = label ? sanitizeForFilename(label) : '';
  const hasLabel = sanitizedLabel.length > 0;
  const hasAmount = amount !== undefined && amount > 0;

  if (hasLabel) {
    parts.push(sanitizedLabel);
    if (hasAmount) {
      parts.push(formatAmountTag(amount));
    }
  } else if (hasAmount) {
    parts.push(formatAmountTag(amount));
    parts.push(address?.slice(0, 8) || 'qr');
  } else {
    parts.push(address?.slice(0, 8) || 'qr');
  }

  return `${parts.join('-')}.png`;
}
