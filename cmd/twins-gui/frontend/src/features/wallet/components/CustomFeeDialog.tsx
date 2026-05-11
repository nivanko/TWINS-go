import React, { useState, useEffect } from 'react';

export interface CustomFeeDialogProps {
  isOpen: boolean;
  currentFeeRate: number;
  onClose: () => void;
  onConfirm: (feeRate: number) => void;
}

export const CustomFeeDialog: React.FC<CustomFeeDialogProps> = ({
  isOpen,
  currentFeeRate,
  onClose,
  onConfirm,
}) => {
  const [inputValue, setInputValue] = useState('');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (isOpen) {
      setInputValue(currentFeeRate.toFixed(8));
      setError(null);
    }
  }, [isOpen, currentFeeRate]);

  if (!isOpen) return null;

  const handleConfirm = () => {
    const value = parseFloat(inputValue);
    if (isNaN(value) || value <= 0) {
      setError('Please enter a valid positive fee rate');
      return;
    }
    if (value < 0.00001) {
      setError('Fee rate too low (minimum: 0.00001 TWINS/kB)');
      return;
    }
    if (value > 1) {
      setError('Fee rate too high (maximum: 1 TWINS/kB)');
      return;
    }
    onConfirm(value);
    onClose();
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') handleConfirm();
    if (e.key === 'Escape') onClose();
  };

  return (
    <div
      style={{
        position: 'fixed',
        top: 0,
        left: 0,
        right: 0,
        bottom: 0,
        backgroundColor: 'rgba(0, 0, 0, 0.7)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1000,
      }}
    >
      <div
        style={{
          backgroundColor: '#2f2f2f',
          border: '1px solid #3a3a3a',
          borderRadius: '8px',
          padding: '20px',
          minWidth: '320px',
          boxShadow: '0 4px 12px rgba(0,0,0,0.5)',
        }}
      >
        <div style={{ fontSize: '13px', fontWeight: 600, color: '#ccc', marginBottom: '16px' }}>
          Custom Fee Rate
        </div>

        <div style={{ marginBottom: '12px' }}>
          <label style={{ fontSize: '11px', color: '#888', display: 'block', marginBottom: '4px' }}>
            Fee Rate (TWINS/kB):
          </label>
          <input
            type="text"
            value={inputValue}
            onChange={(e) => {
              setInputValue(e.target.value);
              setError(null);
            }}
            onKeyDown={handleKeyDown}
            autoFocus
            style={{
              width: '100%',
              padding: '7px 10px',
              backgroundColor: '#252525',
              border: error ? '1px solid #ff6666' : '1px solid #3a3a3a',
              borderRadius: '4px',
              color: '#ddd',
              fontSize: '12px',
              outline: 'none',
              boxSizing: 'border-box',
            }}
          />
          {error && (
            <div style={{ color: '#ff6666', fontSize: '11px', marginTop: '4px' }}>{error}</div>
          )}
        </div>

        <div style={{ fontSize: '11px', color: '#888', marginBottom: '16px' }}>
          Default: 0.0001 TWINS/kB (normal) to 0.001 TWINS/kB (fast)
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px' }}>
          <button
            onClick={onClose}
            style={{
              padding: '8px 16px',
              backgroundColor: '#383838',
              border: '1px solid #4a4a4a',
              borderRadius: '6px',
              color: '#ccc',
              cursor: 'pointer',
              fontSize: '12px',
            }}
          >
            Cancel
          </button>
          <button
            onClick={handleConfirm}
            style={{
              padding: '8px 16px',
              backgroundColor: '#4a7c59',
              border: '1px solid #5a8c69',
              borderRadius: '6px',
              color: '#fff',
              cursor: 'pointer',
              fontSize: '12px',
              fontWeight: 500,
            }}
          >
            OK
          </button>
        </div>
      </div>
    </div>
  );
};
