import React, { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useMasternodes } from '@/store/useStore';
import { Masternode, NetworkMasternode, MasternodeStatistics } from '@/shared/types/masternode.types';
import { SimpleConfirmDialog } from '@/shared/components/SimpleConfirmDialog';
import { RefreshCountdown } from '@/shared/components/RefreshCountdown';
import { UnlockWalletDialog } from '@/features/wallet/components/UnlockWalletDialog';
import { sanitizeErrorMessage } from '@/shared/utils/sanitize';
import { GetMyMasternodes, StartMasternode, StartAllMasternodes, StartMissingMasternodes, GetNetworkMasternodes, GetMasternodeStatistics } from '@wailsjs/go/main/App';
import { useWalletAction } from '@/shared/hooks/useWalletAction';
import { EventsOn, EventsOff } from '@wailsjs/runtime/runtime';
import {
  MasternodesTable,
  MasternodesActions,
  MasternodesContextMenu,
  MasternodeConfigDialog,
  MasternodeSetupWizard,
  NetworkMasternodesTable,
  NetworkMasternodesFilters,
  MasternodeStatisticsPanel,
  MasternodeDebugPanel,
  PaymentStatsTab,
  type SortColumn,
  type SortDirection,
  type NetworkSortColumn,
} from '../components';

// Auto-refresh interval from Qt: MY_MASTERNODELIST_UPDATE_SECONDS = 60
const MY_MASTERNODES_REFRESH_SECONDS = 60;

// Network masternodes refresh interval
const NETWORK_REFRESH_SECONDS = 60;

// Confirmation dialog types
type ConfirmAction = 'start_alias' | 'start_all' | 'start_missing' | null;

// Pending action after wallet unlock
type PendingAction = 'start_alias' | 'start_all' | 'start_missing' | null;

// Context menu state
interface ContextMenuState {
  visible: boolean;
  x: number;
  y: number;
  masternode: Masternode | null;
}

// Map backend MyMasternode to frontend Masternode type
const mapToMasternode = (mn: any): Masternode => ({
  id: mn.alias || mn.id || '',
  alias: mn.alias || '',
  address: mn.address || '',
  protocol: mn.protocol || 0,
  status: mn.status || 'MISSING',
  activeTime: mn.activeTime || mn.active_time || mn.active_seconds || 0,
  lastSeen: mn.lastSeen || mn.last_seen || new Date(),
  tier: mn.tier || 'bronze',
  txHash: mn.txHash || mn.tx_hash || '',
  outputIndex: mn.outputIndex || mn.output_index || 0,
  collateralAddress: mn.collateralAddress || mn.collateral_address || '',
  rewards: mn.rewards || 0,
});

// Map backend MasternodeInfo to frontend NetworkMasternode type with validation
// Backend returns core.MasternodeInfo with json tags matching our field names
const mapToNetworkMasternode = (mn: any): NetworkMasternode | null => {
  // Validate required fields exist
  if (typeof mn.rank !== 'number' || typeof mn.addr !== 'string' || typeof mn.status !== 'string') {
    return null;
  }
  return {
    rank: mn.rank,
    txhash: mn.txhash || '',
    outidx: mn.outidx || 0,
    status: mn.status,
    addr: mn.addr,
    version: mn.version || 0,
    lastseen: mn.lastseen || '',
    activetime: mn.activetime || 0,
    lastpaid: mn.lastpaid || '',
    tier: mn.tier || '',
    paymentaddress: mn.paymentaddress || '',
    pubkey: mn.pubkey || '',
    pubkey_operator: mn.pubkey_operator || '',
  };
};

