import { useEffect, useState, useRef, useCallback } from 'react';
import { Outlet } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Unlock, Lock } from 'lucide-react';
import { Sidebar } from './Sidebar';
import { StatusBar } from './StatusBar';
import { NotificationContainer } from './NotificationContainer';
import { P2PErrorDialog } from './P2PErrorDialog';
import { EncryptWalletDialog } from '@/features/wallet/components/EncryptWalletDialog';
import { UnlockWalletDialog } from '@/features/wallet/components/UnlockWalletDialog';
import { ChangePassphraseDialog } from '@/features/wallet/components/ChangePassphraseDialog';
import { SimpleConfirmDialog } from '@/shared/components/SimpleConfirmDialog';
import { useStore } from '@/store/useStore';
import { useP2PEvents } from '@/shared/hooks/useP2PEvents';
import { GetStakingStatus, GetWalletEncryptionStatus, GetTorStatus, LockWallet, UpdateSetting } from '@wailsjs/go/main/App';
import { EventsOn, EventsOff } from '@wailsjs/runtime/runtime';
import { ToolsTab } from '@/features/tools/constants';
import '@/styles/qt-theme.css';

// Status bar indicator state
interface StatusIndicators {
  isStaking: boolean;
  isEncrypted: boolean;
  isLocked: boolean;
  isStakingOnly: boolean;
  isTor: boolean;
}

