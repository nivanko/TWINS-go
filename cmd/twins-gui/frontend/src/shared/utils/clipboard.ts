/**
 * Write text to the clipboard with a fallback for older browsers / restricted contexts.
 *
 * Tries `navigator.clipboard.writeText` first, then falls back to a hidden
 * `<textarea>` + `document.execCommand('copy')` so the GUI keeps working
 * even when the modern Clipboard API is unavailable (e.g. some embedded
 * webviews or insecure contexts).
 *
 * @returns `true` if the write succeeded, `false` otherwise.
 */
export async function writeToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    try {
      const textArea = document.createElement('textarea');
      textArea.value = text;
      textArea.style.position = 'fixed';
      textArea.style.opacity = '0';
      document.body.appendChild(textArea);
      textArea.select();
      document.execCommand('copy');
      document.body.removeChild(textArea);
      return true;
    } catch {
      return false;
    }
  }
}
