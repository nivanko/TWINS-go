import React, { useState } from 'react';
import { UseFormRegister, useWatch } from 'react-hook-form';
import type { Control } from 'react-hook-form';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { useCoinControl } from '@/store/useStore';
import { useDisplayUnits } from '@/shared/hooks/useDisplayUnits';
import { PillButton } from '@/shared/components/PillButton';

export interface SendCoinControlProps {
  register: UseFormRegister<any>;
  control: Control<any>;
  watchedCustomChangeAddress: boolean;
  watchedSplitUTXO: boolean;
  calculateUTXOSize: () => string;
  onOpenCoinControl: () => void;
}

const inputStyle: React.CSSProperties = {
  backgroundColor: '#252525',
  border: '1px solid #3a3a3a',
  borderRadius: '4px',
  padding: '7px 10px',
  fontSize: '12px',
  color: '#ddd',
  outline: 'none',
};

const checkboxLabelStyle: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: '6px',
  cursor: 'pointer',
};

const fieldLabelStyle: React.CSSProperties = { fontSize: '11px', color: '#888' };
const fieldLabelActiveStyle: React.CSSProperties = { fontSize: '11px', color: '#ddd' };

export const SendCoinControl: React.FC<SendCoinControlProps> = ({
  register,
  control,
  watchedCustomChangeAddress,
  watchedSplitUTXO,
  calculateUTXOSize,
  onOpenCoinControl,
}) => {
  const { coinControl, utxos } = useCoinControl();
  const { formatAmount } = useDisplayUnits();
  const [isExpanded, setIsExpanded] = useState(false);

  const watchedChangeAddress = useWatch({ control, name: 'changeAddress' }) as string | undefined;
  const watchedSplitOutputs = useWatch({ control, name: 'splitOutputs' }) as string | number | undefined;

  const selectedCount = coinControl.selectedCoins.size;
  const hasManualSelection = selectedCount > 0;

  const selectedAmount = hasManualSelection
    ? utxos
        .filter((utxo) => coinControl.selectedCoins.has(`${utxo.txid}:${utxo.vout}`))
        .reduce((sum, utxo) => sum + utxo.amount, 0)
    : 0;

  const customChangeActive =
    !!watchedCustomChangeAddress &&
    typeof watchedChangeAddress === 'string' &&
    watchedChangeAddress.trim() !== '';

  const summaryParts: string[] = [];
  if (hasManualSelection) {
    summaryParts.push(`Coins: ${selectedCount} sel, ${formatAmount(selectedAmount)}`);
  }
  if (customChangeActive) {
    summaryParts.push('Custom change');
  }
  if (watchedSplitUTXO) {
    const splitN = parseInt(String(watchedSplitOutputs ?? ''), 10);
    if (splitN > 0) {
      summaryParts.push(`Split ${splitN}×`);
    }
  }
  const summaryText = summaryParts.length > 0 ? summaryParts.join(' · ') : 'Auto';
  const summaryColor = summaryParts.length > 0 ? '#ff9966' : '#aaa';

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
      <div
        role="button"
        tabIndex={0}
        aria-expanded={isExpanded}
        aria-label={isExpanded ? 'Collapse Coin Control' : 'Expand Coin Control'}
        onClick={() => setIsExpanded((v) => !v)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            setIsExpanded((v) => !v);
          }
        }}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '8px',
          cursor: 'pointer',
          userSelect: 'none',
        }}
      >
        {isExpanded ? (
          <ChevronDown size={14} color="#888" />
        ) : (
          <ChevronRight size={14} color="#888" />
        )}
        <span style={{ fontSize: '13px', fontWeight: 600, color: '#ccc' }}>
          Coin Control Features
        </span>
        <span style={{ fontSize: '11px', color: summaryColor }}>{summaryText}</span>
        <div style={{ flex: 1 }} />
        <div
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
        >
          <PillButton
            onClick={onOpenCoinControl}
            icon={null}
            label="Open Coin Control..."
            title="Open Coin Control"
            ariaLabel="Open Coin Control"
          />
        </div>
      </div>

      {isExpanded && (
        <div
          style={{
            display: 'flex',
            flexDirection: 'row',
            gap: '24px',
            flexWrap: 'wrap',
            alignItems: 'center',
          }}
        >
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '12px',
              flex: '1 1 480px',
              minWidth: 0,
            }}
          >
            <label style={{ ...checkboxLabelStyle, flexShrink: 0 }}>
              <input
                type="checkbox"
                {...register('customChangeAddress')}
                style={{ width: '13px', height: '13px' }}
              />
              <span style={watchedCustomChangeAddress ? fieldLabelActiveStyle : fieldLabelStyle}>
                Custom change address
              </span>
            </label>

            <input
              type="text"
              {...register('changeAddress')}
              placeholder="Enter a TWINS address (e.g. WJmGqDGiE5sGJxHwvW4sSodfnGMgQ9XFb)"
              disabled={!watchedCustomChangeAddress}
              style={{
                ...inputStyle,
                flex: 1,
                minWidth: 0,
                cursor: watchedCustomChangeAddress ? 'text' : 'not-allowed',
              }}
            />
          </div>

          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '12px',
              flexShrink: 0,
              flexWrap: 'wrap',
            }}
          >
            <label style={{ ...checkboxLabelStyle, flexShrink: 0 }}>
              <input
                type="checkbox"
                {...register('splitUTXO')}
                style={{ width: '13px', height: '13px' }}
              />
              <span style={watchedSplitUTXO ? fieldLabelActiveStyle : fieldLabelStyle}>Split UTXO</span>
            </label>

            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
                flexShrink: 0,
                flexWrap: 'wrap',
              }}
            >
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '6px',
                }}
              >
                <span style={watchedSplitUTXO ? fieldLabelActiveStyle : fieldLabelStyle}># of outputs</span>
                <input
                  type="text"
                  {...register('splitOutputs')}
                  disabled={!watchedSplitUTXO}
                  style={{
                    ...inputStyle,
                    width: '60px',
                    cursor: watchedSplitUTXO ? 'text' : 'not-allowed',
                  }}
                />
              </div>

              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '6px',
                }}
              >
                <span style={watchedSplitUTXO ? fieldLabelActiveStyle : fieldLabelStyle}>UTXO Size:</span>
                <span style={watchedSplitUTXO ? fieldLabelActiveStyle : fieldLabelStyle}>
                  {watchedSplitUTXO ? calculateUTXOSize() : '0'} TWINS
                </span>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
