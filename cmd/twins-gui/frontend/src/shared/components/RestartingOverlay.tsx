import React from 'react';
import { createPortal } from 'react-dom';
import { RefreshCw } from 'lucide-react';

interface RestartingOverlayProps {
  message?: string;
}

/**
 * Full-screen dark overlay with spinning icon shown during app restart.
 * Used by OptionsDialog (settings restart) and WalletRepairTab (repair restart).
 */
export const RestartingOverlay: React.FC<RestartingOverlayProps> = ({
  message = 'Restarting application...',
}) =>
  // Portal to document.body so the overlay covers the entire viewport.
  // Without this, parent elements with CSS `transform` (e.g. dialog centering)
  // create a new containing block that constrains position:fixed to the dialog.
  createPortal(
    <div
      style={{
        position: 'fixed',
        inset: 0,
        backgroundColor: 'rgba(0, 0, 0, 0.85)',
        zIndex: 1020,
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        alignItems: 'center',
        gap: '16px',
      }}
    >
      <RefreshCw
        size={32}
        style={{
          color: '#4a9eff',
          animation: 'spin 1s linear infinite',
        }}
      />
      <span style={{ color: '#fff', fontSize: '16px', fontWeight: 500 }}>
        {message}
      </span>
    </div>,
    document.body,
  );