export const MasternodesPage: React.FC = () => {
  const { t } = useTranslation('masternode');
  const {
    masternodes,
    selectedMasternode,
    selectMasternode,
    isLoading,
    isStartingMasternode,
    operationError,
    operationSuccess,
    setMasternodes,
    setLoading,
    setStartingMasternode,
    setLastRefresh,
    setOperationError,
    setOperationSuccess,
    clearOperationMessages,
    // Network masternodes state
    networkMasternodes,
    isLoadingNetwork,
    networkFilters,
    masternodeActiveTab,
    setNetworkMasternodes,
    setLoadingNetwork,
    setNetworkLastRefresh,
    setNetworkFilters,
    setMasternodeActiveTab,
    getFilteredNetworkMasternodes,
    getNetworkMasternodeCount,
  } = useMasternodes();

  // Sorting state for My Masternodes tab
  const [sortColumn, setSortColumn] = useState<SortColumn>('alias');
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc');

  // Auto-refresh countdown state for My Masternodes
  const [myCountdown, setMyCountdown] = useState<number>(MY_MASTERNODES_REFRESH_SECONDS);
  const myCountdownRef = useRef<number>(MY_MASTERNODES_REFRESH_SECONDS);

  // Auto-refresh countdown state for Network Masternodes
  const [networkCountdown, setNetworkCountdown] = useState<number>(NETWORK_REFRESH_SECONDS);
  const networkCountdownRef = useRef<number>(NETWORK_REFRESH_SECONDS);

  // Confirmation dialog state
  const [confirmAction, setConfirmAction] = useState<ConfirmAction>(null);

  // Configuration dialog state
  const [configDialogOpen, setConfigDialogOpen] = useState(false);

  // Setup wizard state
  const [wizardOpen, setWizardOpen] = useState(false);

  // Wallet unlock hook (matches legacy masternodelist.cpp:265-280)
  const { showUnlockDialog, executeWithUnlock, unlockDialogProps } = useWalletAction({
    restoreAfter: true,
    onCancel: () => setOperationError(t('messages.unlockCancelled')),
  });

  // Context menu state (Qt masternodelist.cpp:51-57)
  const [contextMenu, setContextMenu] = useState<ContextMenuState>({
    visible: false,
    x: 0,
    y: 0,
    masternode: null,
  });
  const contextMenuRef = useRef<HTMLDivElement>(null);

  // Refs to track state for stable callbacks (avoids stale closures)
  const isLoadingRef = useRef(false);
  const selectedMasternodeRef = useRef(selectedMasternode);
  selectedMasternodeRef.current = selectedMasternode;

  // Fetch my masternodes from backend
  const fetchMasternodes = useCallback(async () => {
    if (isLoadingRef.current) return; // Prevent concurrent fetches

    isLoadingRef.current = true;
    setLoading(true);
    clearOperationMessages();
    try {
      const result = await GetMyMasternodes();
      if (result) {
        const mapped = result.map(mapToMasternode);
        setMasternodes(mapped);
      }
      setLastRefresh(Date.now());
      // Reset countdown after refresh
      myCountdownRef.current = MY_MASTERNODES_REFRESH_SECONDS;
      setMyCountdown(MY_MASTERNODES_REFRESH_SECONDS);
    } catch (error) {
      console.error('Failed to fetch masternodes:', error);
      setOperationError(t('messages.fetchFailed'));
    } finally {
      isLoadingRef.current = false;
      setLoading(false);
    }
  }, [setLoading, setMasternodes, setLastRefresh, setOperationError, clearOperationMessages]);

  // Stable ref for fetchMasternodes to avoid effect re-runs
  const fetchMasternodesRef = useRef(fetchMasternodes);
  fetchMasternodesRef.current = fetchMasternodes;

  // Ref to track network loading state
  const isLoadingNetworkRef = useRef(false);

  // Statistics state
  const [statistics, setStatistics] = useState<MasternodeStatistics | null>(null);
  const [isLoadingStatistics, setIsLoadingStatistics] = useState(false);

  // Fetch network masternodes and statistics from backend
  const fetchNetworkMasternodes = useCallback(async () => {
    if (isLoadingNetworkRef.current) return; // Prevent concurrent fetches

    isLoadingNetworkRef.current = true;
    setLoadingNetwork(true);
    setIsLoadingStatistics(true);
    try {
      // Fetch masternodes and statistics in parallel
      const [networkResult, statsResult] = await Promise.all([
        GetNetworkMasternodes(),
        GetMasternodeStatistics(),
      ]);

      if (networkResult && Array.isArray(networkResult)) {
        // Map and filter with proper type validation
        const mapped = networkResult
          .map(mapToNetworkMasternode)
          .filter((mn): mn is NetworkMasternode => mn !== null);
        setNetworkMasternodes(mapped);
      }

      if (statsResult) {
        setStatistics(statsResult as MasternodeStatistics);
      }

      setNetworkLastRefresh(Date.now());
      // Reset countdown after refresh
      networkCountdownRef.current = NETWORK_REFRESH_SECONDS;
      setNetworkCountdown(NETWORK_REFRESH_SECONDS);
    } catch (error) {
      console.error('Failed to fetch network masternodes:', error);
    } finally {
      isLoadingNetworkRef.current = false;
      setLoadingNetwork(false);
      setIsLoadingStatistics(false);
    }
  }, [setLoadingNetwork, setNetworkMasternodes, setNetworkLastRefresh]);

  // Stable ref for fetchNetworkMasternodes to avoid effect re-runs
  const fetchNetworkMasternodesRef = useRef(fetchNetworkMasternodes);
  fetchNetworkMasternodesRef.current = fetchNetworkMasternodes;

  // Auto-refresh timer for My Masternodes (only runs when tab is active)
  useEffect(() => {
    if (masternodeActiveTab !== 'my') return;

    // Fresh fetch and countdown reset on every tab entry
    myCountdownRef.current = MY_MASTERNODES_REFRESH_SECONDS;
    setMyCountdown(MY_MASTERNODES_REFRESH_SECONDS);
    fetchMasternodesRef.current();

    // Countdown timer - runs every second
    const countdownInterval = setInterval(() => {
      myCountdownRef.current -= 1;
      setMyCountdown(myCountdownRef.current);

      if (myCountdownRef.current <= 0) {
        // Use ref to get latest function without causing effect re-run
        fetchMasternodesRef.current();
      }
    }, 1000);

    return () => {
      clearInterval(countdownInterval);
    };
  }, [masternodeActiveTab]); // Re-run when tab changes: pause on exit, fetch + reset on entry

  // Auto-refresh timer for Network Masternodes (only runs when tab is active)
  useEffect(() => {
    if (masternodeActiveTab !== 'network') return;

    // Fresh fetch and countdown reset on every tab entry
    networkCountdownRef.current = NETWORK_REFRESH_SECONDS;
    setNetworkCountdown(NETWORK_REFRESH_SECONDS);
    fetchNetworkMasternodesRef.current();

    // Countdown timer - runs every second
    const countdownInterval = setInterval(() => {
      networkCountdownRef.current -= 1;
      setNetworkCountdown(networkCountdownRef.current);

      if (networkCountdownRef.current <= 0) {
        fetchNetworkMasternodesRef.current();
      }
    }, 1000);

    return () => {
      clearInterval(countdownInterval);
    };
  }, [masternodeActiveTab]); // Re-run when tab changes: pause on exit, fetch + reset on entry

  // Event listener for backend updates (separate effect)
  useEffect(() => {
    EventsOn('masternode:updated', () => {
      fetchMasternodesRef.current();
    });

    return () => {
      EventsOff('masternode:updated');
    };
  }, []); // Empty deps - event subscription runs once

  // Auto-clear success/error messages after 5 seconds
  useEffect(() => {
    if (operationSuccess || operationError) {
      const timer = setTimeout(() => {
        clearOperationMessages();
      }, 5000);
      return () => clearTimeout(timer);
    }
  }, [operationSuccess, operationError, clearOperationMessages]);

  // Close context menu when clicking outside (Qt behavior)
  useEffect(() => {
    if (contextMenu.visible) {
      const handleClickOutside = (e: MouseEvent) => {
        if (contextMenuRef.current && !contextMenuRef.current.contains(e.target as Node)) {
          setContextMenu(prev => ({ ...prev, visible: false }));
        }
      };
      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }
  }, [contextMenu.visible]);

  // Execute a masternode action with wallet unlock if needed
  const runMasternodeAction = useCallback(async (action: PendingAction) => {
    if (!action) return;

    setStartingMasternode(true);
    clearOperationMessages();

    try {
      switch (action) {
        case 'start_alias':
          if (!selectedMasternodeRef.current) break;
          await StartMasternode(selectedMasternodeRef.current.alias);
          setOperationSuccess(t('messages.startSuccess', { alias: selectedMasternodeRef.current.alias }));
          break;
        case 'start_all':
          await StartAllMasternodes();
          setOperationSuccess(t('messages.startAllSuccess'));
          break;
        case 'start_missing':
          const count = await StartMissingMasternodes();
          if (count > 0) {
            setOperationSuccess(t('messages.startMissingSuccess', { count }));
          } else {
            setOperationSuccess(t('messages.noMissingToStart'));
          }
          break;
      }
      await fetchMasternodes();
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error);
      setOperationError(sanitizeErrorMessage(errorMsg));
    } finally {
      setStartingMasternode(false);
    }
  }, [t, fetchMasternodes, setStartingMasternode, clearOperationMessages, setOperationSuccess, setOperationError]);

  // Check wallet and execute with unlock if needed
  const checkWalletAndExecute = useCallback(async (action: PendingAction) => {
    await executeWithUnlock(async () => {
      await runMasternodeAction(action);
    });
  }, [executeWithUnlock, runMasternodeAction]);

  // Handle column header click for sorting (My Masternodes)
  const handleSort = useCallback((column: SortColumn) => {
    setSortColumn(prev => {
      if (prev === column) {
        setSortDirection(d => d === 'asc' ? 'desc' : 'asc');
        return prev;
      }
      setSortDirection('asc');
      return column;
    });
  }, []);

  // Handle column header click for sorting (Network Masternodes)
  const networkFiltersRef = useRef(networkFilters);
  networkFiltersRef.current = networkFilters;
  const handleNetworkSort = useCallback((column: NetworkSortColumn) => {
    const filters = networkFiltersRef.current;
    if (filters.sortColumn === column) {
      setNetworkFilters({ sortDirection: filters.sortDirection === 'asc' ? 'desc' : 'asc' });
    } else {
      setNetworkFilters({ sortColumn: column, sortDirection: 'asc' });
    }
  }, [setNetworkFilters]);

  // Handle row click for selection
  const handleRowClick = useCallback((masternode: Masternode) => {
    if (selectedMasternodeRef.current?.id === masternode.id) {
      selectMasternode(null);
    } else {
      selectMasternode(masternode);
    }
  }, [selectMasternode]);

  // Handle right-click context menu (Qt masternodelist.cpp:83-87)
  const handleContextMenu = useCallback((e: React.MouseEvent, masternode: Masternode) => {
    e.preventDefault();
    e.stopPropagation();
    selectMasternode(masternode);

    // Calculate position with viewport boundary checking
    const menuWidth = 140;
    const menuHeight = 40;
    const padding = 10;

    let x = e.clientX;
    let y = e.clientY;

    if (x + menuWidth + padding > window.innerWidth) {
      x = window.innerWidth - menuWidth - padding;
    }
    if (y + menuHeight + padding > window.innerHeight) {
      y = window.innerHeight - menuHeight - padding;
    }
    x = Math.max(padding, x);
    y = Math.max(padding, y);

    setContextMenu({
      visible: true,
      x,
      y,
      masternode,
    });
  }, [selectMasternode]);

  // Handle context menu action
  const handleContextMenuAction = () => {
    if (contextMenu.masternode) {
      setConfirmAction('start_alias');
    }
    setContextMenu(prev => ({ ...prev, visible: false }));
  };

  const handleUpdateStatus = () => {
    fetchMasternodes();
  };

  // Confirm dialog handler - triggers wallet check before action
  const handleConfirmAction = () => {
    setConfirmAction(null);
    checkWalletAndExecute(confirmAction);
  };

  const getConfirmMessage = (): string => {
    switch (confirmAction) {
      case 'start_alias':
        return t('dialogs.startConfirm.message', { alias: selectedMasternode?.alias });
      case 'start_all':
        return t('dialogs.startConfirm.messageAll');
      case 'start_missing':
        return t('dialogs.startConfirm.messageMissing');
      default:
        return '';
    }
  };

  // Memoize filtered network masternodes to prevent re-computation on countdown ticks.
  // Deps include both the data inputs and the getter functions themselves for correctness.
  const filteredNetworkMasternodes = useMemo(
    () => getFilteredNetworkMasternodes(),
    [getFilteredNetworkMasternodes, networkMasternodes, networkFilters]
  );
  const networkCount = useMemo(
    () => getNetworkMasternodeCount(),
    [getNetworkMasternodeCount, networkMasternodes, networkFilters]
  );

  // Check if network data has been loaded at least once (for loading indicator)
  const { networkLastRefresh } = useMasternodes();
  const networkHasLoaded = networkLastRefresh !== null;

  return (
    <div className="qt-frame" style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <div className="qt-vbox" style={{ padding: '8px', height: '100%', display: 'flex', flexDirection: 'column' }}>
        {/* Page Header */}
        <div className="qt-header-label" style={{ marginBottom: '8px', fontSize: '18px' }}>
          {t('title').toUpperCase()}
        </div>

        {/* Tab Buttons */}
        <div style={{
          display: 'flex',
          gap: '0',
          marginBottom: '8px',
          borderBottom: '1px solid #4a4a4a',
        }}>
          <button
            onClick={() => setMasternodeActiveTab('my')}
            style={{
              padding: '8px 16px',
              fontSize: '12px',
              fontWeight: masternodeActiveTab === 'my' ? 'bold' : 'normal',
              backgroundColor: masternodeActiveTab === 'my' ? '#3a3a3a' : 'transparent',
              color: masternodeActiveTab === 'my' ? '#fff' : '#999',
              border: 'none',
              borderBottom: masternodeActiveTab === 'my' ? '2px solid #4a8af4' : '2px solid transparent',
              cursor: 'pointer',
              transition: 'all 0.15s',
            }}
          >
            {t('tabs.myMasternodes')}
          </button>
          <button
            onClick={() => setMasternodeActiveTab('network')}
            style={{
              padding: '8px 16px',
              fontSize: '12px',
              fontWeight: masternodeActiveTab === 'network' ? 'bold' : 'normal',
              backgroundColor: masternodeActiveTab === 'network' ? '#3a3a3a' : 'transparent',
              color: masternodeActiveTab === 'network' ? '#fff' : '#999',
              border: 'none',
              borderBottom: masternodeActiveTab === 'network' ? '2px solid #4a8af4' : '2px solid transparent',
              cursor: 'pointer',
              transition: 'all 0.15s',
            }}
          >
            {t('tabs.network')}
          </button>
          <button
            onClick={() => setMasternodeActiveTab('payments')}
            style={{
              padding: '8px 16px',
              fontSize: '12px',
              fontWeight: masternodeActiveTab === 'payments' ? 'bold' : 'normal',
              backgroundColor: masternodeActiveTab === 'payments' ? '#3a3a3a' : 'transparent',
              color: masternodeActiveTab === 'payments' ? '#fff' : '#999',
              border: 'none',
              borderBottom: masternodeActiveTab === 'payments' ? '2px solid #4a8af4' : '2px solid transparent',
              cursor: 'pointer',
              transition: 'all 0.15s',
            }}
          >
            {t('tabs.paymentStats')}
          </button>
          <button
            onClick={() => setMasternodeActiveTab('debug')}
            style={{
              padding: '8px 16px',
              fontSize: '12px',
              fontWeight: masternodeActiveTab === 'debug' ? 'bold' : 'normal',
              backgroundColor: masternodeActiveTab === 'debug' ? '#3a3a3a' : 'transparent',
              color: masternodeActiveTab === 'debug' ? '#fff' : '#999',
              border: 'none',
              borderBottom: masternodeActiveTab === 'debug' ? '2px solid #4a8af4' : '2px solid transparent',
              cursor: 'pointer',
              transition: 'all 0.15s',
            }}
          >
            Debug
          </button>
        </div>

        {/* Success/Error Messages (only show on My Masternodes tab) */}
        {masternodeActiveTab === 'my' && operationSuccess && (
          <div style={{
            padding: '8px 12px',
            marginBottom: '8px',
            fontSize: '12px',
            color: '#00ff00',
            backgroundColor: '#1a3a1a',
            border: '1px solid #00ff00',
            borderRadius: '2px',
          }}>
            {operationSuccess}
          </div>
        )}
        {masternodeActiveTab === 'my' && operationError && (
          <div style={{
            padding: '8px 12px',
            marginBottom: '8px',
            fontSize: '12px',
            color: '#ff6666',
            backgroundColor: '#3a1a1a',
            border: '1px solid #ff6666',
            borderRadius: '2px',
          }}>
            {operationError}
          </div>
        )}

        {/* My Masternodes Tab Content */}
        {masternodeActiveTab === 'my' && (
          <>
            {/* Warning Note */}
            <div style={{
              padding: '8px',
              marginBottom: '8px',
              fontSize: '11px',
              color: '#cccccc',
              lineHeight: '1.4'
            }}>
              {t('note.title')} {t('note.line1')}<br />
              {t('note.line2')}<br />
              {t('note.line3')}
            </div>

            {/* Refresh Countdown */}
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: '4px' }}>
              <RefreshCountdown
                countdown={myCountdown}
                total={MY_MASTERNODES_REFRESH_SECONDS}
                mode="display"
              />
            </div>

            {/* Masternodes Table */}
            <MasternodesTable
              masternodes={masternodes}
              selectedMasternode={selectedMasternode}
              isLoading={isLoading}
              sortColumn={sortColumn}
              sortDirection={sortDirection}
              onSort={handleSort}
              onRowClick={handleRowClick}
              onContextMenu={handleContextMenu}
            />

            {/* Action Buttons */}
            <MasternodesActions
              selectedMasternode={selectedMasternode}
              isLoading={isLoading}
              isStartingMasternode={isStartingMasternode}
              onStartAlias={() => setConfirmAction('start_alias')}
              onStartAll={() => setConfirmAction('start_all')}
              onStartMissing={() => setConfirmAction('start_missing')}
              onUpdateStatus={handleUpdateStatus}
              onConfigure={() => setConfigDialogOpen(true)}
              onSetupWizard={() => setWizardOpen(true)}
            />
          </>
        )}

        {/* Network Masternodes Tab Content */}
        {masternodeActiveTab === 'network' && (
          <>
            {/* Statistics Panel */}
            <MasternodeStatisticsPanel
              statistics={statistics}
              isLoading={isLoadingStatistics}
            />

            {/* Filters and Count */}
            <NetworkMasternodesFilters
              filters={networkFilters}
              filteredCount={networkCount.filtered}
              totalCount={networkCount.total}
              countdown={networkCountdown}
              countdownTotal={NETWORK_REFRESH_SECONDS}
              onFilterChange={setNetworkFilters}
              onRefresh={fetchNetworkMasternodes}
              isLoading={isLoadingNetwork}
            />

            {/* Network Masternodes Table */}
            <NetworkMasternodesTable
              masternodes={filteredNetworkMasternodes}
              isLoading={isLoadingNetwork}
              hasLoaded={networkHasLoaded}
              filters={networkFilters}
              onSort={handleNetworkSort}
            />
          </>
        )}

        {/* Payment Stats Tab Content */}
        {masternodeActiveTab === 'payments' && (
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0, padding: '0' }}>
            <PaymentStatsTab />
          </div>
        )}

        {/* Debug Tab Content */}
        {masternodeActiveTab === 'debug' && (
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
            <MasternodeDebugPanel />
          </div>
        )}
      </div>

      {/* Confirmation Dialog */}
      <SimpleConfirmDialog
        isOpen={confirmAction !== null}
        title={t('dialogs.startConfirm.title')}
        message={getConfirmMessage()}
        confirmText={t('common:buttons.yes')}
        cancelText={t('common:buttons.no')}
        onConfirm={handleConfirmAction}
        onCancel={() => setConfirmAction(null)}
        isLoading={isStartingMasternode}
      />

      {/* Context Menu */}
      <MasternodesContextMenu
        ref={contextMenuRef}
        visible={contextMenu.visible}
        x={contextMenu.x}
        y={contextMenu.y}
        isStartingMasternode={isStartingMasternode}
        onStartAlias={handleContextMenuAction}
      />

      {/* Configuration Dialog */}
      <MasternodeConfigDialog
        isOpen={configDialogOpen}
        onClose={() => {
          setConfigDialogOpen(false);
          // Refresh masternodes list when dialog closes in case config changed
          fetchMasternodes();
        }}
      />

      {/* Setup Wizard */}
      <MasternodeSetupWizard
        isOpen={wizardOpen}
        onClose={() => setWizardOpen(false)}
        onSuccess={() => {
          fetchMasternodes();
        }}
      />

      {/* Wallet Unlock Dialog (matches legacy masternodelist.cpp:265-280) */}
      <UnlockWalletDialog
        isOpen={showUnlockDialog}
        {...unlockDialogProps}
        temporaryUnlock
      />
    </div>
  );
};

export { MasternodesPage as Masternodes };
