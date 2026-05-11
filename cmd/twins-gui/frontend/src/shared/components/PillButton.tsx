import React from 'react';

export interface PillButtonProps {
  onClick: () => void;
  title: string;
  ariaLabel: string;
  icon: React.ReactNode;
  label: string;
  disabled?: boolean;
  cursor?: React.CSSProperties['cursor'];
}

export const PillButton: React.FC<PillButtonProps> = ({
  onClick,
  title,
  ariaLabel,
  icon,
  label,
  disabled,
  cursor,
}) => (
  <button
    type="button"
    onClick={onClick}
    disabled={disabled}
    title={title}
    aria-label={ariaLabel}
    style={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      gap: '6px',
      padding: '6px 14px',
      fontSize: '11px',
      fontWeight: 500,
      backgroundColor: 'transparent',
      border: '1px solid #4a4a4a',
      borderRadius: '999px',
      color: '#ccc',
      cursor: disabled ? 'not-allowed' : (cursor ?? 'pointer'),
      opacity: disabled ? 0.5 : 1,
      transition: 'background-color 0.15s, border-color 0.15s, color 0.15s',
    }}
    onMouseEnter={(e) => {
      if (disabled) return;
      e.currentTarget.style.backgroundColor = '#383838';
      e.currentTarget.style.borderColor = '#5a5a5a';
      e.currentTarget.style.color = '#fff';
    }}
    onMouseLeave={(e) => {
      e.currentTarget.style.backgroundColor = 'transparent';
      e.currentTarget.style.borderColor = '#4a4a4a';
      e.currentTarget.style.color = '#ccc';
    }}
  >
    {icon}
    {label}
  </button>
);
