import React from 'react';
import { useTranslation } from 'react-i18next';
import { core } from '@/shared/types/wallet.types';
import { useDisplayUnits } from '@/shared/hooks/useDisplayUnits';

interface TWINSBalanceCardProps {
  balance: core.Balance;
  showWatchOnly?: boolean;
  hideZeroBalances?: boolean;
  isLoading?: boolean;
}

// Balance row rendered as a table row (tr).
// Parent table auto-sizes both columns to content width,
// keeping values tight to labels with right-aligned monospace numbers.
const BalanceRow: React.FC<{
  label: string;
  value: number;
  isLoading: boolean;
  hideIfZero?: boolean;
}> = ({ label, value, isLoading, hideIfZero = false }) => {
  const { formatAmount } = useDisplayUnits();

  if (hideIfZero && value === 0) {
    return null;
  }

  return (
    <tr>
      <td style={{ fontSize: '13px', color: '#999', paddingRight: '12px', paddingBottom: '4px', whiteSpace: 'nowrap' }}>
        {label}:
      </td>
      <td style={{ fontSize: '13px', fontWeight: 'bold', textAlign: 'right', paddingBottom: '4px', whiteSpace: 'nowrap' }}>
        {isLoading ? (
          <div className="loading-skeleton" style={{ width: '150px', height: '14px' }} />
        ) : (
          formatAmount(value)
        )}
      </td>
    </tr>
  );
};

export const TWINSBalanceCard: React.FC<TWINSBalanceCardProps> = ({
  balance,
  hideZeroBalances = false,
  isLoading = false
}) => {
  const { t } = useTranslation('wallet');

  return (
    <div className="qt-frame-secondary" style={{ padding: '0', marginBottom: '20px', marginTop: '15px' }}>
      <div className="qt-hbox" style={{ alignItems: 'baseline', marginBottom: '10px' }}>
        <div className="qt-label" style={{ fontSize: '13px', fontWeight: 'normal' }}>
          {t('balance.twins')}
        </div>
      </div>

      <table style={{ marginTop: '10px', borderCollapse: 'collapse' }}>
        <tbody>
          <BalanceRow
            label={t('balance.available')}
            value={balance.available}
            isLoading={isLoading}
          />
          <BalanceRow
            label={t('balance.pending')}
            value={balance.pending}
            isLoading={isLoading}
            hideIfZero={hideZeroBalances}
          />
          <BalanceRow
            label={t('balance.immature')}
            value={balance.immature}
            isLoading={isLoading}
            hideIfZero={hideZeroBalances}
          />
          <BalanceRow
            label={t('balance.locked')}
            value={balance.locked}
            isLoading={isLoading}
            hideIfZero={hideZeroBalances}
          />
          <BalanceRow
            label={t('balance.total')}
            value={balance.total}
            isLoading={isLoading}
          />
        </tbody>
      </table>
    </div>
  );
};
