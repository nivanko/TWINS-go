import { describe, it, expect, vi, afterEach } from 'vitest';
import { writeToClipboard } from '../clipboard';

describe('writeToClipboard', () => {
  // Save original navigator.clipboard and document.execCommand so we can restore them
  const originalClipboard = (navigator as Navigator & { clipboard?: Clipboard }).clipboard;
  const originalExecCommand = (document as Document & { execCommand?: (cmd: string) => boolean }).execCommand;

  afterEach(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: originalClipboard,
      writable: true,
      configurable: true,
    });
    Object.defineProperty(document, 'execCommand', {
      value: originalExecCommand,
      writable: true,
      configurable: true,
    });
    vi.restoreAllMocks();
  });

  it('returns true when navigator.clipboard.writeText succeeds', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      writable: true,
      configurable: true,
    });

    const result = await writeToClipboard('hello');

    expect(result).toBe(true);
    expect(writeText).toHaveBeenCalledWith('hello');
  });

  it('falls back to execCommand when clipboard.writeText rejects', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('not allowed'));
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      writable: true,
      configurable: true,
    });
    const execCommand = vi.fn().mockReturnValue(true);
    Object.defineProperty(document, 'execCommand', {
      value: execCommand,
      writable: true,
      configurable: true,
    });

    const result = await writeToClipboard('fallback text');

    expect(result).toBe(true);
    expect(writeText).toHaveBeenCalledWith('fallback text');
    expect(execCommand).toHaveBeenCalledWith('copy');
  });

  it('returns false when both clipboard API and execCommand fail', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('not allowed'));
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      writable: true,
      configurable: true,
    });
    const execCommand = vi.fn().mockImplementation(() => {
      throw new Error('execCommand failed');
    });
    Object.defineProperty(document, 'execCommand', {
      value: execCommand,
      writable: true,
      configurable: true,
    });

    const result = await writeToClipboard('text');

    expect(result).toBe(false);
  });

  it('handles empty string', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      writable: true,
      configurable: true,
    });

    const result = await writeToClipboard('');

    expect(result).toBe(true);
    expect(writeText).toHaveBeenCalledWith('');
  });
});
