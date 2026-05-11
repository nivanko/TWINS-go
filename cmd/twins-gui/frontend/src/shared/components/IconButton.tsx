import React from 'react';

export interface IconButtonProps {
  onClick: () => void;
  title: string;
  ariaLabel: string;
  icon: React.ReactNode;
  disabled?: boolean;
  size?: 24 | 26;
  variant?: 'ghost' | 'danger';
}

export const IconButton: React.FC<IconButtonProps> = ({
  onClick,
  title,
  ariaLabel,
  icon,
  disabled,
  size = 24,
  variant = 'ghost',
}) => {
  const hoverColor = variant === 'danger' ? '#ff6666' : '#ddd';
  return (
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
        width: `${size}px`,
        height: `${size}px`,
        background: 'none',
        border: '1px solid #3a3a3a',
        borderRadius: '4px',
        color: '#888',
        cursor: disabled ? 'not-allowed' : 'pointer',
        flexShrink: 0,
        opacity: disabled ? 0.5 : 1,
        transition: 'color 0.15s, border-color 0.15s',
      }}
      onMouseEnter={(e) => {
        if (disabled) return;
        e.currentTarget.style.color = hoverColor;
        e.currentTarget.style.borderColor = '#555';
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.color = '#888';
        e.currentTarget.style.borderColor = '#3a3a3a';
      }}
    >
      {icon}
    </button>
  );
};
