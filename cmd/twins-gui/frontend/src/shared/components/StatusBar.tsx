import React, { useState, useRef, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { Lock, Unlock, LockOpen } from 'lucide-react';
import { getUnitLabel, DISPLAY_UNIT_TWINS, DISPLAY_UNIT_MTWINS, DISPLAY_UNIT_UTWINS } from '@/shared/utils/format';
import '@/styles/qt-theme.css';

const UNIT_OPTIONS = [
  { value: DISPLAY_UNIT_TWINS, label: 'TWINS' },
  { value: DISPLAY_UNIT_MTWINS, label: 'mTWINS' },
  { value: DISPLAY_UNIT_UTWINS, label: 'µTWINS' },
];

interface StatusBarProps {
  isConnected?: boolean;
  connections?: number;
  syncProgress?: number;
  blockHeight?: number;
  behindText?: string;
  isStaking?: boolean;
  isEncrypted?: boolean;
  isLocked?: boolean;
  isStakingOnly?: boolean;
  isHD?: boolean;
  isTor?: boolean;
  displayUnit?: number;
  /** Called when the lock icon is clicked (encrypted wallets only) */
  onLockClick?: (e: React.MouseEvent) => void;
  /** Called when the "not encrypted" area is clicked (unencrypted wallets only) */
  onEncryptClick?: () => void;
  /** Called when the peers count indicator is clicked */
  onPeersClick?: () => void;
  /** Called when the user selects a different display unit */
  onUnitChange?: (unit: number) => void;
}

export const StatusBar: React.FC<StatusBarProps> = ({
  isConnected = false,
  connections = 0,
  syncProgress = 0,
  blockHeight = 1545297,
  behindText = '30 weeks behind',
  isStaking = false,
  isEncrypted = false,
  isLocked = true,
  isStakingOnly = false,
  isHD = true,
  isTor = false,
  displayUnit = 0,
  onLockClick,
  onEncryptClick,
  onPeersClick,
  onUnitChange,
}) => {
  const { t } = useTranslation('common');
  const [unitDropdownOpen, setUnitDropdownOpen] = useState(false);
  const unitDropdownRef = useRef<HTMLDivElement>(null);

  // Close unit dropdown on click outside
  useEffect(() => {
    if (unitDropdownOpen) {
      const handleClickOutside = (e: MouseEvent) => {
        if (unitDropdownRef.current && !unitDropdownRef.current.contains(e.target as Node)) {
          setUnitDropdownOpen(false);
        }
      };
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [unitDropdownOpen]);

  // Determine which connection icon to use based on connection count
  const getConnectionIcon = () => {
    if (!isConnected || connections === 0) return '/icons/status/connect0_16.png';
    if (connections <= 2) return '/icons/status/connect1_16.png';
    if (connections <= 4) return '/icons/status/connect2_16.png';
    if (connections <= 6) return '/icons/status/connect3_16.png';
    return '/icons/status/connect4_16.png';
  };

  // Get sync status icon
  const getSyncIcon = () => {
    if (syncProgress >= 100) return '/icons/status/synced.png';
    if (syncProgress >= 80) return '/icons/status/clock5.png';
    if (syncProgress >= 60) return '/icons/status/clock4.png';
    if (syncProgress >= 40) return '/icons/status/clock3.png';
    if (syncProgress >= 20) return '/icons/status/clock2.png';
    return '/icons/status/clock1.png';
  };
  return (
    <div
      className="flex items-center justify-between"
      style={{
        height: 'var(--qt-statusbar-height)',
        backgroundColor: 'var(--qt-status-bar-bg)',
        borderTop: '1px solid rgba(255, 255, 255, 0.1)',
        padding: '0 12px',
        fontSize: '12px',
        color: 'var(--qt-text-secondary)',
        position: 'fixed',
        bottom: 0,
        left: 0,
        right: 0,
        zIndex: 50,
      }}
    >
      {/* Left side - Sync status */}
      <div className="flex items-center gap-3">
        {syncProgress < 100 ? (
          <>
            <div className="flex items-center gap-2">
              <img
                src={getSyncIcon()}
                alt="Sync status"
                style={{ width: '16px', height: '16px' }}
              />
              <span>{t('statusBar.synchronizing')}</span>
            </div>
            <div
              className="flex items-center"
              style={{
                backgroundColor: 'var(--qt-bg-secondary)',
                padding: '2px 8px',
                borderRadius: '3px',
              }}
            >
              <span>{behindText}</span>
            </div>
            <span>{t('statusBar.scanningBlock', { height: blockHeight.toLocaleString() })}</span>
          </>
        ) : (
          <div className="flex items-center gap-2">
            <img
              src="/icons/status/synced.png"
              alt={t('status.synced')}
              style={{ width: '16px', height: '16px' }}
            />
            <span>{t('statusBar.upToDate')}</span>
          </div>
        )}
      </div>

      {/* Right side - Network icons */}
      <div className="flex items-center gap-4">
        {/* Unit display selector */}
        <div style={{ position: 'relative' }} ref={unitDropdownRef}>
          <button
            onClick={() => setUnitDropdownOpen(!unitDropdownOpen)}
            title={t('statusBar.unitTooltip', 'Unit to show amounts in. Click to select another unit.')}
            aria-label={t('statusBar.unitTooltip', 'Unit to show amounts in. Click to select another unit.')}
            style={{
              background: 'none',
              border: 'none',
              padding: '2px 6px',
              cursor: 'pointer',
              borderRadius: '2px',
              color: 'var(--qt-text-secondary)',
              fontSize: '11px',
              fontWeight: 500,
            }}
            onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'rgba(255, 255, 255, 0.1)'}
            onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
          >
            {getUnitLabel(displayUnit)}
          </button>
          {unitDropdownOpen && (
            <div
              style={{
                position: 'absolute',
                bottom: '100%',
                left: 0,
                marginBottom: '4px',
                backgroundColor: '#2b2b2b',
                border: '1px solid #555',
                borderRadius: '4px',
                boxShadow: '0 4px 16px rgba(0, 0, 0, 0.6)',
                zIndex: 60,
                minWidth: '100px',
                padding: '4px 0',
              }}
            >
              {UNIT_OPTIONS.map((opt) => (
                <button
                  key={opt.value}
                  onClick={() => {
                    onUnitChange?.(opt.value);
                    setUnitDropdownOpen(false);
                  }}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    width: '100%',
                    padding: '6px 12px',
                    background: 'none',
                    border: 'none',
                    color: displayUnit === opt.value ? '#4a8af4' : '#ddd',
                    fontSize: '12px',
                    cursor: 'pointer',
                    textAlign: 'left',
                  }}
                  onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'rgba(255, 255, 255, 0.1)'}
                  onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
                >
                  <span style={{ width: '18px', flexShrink: 0 }}>
                    {displayUnit === opt.value ? '✓' : ''}
                  </span>
                  {opt.label}
                </button>
              ))}
            </div>
          )}
        </div>

        {/* HD Wallet status */}
        {isHD && (
          <div className="flex items-center" title={t('statusBar.hdEnabled')}>
            <img
              src="/icons/status/hd_enabled.png"
              alt={t('statusBar.hdEnabled')}
              style={{ width: '16px', height: '16px' }}
            />
          </div>
        )}

        {/* Tor status */}
        {isTor && (
          <div className="flex items-center" title={t('statusBar.torEnabled')}>
            <img
              src="/icons/status/onion.png"
              alt={t('statusBar.torEnabled')}
              style={{ width: '16px', height: '16px' }}
            />
          </div>
        )}

        {/* Encryption status - clickable to open lock/unlock or encrypt dialog */}
        {isEncrypted ? (
          <button
            className="flex items-center"
            onClick={onLockClick}
            title={isLocked ? t('statusBar.walletEncrypted') : isStakingOnly ? t('statusBar.walletStakingOnly', 'Wallet unlocked for staking only') : t('statusBar.walletUnlocked', 'Wallet unlocked')}
            aria-label={isLocked ? t('statusBar.walletEncrypted') : isStakingOnly ? t('statusBar.walletStakingOnly', 'Wallet unlocked for staking only') : t('statusBar.walletUnlocked', 'Wallet unlocked')}
            style={{
              background: 'none',
              border: 'none',
              padding: '2px',
              cursor: 'pointer',
              borderRadius: '2px',
            }}
            onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'rgba(255, 255, 255, 0.1)'}
            onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
          >
            {isLocked ? (
              <Lock size={16} style={{ color: '#cc8888' }} />
            ) : isStakingOnly ? (
              <Lock size={16} style={{ color: '#cc9944' }} />
            ) : (
              <Unlock size={16} style={{ color: '#88cc88' }} />
            )}
          </button>
        ) : (
          <button
            className="flex items-center"
            onClick={onEncryptClick}
            title={t('statusBar.walletNotEncrypted')}
            aria-label={t('statusBar.walletNotEncrypted')}
            style={{
              background: 'none',
              border: 'none',
              padding: '2px',
              cursor: 'pointer',
              borderRadius: '2px',
            }}
            onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'rgba(255, 255, 255, 0.1)'}
            onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
          >
            <LockOpen size={16} style={{ color: '#cc8888', opacity: 0.7 }} />
          </button>
        )}

        {/* Staking status */}
        <div className="flex items-center" title={isStaking ? t('statusBar.stakingActive') : t('statusBar.stakingInactive')}>
          <img
            src={isStaking ? "/icons/status/staking_active.png" : "/icons/status/staking_inactive.png"}
            alt={isStaking ? t('statusBar.stakingActive') : t('statusBar.stakingInactive')}
            style={{ width: '16px', height: '16px' }}
          />
        </div>

        {/* Network connections - clickable to open Tools Window > Peers tab */}
        <button
          className="flex items-center gap-1"
          onClick={onPeersClick}
          title={t('statusBar.connections', { count: connections })}
          aria-label={t('statusBar.connections', { count: connections })}
          style={{
            background: 'none',
            border: 'none',
            padding: '2px',
            cursor: 'pointer',
            borderRadius: '2px',
          }}
          onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'rgba(255, 255, 255, 0.1)'}
          onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
        >
          <img
            src={getConnectionIcon()}
            alt={t('statusBar.connections', { count: connections })}
            style={{ width: '16px', height: '16px' }}
          />
          <span style={{ fontSize: '11px', color: 'var(--qt-text-secondary)' }}>
            {connections}
          </span>
        </button>
      </div>
    </div>
  );
};