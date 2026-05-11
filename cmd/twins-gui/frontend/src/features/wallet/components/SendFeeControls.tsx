import React from 'react';
import { useTranslation } from 'react-i18next';
import { Info, Sliders } from 'lucide-react';
import { Banner } from '@/shared/components/Banner';
import { PillButton } from '@/shared/components/PillButton';

export interface SendFeeControlsProps {
  feeRate: number;
  sliderPosition: number;
  onSliderChange: (position: number, rate: number) => void;
  estimateFeeAvailable?: boolean;
  onChooseCustomFee?: () => void;
}

const SLIDER_STYLES = `
.send-fee-slider::-webkit-slider-runnable-track {
  height: 6px;
  border-radius: 6px;
  background: transparent;
}
.send-fee-slider::-webkit-slider-thumb {
  -webkit-appearance: none;
  appearance: none;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #27ae60;
  border: 2px solid #5a8c69;
  cursor: pointer;
  margin-top: -4px;
}
.send-fee-slider::-moz-range-track {
  height: 6px;
  border-radius: 6px;
  background: transparent;
}
.send-fee-slider::-moz-range-thumb {
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: #27ae60;
  border: 2px solid #5a8c69;
  cursor: pointer;
}
`;

export const SendFeeControls: React.FC<SendFeeControlsProps> = ({
  feeRate,
  sliderPosition,
  onSliderChange,
  estimateFeeAvailable = true,
  onChooseCustomFee,
}) => {
  const { t } = useTranslation('wallet');

  const handleSliderChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const position = parseInt(e.target.value);
    const minRate = 0.0001;
    const maxRate = 0.001;
    const rate = minRate + (maxRate - minRate) * (position / 100);
    onSliderChange(position, rate);
  };

  const trackBackground = `linear-gradient(to right, #5a8c69 0%, #5a8c69 ${sliderPosition}%, #3a3a3a ${sliderPosition}%, #3a3a3a 100%)`;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
      <style dangerouslySetInnerHTML={{ __html: SLIDER_STYLES }} />

      <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
        <span style={{ fontSize: '11px', color: '#888', whiteSpace: 'nowrap' }}>
          Confirmation time:
        </span>
        <span
          style={{ cursor: 'help', display: 'inline-flex', alignItems: 'center' }}
          title={estimateFeeAvailable ? t('send.fee.smartFeeTooltip') : t('send.fee.smartFeeUnavailableTooltip')}
        >
          <Info size={16} color="#aaa" />
        </span>
        <span style={{ fontSize: '11px', color: '#888' }}>normal</span>
        <input
          type="range"
          min="0"
          max="100"
          value={sliderPosition}
          onChange={handleSliderChange}
          className="send-fee-slider"
          style={{
            flex: 1,
            height: '6px',
            borderRadius: '6px',
            outline: 'none',
            cursor: 'pointer',
            appearance: 'none',
            WebkitAppearance: 'none',
            background: trackBackground,
          }}
        />
        <span style={{ fontSize: '11px', color: '#888' }}>fast</span>
      </div>

      {!estimateFeeAvailable && (
        <Banner variant="warning" message={t('send.fee.smartFeeUnavailableTooltip')} />
      )}

      <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
        <span style={{ fontSize: '11px', color: '#888' }}>Transaction Fee:</span>
        <span style={{ fontSize: '12px', color: '#ddd' }}>{feeRate.toFixed(8)} TWINS/kB</span>
        <div style={{ marginLeft: 'auto' }}>
          <PillButton
            onClick={onChooseCustomFee ?? (() => {})}
            disabled={!onChooseCustomFee}
            icon={<Sliders size={12} />}
            label="Choose..."
            title="Choose custom fee"
            ariaLabel="Choose custom fee"
          />
        </div>
      </div>
    </div>
  );
};
