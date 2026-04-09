import { describe, it, expect } from 'vitest';
import { buildTwinsURI, MAX_QR_DATA_LENGTH } from '../twinsUri';

describe('buildTwinsURI', () => {
  it('returns plain address URI when no optional params provided', () => {
    expect(buildTwinsURI('TW1abc123')).toBe('twins:TW1abc123');
  });

  it('appends amount parameter', () => {
    expect(buildTwinsURI('TW1abc123', 1.5)).toBe('twins:TW1abc123?amount=1.5');
  });

  it('appends label parameter with URL encoding', () => {
    expect(buildTwinsURI('TW1abc123', undefined, 'Invoice #42')).toBe(
      'twins:TW1abc123?label=Invoice%20%2342'
    );
  });

  it('appends message parameter with URL encoding', () => {
    expect(buildTwinsURI('TW1abc123', undefined, undefined, 'Pay me')).toBe(
      'twins:TW1abc123?message=Pay%20me'
    );
  });

  it('combines all parameters', () => {
    const uri = buildTwinsURI('TW1abc123', 100, 'Test', 'Hello');
    expect(uri).toBe('twins:TW1abc123?amount=100&label=Test&message=Hello');
  });

  it('skips zero amount', () => {
    expect(buildTwinsURI('TW1abc123', 0)).toBe('twins:TW1abc123');
  });

  it('skips negative amount', () => {
    expect(buildTwinsURI('TW1abc123', -5)).toBe('twins:TW1abc123');
  });

  it('skips empty label', () => {
    expect(buildTwinsURI('TW1abc123', undefined, '')).toBe('twins:TW1abc123');
  });

  it('skips empty message', () => {
    expect(buildTwinsURI('TW1abc123', undefined, undefined, '')).toBe('twins:TW1abc123');
  });
});

describe('MAX_QR_DATA_LENGTH', () => {
  it('is 2000 characters', () => {
    expect(MAX_QR_DATA_LENGTH).toBe(2000);
  });
});