export const MainLayout: React.FC = () => {
  // Subscribe to P2P events and update store
  const { errorDialogOpen, errorMessage, dismissError, retryConnection } = useP2PEvents();

  // Get connection status, blockchain info, and tools dialog actions from store
  const connectionStatus = useStore((state) => state.connectionStatus);
  const blockchainInfo = useStore((state) => state.blockchainInfo);
  const openToolsDialog = useStore((state) => state.openToolsDialog);
  const loadDisplayUnits = useStore((state) => state.loadDisplayUnits);
  const displayUnit = useStore((state) => state.displayUnit);
  const setDisplayUnit = useStore((state) => state.setDisplayUnit);

  // Load display unit settings from backend on mount
  useEffect(() => {
    loadDisplayUnits();
  }, [loadDisplayUnits]);

  // Status bar indicators state
  const [statusIndicators, setStatusIndicators] = useState<StatusIndicators>({
    isStaking: false,
    isEncrypted: false,
    isLocked: true,
    isStakingOnly: false,
    isTor: false,
  });

  // Wallet dialog states
  const [encryptDialogOpen, setEncryptDialogOpen] = useState(false);
  const [unlockDialogOpen, setUnlockDialogOpen] = useState(false);
  const [changePassphraseDialogOpen, setChangePassphraseDialogOpen] = useState(false);
  const [lockConfirmDialogOpen, setLockConfirmDialogOpen] = useState(false);
  const [isLocking, setIsLocking] = useState(false);
  const [stakingPopover, setStakingPopover] = useState<{ x: number; y: number } | null>(null);
  const stakingPopoverRef = useRef<HTMLDivElement>(null);

  const { t } = useTranslation('common');

  // Use ref to track if component is mounted (prevent state updates after unmount)
  const isMounted = useRef(true);

  // Poll for status updates
  useEffect(() => {
    isMounted.current = true;

    const fetchStatusIndicators = async () => {
      try {
        const [stakingStatus, encryptionStatus, torStatus] = await Promise.all([
          GetStakingStatus(),
          GetWalletEncryptionStatus(),
          GetTorStatus(),
        ]);

        if (isMounted.current) {
          setStatusIndicators({
            isStaking: stakingStatus?.staking ?? false,
            isEncrypted: encryptionStatus?.encrypted ?? false,
            isLocked: encryptionStatus?.locked ?? true,
            isStakingOnly: encryptionStatus?.status === 'unlocked_staking',
            isTor: torStatus?.enabled ?? false,
          });
        }
      } catch (error) {
        console.error('Failed to fetch status indicators:', error);
      }
    };

    // Initial fetch
    fetchStatusIndicators();

    // Poll every 2 seconds (matches P2P status polling interval)
    const intervalId = setInterval(fetchStatusIndicators, 2000);

    return () => {
      isMounted.current = false;
      clearInterval(intervalId);
    };
  }, []);

  // Listen for wallet state change events
  useEffect(() => {
    const handleWalletEncrypted = () => {
      if (isMounted.current) {
        setStatusIndicators((prev) => ({ ...prev, isEncrypted: true, isLocked: true, isStakingOnly: false }));
      }
    };

    const handleWalletUnlocked = (data?: { stakingOnly?: boolean }) => {
      if (isMounted.current) {
        setStatusIndicators((prev) => ({
          ...prev,
          isLocked: false,
          isStakingOnly: data?.stakingOnly ?? false,
        }));
      }
    };

    const handleWalletLocked = () => {
      if (isMounted.current) {
        setStatusIndicators((prev) => ({ ...prev, isLocked: true, isStakingOnly: false }));
      }
    };

    const handlePassphraseChanged = () => {
      // Passphrase changed - wallet is still encrypted and locked after change
      if (isMounted.current) {
        setStatusIndicators((prev) => ({ ...prev, isEncrypted: true, isLocked: true, isStakingOnly: false }));
      }
    };

    // Subscribe to wallet events
    EventsOn('wallet:encrypted', handleWalletEncrypted);
    EventsOn('wallet:unlocked', handleWalletUnlocked);
    EventsOn('wallet:locked', handleWalletLocked);
    EventsOn('wallet:passphrase_changed', handlePassphraseChanged);

    return () => {
      EventsOff('wallet:encrypted');
      EventsOff('wallet:unlocked');
      EventsOff('wallet:locked');
      EventsOff('wallet:passphrase_changed');
    };
  }, []);

  // Listen for invalid masternode collateral events
  const addNotification = useStore((state) => state.addNotification);
  useEffect(() => {
    const handleInvalidCollateral = (data: { alias: string; txHash: string; vout: number }) => {
      if (isMounted.current && data?.alias) {
        addNotification({
          type: 'warning',
          title: 'Masternode Collateral Missing',
          message: `Collateral UTXO for masternode "${data.alias}" was not found. It may have been spent externally.`,
          duration: 15000,
        });
      }
    };

    EventsOn('masternode:invalid_collateral', handleInvalidCollateral);

    return () => {
      EventsOff('masternode:invalid_collateral');
    };
  }, [addNotification]);

  // Listen for settings dialog open events
  useEffect(() => {
    const handleOpenEncrypt = () => setEncryptDialogOpen(true);
    const handleOpenUnlock = () => setUnlockDialogOpen(true);
    const handleOpenChangePassphrase = () => setChangePassphraseDialogOpen(true);

    EventsOn('settings:open-encrypt-dialog', handleOpenEncrypt);
    EventsOn('settings:open-unlock-dialog', handleOpenUnlock);
    EventsOn('settings:open-change-passphrase-dialog', handleOpenChangePassphrase);

    return () => {
      EventsOff('settings:open-encrypt-dialog');
      EventsOff('settings:open-unlock-dialog');
      EventsOff('settings:open-change-passphrase-dialog');
    };
  }, []);

  // Close staking-only popover on click outside
  useEffect(() => {
    if (stakingPopover) {
      const handleClickOutside = (e: MouseEvent) => {
        if (stakingPopoverRef.current && !stakingPopoverRef.current.contains(e.target as Node)) {
          setStakingPopover(null);
        }
      };
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [stakingPopover]);

  // Handle lock icon click - show appropriate dialog based on wallet state
  const handleLockClick = useCallback((e: React.MouseEvent) => {
    if (statusIndicators.isLocked) {
      setUnlockDialogOpen(true);
    } else if (statusIndicators.isStakingOnly) {
      // Show popover with "Unlock fully" and "Lock wallet" options
      const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
      setStakingPopover({ x: rect.left, y: rect.top });
    } else {
      setLockConfirmDialogOpen(true);
    }
  }, [statusIndicators.isLocked, statusIndicators.isStakingOnly]);

  // Handle confirmed lock action
  const handleConfirmLock = useCallback(async () => {
    setIsLocking(true);
    try {
      await LockWallet();
      // Event listener will update the status
      setLockConfirmDialogOpen(false);
    } catch (error) {
      console.error('Failed to lock wallet:', error);
    } finally {
      setIsLocking(false);
    }
  }, []);

  // Handle encrypt click - show encrypt dialog
  const handleEncryptClick = useCallback(() => {
    setEncryptDialogOpen(true);
  }, []);

  // Handle peers click - open Tools Window on Peers tab (index 3)
  const handlePeersClick = useCallback(() => {
    openToolsDialog(ToolsTab.Peers);
  }, [openToolsDialog]);

  // Handle unit display change - persist to settings and update store
  const handleUnitChange = useCallback(async (unit: number) => {
    const prevUnit = displayUnit;
    setDisplayUnit(unit);
    try {
      await UpdateSetting('nDisplayUnit', unit);
    } catch (error) {
      console.error('Failed to persist display unit:', error);
      setDisplayUnit(prevUnit);
    }
  }, [setDisplayUnit, displayUnit]);

  // Refresh status indicators after dialog success
  const handleDialogSuccess = useCallback(() => {
    // Status will be updated via event listeners
  }, []);

  return (
    <div
      className="flex h-screen"
      style={{
        backgroundColor: 'var(--qt-bg-primary)',
        margin: 0,
        padding: 0,
        position: 'relative',
        top: 0
      }}
    >
      <Sidebar />

      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Main content area */}
        <main
          className="flex-1 overflow-y-auto"
          style={{
            backgroundColor: 'var(--qt-bg-primary)',
            paddingBottom: 'var(--qt-statusbar-height)' // Make room for status bar
          }}
        >
          <Outlet />
        </main>
      </div>

      <StatusBar
        isConnected={connectionStatus.isConnected}
        connections={connectionStatus.peers}
        syncProgress={blockchainInfo?.sync_percentage ?? 0}
        blockHeight={blockchainInfo?.blocks ?? 0}
        behindText={blockchainInfo?.behind_time ?? ''}
        isStaking={statusIndicators.isStaking}
        isEncrypted={statusIndicators.isEncrypted}
        isLocked={statusIndicators.isLocked}
        isStakingOnly={statusIndicators.isStakingOnly}
        isHD={true}
        isTor={statusIndicators.isTor}
        displayUnit={displayUnit}
        onLockClick={handleLockClick}
        onEncryptClick={handleEncryptClick}
        onPeersClick={handlePeersClick}
        onUnitChange={handleUnitChange}
      />
      <NotificationContainer />
      <P2PErrorDialog
        isOpen={errorDialogOpen}
        error={errorMessage}
        onRetry={retryConnection}
        onDismiss={dismissError}
      />

      {/* Wallet Encryption Dialogs */}
      <EncryptWalletDialog
        isOpen={encryptDialogOpen}
        onClose={() => setEncryptDialogOpen(false)}
        onSuccess={handleDialogSuccess}
        zIndex={1100}
      />
      <UnlockWalletDialog
        isOpen={unlockDialogOpen}
        onClose={() => setUnlockDialogOpen(false)}
        onSuccess={handleDialogSuccess}
        zIndex={1100}
      />
      <ChangePassphraseDialog
        isOpen={changePassphraseDialogOpen}
        onClose={() => setChangePassphraseDialogOpen(false)}
        onSuccess={handleDialogSuccess}
        zIndex={1100}
      />
      <SimpleConfirmDialog
        isOpen={lockConfirmDialogOpen}
        onCancel={() => setLockConfirmDialogOpen(false)}
        onConfirm={handleConfirmLock}
        title="Lock Wallet"
        message="Are you sure you want to lock the wallet? You will need to enter your passphrase to unlock it again."
        confirmText="Lock"
        isLoading={isLocking}
      />

      {/* Staking-only popover menu */}
      {stakingPopover && (
        <div
          ref={stakingPopoverRef}
          style={{
            position: 'fixed',
            left: stakingPopover.x,
            bottom: window.innerHeight - stakingPopover.y + 4,
            backgroundColor: '#2b2b2b',
            border: '1px solid #555',
            borderRadius: '4px',
            boxShadow: '0 4px 16px rgba(0, 0, 0, 0.6)',
            zIndex: 60,
            minWidth: '160px',
            padding: '4px 0',
          }}
        >
          <button
            onClick={() => {
              setStakingPopover(null);
              setUnlockDialogOpen(true);
            }}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              width: '100%',
              padding: '8px 12px',
              background: 'none',
              border: 'none',
              color: '#ddd',
              fontSize: '12px',
              cursor: 'pointer',
              textAlign: 'left',
            }}
            onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'rgba(255, 255, 255, 0.1)'}
            onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
          >
            <Unlock size={14} style={{ color: '#88cc88' }} />
            {t('statusBar.unlockFully', 'Unlock fully')}
          </button>
          <button
            onClick={() => {
              setStakingPopover(null);
              setLockConfirmDialogOpen(true);
            }}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              width: '100%',
              padding: '8px 12px',
              background: 'none',
              border: 'none',
              color: '#ddd',
              fontSize: '12px',
              cursor: 'pointer',
              textAlign: 'left',
            }}
            onMouseEnter={(e) => e.currentTarget.style.backgroundColor = 'rgba(255, 255, 255, 0.1)'}
            onMouseLeave={(e) => e.currentTarget.style.backgroundColor = 'transparent'}
          >
            <Lock size={14} style={{ color: '#cc8888' }} />
            {t('statusBar.lockWallet', 'Lock wallet')}
          </button>
        </div>
      )}
    </div>
  );
};