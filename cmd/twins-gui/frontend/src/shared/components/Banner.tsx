import React from 'react';

export type BannerVariant = 'warning' | 'error' | 'info';

export interface BannerProps {
  variant: BannerVariant;
  message: string;
  role?: 'alert' | 'status';
}

const VARIANT_PALETTES: Record<
  BannerVariant,
  { backgroundColor: string; borderColor: string; color: string }
> = {
  warning: { backgroundColor: '#4a3a2a', borderColor: '#ff9966', color: '#ff9966' },
  error: { backgroundColor: '#4a2a2a', borderColor: '#ff6666', color: '#ff6666' },
  info: { backgroundColor: '#2a3a4a', borderColor: '#6699cc', color: '#6699cc' },
};

export const Banner: React.FC<BannerProps> = ({ variant, message, role }) => {
  const palette = VARIANT_PALETTES[variant];
  const effectiveRole = role ?? (variant === 'error' ? 'alert' : 'status');
  return (
    <div
      role={effectiveRole}
      style={{
        padding: '6px 10px',
        backgroundColor: palette.backgroundColor,
        border: `1px solid ${palette.borderColor}`,
        borderRadius: '6px',
        color: palette.color,
        fontSize: '11px',
        textAlign: 'center',
      }}
    >
      {message}
    </div>
  );
};
