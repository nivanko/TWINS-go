import React, { useEffect, useCallback, useState, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useStore } from '@/store/useStore';
import { useShallow } from 'zustand/react/shallow';
import { useWalletActions } from '@/shared/hooks/useWalletActions';
import { CombinedBalanceCard } from './CombinedBalanceCard';
import { TWINSBalanceCard } from './TWINSBalanceCard';
import { SyncStatusWidget } from './SyncStatusWidget';
import { NetworkStatusWidget } from './NetworkStatusWidget';
import { StakingStatusWidget } from './StakingStatusWidget';
import { LoadingSpinner } from '@/shared/components/LoadingSpinner';
import { TransactionList } from './TransactionItem';
import { TransactionDetailsDialog } from './TransactionDetailsDialog';
import { EventsOn } from '@wailsjs/runtime/runtime';
import { GetBalance, GetRecentTransactions, GetNetworkInfo, GetStakingInfo, GetSettingBool } from '@wailsjs/go/main/App';
import { core } from '@/shared/types/wallet.types';
import { logger } from '@/shared/utils/logger';
import '@/styles/qt-theme.css';

// Auto-refresh interval in milliseconds (10 seconds)
const STATUS_REFRESH_INTERVAL = 10000;

const OverviewPage: React.FC = () => {
  const { t } = useTranslation('wallet');
  const { balance, isLoading } = useStore(useShallow((s) => ({
    balance: s.balance,
    isLoading: s.isLoading,
  })));
  const { refreshBalance } = useWalletActions();
  const [recentTransactions, setRecentTransactions] = useState<core.Transaction[]>([]);
  const [txLoading, setTxLoading] = useState(false);
  const [selectedTransaction, setSelectedTransaction] = useState<core.Transaction | null>(null);

  // Blockchain info from shared store (populated by useP2PEvents in MainLayout)
  const blockchainInfo = useStore((state) => state.blockchainInfo);

  // Status info state
  const [networkInfo, setNetworkInfo] = useState<core.NetworkInfo | null>(null);
  const [stakingInfo, setStakingInfo] = useState<core.StakingInfo | null>(null);
  const [statusLoading, setStatusLoading] = useState(false);

  // GUI settings state
  const [hideZeroBalances, setHideZeroBalances] = useState(false); // Synced from GUISettings on mount

  // Ref to track if component is mounted (for cleanup)
  const isMountedRef = useRef(true);

  // Maximum number of recent transactions to display
  const MAX_RECENT_TRANSACTIONS = 9;

  // Serialized transaction refresh: prevents concurrent fetches from producing
  // stale or accumulated results. Only one fetch runs at a time; if a new
  // request arrives while one is in-flight, it runs after the current completes.
  const txFetchInFlightRef = useRef(false);
  const txFetchPendingRef = useRef(false);

  const refreshTransactions = useCallback(async (showLoading = false) => {
    if (txFetchInFlightRef.current) {
      txFetchPendingRef.current = true;
      return;
    }
    txFetchInFlightRef.current = true;
    if (showLoading) setTxLoading(true);

    try {
      const txs = await GetRecentTransactions();
      if (isMountedRef.current) {
        setRecentTransactions(txs.map(tx => new core.Transaction(tx)).slice(0, MAX_RECENT_TRANSACTIONS));
      }
    } catch (error) {
      logger.error('OverviewPage: Failed to fetch recent transactions', error);
      if (isMountedRef.current) setRecentTransactions([]);
    } finally {
      if (showLoading) setTxLoading(false);
      txFetchInFlightRef.current = false;
      // If a request came in while we were fetching, run it now
      if (txFetchPendingRef.current && isMountedRef.current) {
        txFetchPendingRef.current = false;
        refreshTransactions(false);
      }
    }
  }, []);

  // Initial fetch with loading indicator
  const fetchRecentTransactions = useCallback(async () => {
    await refreshTransactions(true);
  }, [refreshTransactions]);

  // Fetch blockchain, network, and staking info
  const fetchStatusInfo = useCallback(async () => {
    try {
      setStatusLoading(true);
      logger.debug('OverviewPage: Fetching status info...');

      // Fetch network, staking info, and GUI settings in parallel
      // Note: blockchainInfo is fetched by useP2PEvents (MainLayout) and read from store
      const [network, staking, hideZero] = await Promise.all([
        GetNetworkInfo().catch(err => {
          logger.error('Failed to get network info:', err);
          return null;
        }),
        GetStakingInfo().catch(err => {
          logger.error('Failed to get staking info:', err);
          return null;
        }),
        GetSettingBool('fHideZeroBalances').catch(() => null),
      ]);

      // Only update state if component is still mounted
      if (isMountedRef.current) {
        if (network) {
          setNetworkInfo(new core.NetworkInfo(network));
        }
        if (staking) {
          setStakingInfo(new core.StakingInfo(staking));
        }
        if (hideZero !== null) {
          setHideZeroBalances(hideZero);
        }
        logger.debug('OverviewPage: Status info updated');
      }
    } catch (error) {
      logger.error('OverviewPage: Failed to fetch status info', error);
    } finally {
      if (isMountedRef.current) {
        setStatusLoading(false);
      }
    }
  }, []);

  // Silent refresh for auto-refresh interval: updates data without loading indicators
  // to prevent UI flicker every 10 seconds.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const silentRefresh = useCallback(() => {
    // Refresh network, staking info, and GUI settings without loading indicator
    // Note: blockchainInfo is refreshed by useP2PEvents (MainLayout) on P2P events and every 10s
    Promise.all([
      GetNetworkInfo().catch(err => { logger.debug('Silent refresh: network info failed', err); return null; }),
      GetStakingInfo().catch(err => { logger.debug('Silent refresh: staking info failed', err); return null; }),
      GetSettingBool('fHideZeroBalances').catch(() => null),
    ]).then(([network, staking, hideZero]) => {
      if (!isMountedRef.current) return;
      if (network) setNetworkInfo(new core.NetworkInfo(network));
      if (staking) setStakingInfo(new core.StakingInfo(staking));
      if (hideZero !== null) setHideZeroBalances(hideZero);
    });

    // Refresh balance without store loading indicator
    GetBalance().then(b => {
      if (b && isMountedRef.current) {
        useStore.getState().setBalance(new core.Balance(b));
      }
    }).catch(err => { logger.debug('Silent refresh: balance failed', err); });

    // Refresh transactions without txLoading indicator (serialized)
    refreshTransactions(false);
  }, []);

  // Load data on mount and set up auto-refresh
  useEffect(() => {
    isMountedRef.current = true;
    logger.debug('OverviewPage: Loading initial data...');

    // Initial data fetch (with loading indicators for first load)
    refreshBalance(true); // silent=true to suppress error notifications during startup
    fetchRecentTransactions();
    fetchStatusInfo();

    // Set up auto-refresh interval (10 seconds)
    // Uses silentRefresh to avoid loading indicator flicker.
    const statusInterval = setInterval(() => {
      if (isMountedRef.current) {
        silentRefresh();
      }
    }, STATUS_REFRESH_INTERVAL);

    // Cleanup on unmount
    return () => {
      isMountedRef.current = false;
      clearInterval(statusInterval);
    };
  }, [refreshBalance, fetchRecentTransactions, fetchStatusInfo, silentRefresh]);

  // Debounced silentRefresh to coalesce rapid P2P events (max once per second)
  const debounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const debouncedSilentRefresh = useCallback(() => {
    if (debounceTimerRef.current) return; // Already scheduled
    debounceTimerRef.current = setTimeout(() => {
      debounceTimerRef.current = null;
      silentRefresh();
    }, 1000);
  }, [silentRefresh]);

  // Subscribe to balance changes and P2P events from backend
  useEffect(() => {
    logger.debug('OverviewPage: Setting up event listeners...');

    // Listen for balance changes
    const unsubscribeBalance = EventsOn('balance:changed', (newBalance: any) => {
      logger.debug('OverviewPage: Balance changed event received', newBalance);
      const balanceInstance = new core.Balance(newBalance);
      useStore.getState().setBalance(balanceInstance);
    });

    // Listen for new transactions (silent refresh to avoid loading spinner flash)
    const unsubscribeTransaction = EventsOn('transaction:received', () => {
      logger.debug('OverviewPage: Transaction received event');
      refreshTransactions(false);
    });

    // Listen for P2P events to update status widgets in real-time (debounced)
    const unsubscribePeerCount = EventsOn('p2p:peer_count', () => {
      debouncedSilentRefresh();
    });
    const unsubscribeSyncing = EventsOn('p2p:syncing', () => {
      debouncedSilentRefresh();
    });
    const unsubscribeSynced = EventsOn('p2p:synced', () => {
      debouncedSilentRefresh();
    });
    const unsubscribeChainSync = EventsOn('chain:sync', () => {
      debouncedSilentRefresh();
    });

    // Listen for staking setting changes (from Options dialog or ToggleStaking)
    const unsubscribeStaking = EventsOn('staking:changed', () => {
      debouncedSilentRefresh();
    });

    return () => {
      unsubscribeBalance();
      unsubscribeTransaction();
      unsubscribePeerCount();
      unsubscribeSyncing();
      unsubscribeSynced();
      unsubscribeChainSync();
      unsubscribeStaking();
      if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedSilentRefresh, refreshTransactions]);

  // Determine sync status from real blockchain info (unknown = don't show badge until first poll)
  const isOutOfSync = blockchainInfo ? blockchainInfo.is_out_of_sync : false;

  return (
    <div className="qt-frame" style={{ width: '100%', height: '100%', margin: 0, padding: 0 }}>
      <div className="qt-vbox" style={{ margin: 0, padding: 0 }}>
        {/* Header */}
        <div style={{ paddingTop: '20px', paddingBottom: '10px', paddingLeft: '15px' }}>
          <div className="qt-header-label" style={{ fontSize: '16px', fontWeight: 'bold', letterSpacing: '0.5px' }}>
            {t('overview.title')}
          </div>
        </div>

        {/* Content */}
        <div className="qt-hbox" style={{ padding: '15px', gap: '30px', flex: 1 }}>
          {/* Left Column - Balances */}
          <div className="qt-vbox" style={{ flex: 1 }}>
            <CombinedBalanceCard
              balance={balance}
              isOutOfSync={isOutOfSync}
              isLoading={isLoading}
            />

            <TWINSBalanceCard
              balance={balance}
              showWatchOnly={false}
              hideZeroBalances={hideZeroBalances}
              isLoading={isLoading}
            />

            {/* Status Widgets */}
            <SyncStatusWidget
              blockchainInfo={blockchainInfo}
              isLoading={statusLoading}
            />

            <NetworkStatusWidget
              networkInfo={networkInfo}
              isLoading={statusLoading}
            />

            <StakingStatusWidget
              stakingInfo={stakingInfo}
              isLoading={statusLoading}
            />

          </div>

          {/* Right Column - Recent Transactions */}
          <div className="qt-vbox" style={{ flex: 1 }}>
            <div className="qt-frame-secondary" style={{ padding: '0', height: '100%', display: 'flex', flexDirection: 'column' }}>
              <div className="qt-hbox" style={{ alignItems: 'baseline', marginBottom: '8px' }}>
                <div className="qt-label" style={{ fontSize: '13px', fontWeight: 'normal' }}>{t('overview.recentTransactions')}</div>
                {isOutOfSync && <span className="qt-out-of-sync" style={{ marginLeft: '10px', fontSize: '12px', color: '#ff0000' }}>{t('common:status.outOfSync')}</span>}
              </div>

              {/* Horizontal line separator */}
              <div style={{ height: '1px', backgroundColor: '#555555', marginBottom: '10px' }} />

              {/* Transaction List */}
              <div style={{ flex: 1, minHeight: '400px', overflow: 'auto', position: 'relative' }}>
                {txLoading ? (
                  <LoadingSpinner message={t('common:loading.transactions')} overlay={true} />
                ) : (
                  <div className="qt-fade-in">
                    <TransactionList
                      transactions={recentTransactions}
                      limit={9}
                      onTransactionClick={(tx) => setSelectedTransaction(tx)}
                    />
                  </div>
                )}
              </div>
            </div>
          </div>
        </div>
      </div>

      <TransactionDetailsDialog
        isOpen={selectedTransaction !== null}
        transaction={selectedTransaction}
        onClose={() => setSelectedTransaction(null)}
      />
    </div>
  );
};

export default OverviewPage;
