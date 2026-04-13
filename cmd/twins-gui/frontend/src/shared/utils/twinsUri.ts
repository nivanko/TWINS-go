/**
 * Build a twins: URI from payment request data.
 * Follows BIP21-style URI format: twins:<address>[?amount=<amount>&label=<label>&message=<message>]
 */
export function buildTwinsURI(address: string, amount?: number, label?: string, message?: string): string {
  let uri = `twins:${address}`;
  const params: string[] = [];

  if (amount && amount > 0) {
    params.push(`amount=${amount}`);
  }
  if (label) {
    params.push(`label=${encodeURIComponent(label)}`);
  }
  if (message) {
    params.push(`message=${encodeURIComponent(message)}`);
  }

  if (params.length > 0) {
    uri += '?' + params.join('&');
  }

  return uri;
}

/** Maximum QR code data length before warning.
 *  The QR canvas is 200px with Level H error correction and a 76×76px logo overlay.
 *  At Level H, modules become too small to scan reliably beyond ~350 characters. */
export const MAX_QR_DATA_LENGTH = 350;
