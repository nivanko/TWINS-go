import React from 'react';
import { formatAmountDisplay } from '@/utils/amountValidation';
import { Banner } from '@/shared/components/Banner';

interface TransactionTotals {
  recipientsTotal: number;
  estimatedFee: number;
  grandTotal: number;
  remainingBalance: number;
  canSend: boolean;
}

export interface SendTransactionTotalsProps {
  transactionTotals: TransactionTotals | null;
  recipientCount: number;
}

const labelStyle: React.CSSProperties = { fontSize: '11px', color: '#888' };
const valueStyle: React.CSSProperties = { fontSize: '12px', color: '#ddd' };
const rowStyle: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'center',
};

export const SendTransactionTotals: React.FC<SendTransactionTotalsProps> = ({
  transactionTotals,
  recipientCount,
}) => {
  return (
    <>
      {recipientCount > 1 && (
        <Banner
          variant="info"
          message={`Sending to ${recipientCount} recipients in a single transaction.`}
        />
      )}

      {transactionTotals && transactionTotals.recipientsTotal > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
          <div style={rowStyle}>
            <span style={labelStyle}>Recipients Total:</span>
            <span style={valueStyle}>{formatAmountDisplay(transactionTotals.recipientsTotal)}</span>
          </div>
          <div style={rowStyle}>
            <span style={labelStyle}>Estimated Fee:</span>
            <span style={valueStyle}>{formatAmountDisplay(transactionTotals.estimatedFee)}</span>
          </div>
          <div style={{ borderTop: '1px solid #3a3a3a', margin: '4px 0' }} />
          <div style={rowStyle}>
            <span style={{ fontSize: '12px', fontWeight: 600, color: '#ddd' }}>Grand Total:</span>
            <span
              style={{
                fontSize: '12px',
                fontWeight: 600,
                color: transactionTotals.canSend ? '#27ae60' : '#ff6666',
              }}
            >
              {formatAmountDisplay(transactionTotals.grandTotal)}
            </span>
          </div>
          <div style={rowStyle}>
            <span style={labelStyle}>Remaining Balance:</span>
            <span style={labelStyle}>{formatAmountDisplay(transactionTotals.remainingBalance)}</span>
          </div>
          {!transactionTotals.canSend && (
            <Banner variant="error" message="Insufficient balance for this transaction" />
          )}
        </div>
      )}
    </>
  );
};
