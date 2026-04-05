import { create } from 'zustand';
import { useShallow } from 'zustand/react/shallow';
import { devtools, persist, subscribeWithSelector } from 'zustand/middleware';
import { immer } from 'zustand/middleware/immer';
import { enableMapSet } from 'immer';
import { createWalletSlice } from './slices/walletSlice';
import { createMasternodeSlice } from './slices/masternodeSlice';
import { createAppSlice } from './slices/appSlice';
import { createSendSlice } from './slices/sendSlice';
import { createCoinControlSlice } from './slices/coinControlSlice';
import { createReceiveSlice } from './slices/receiveSlice';
import { createTransactionsSlice } from './slices/transactionsSlice';
import { createExplorerSlice } from './slices/explorerSlice';
import { createOptionsSlice } from './slices/optionsSlice';
import { createToolsSlice } from './slices/toolsSlice';
import { createSignVerifySlice } from './slices/signVerifySlice';
import { createAddressBookSlice } from './slices/addressBookSlice';
import type { StoreState } from './store.types';

// Enable Immer's MapSet plugin to support Set and Map in state
// Required for coinControl.selectedCoins and coinControl.lockedCoins which are Sets
enableMapSet();

export type { StoreState };

export const useStore = create<StoreState>()(
  devtools(
    persist(
      subscribeWithSelector(
        immer((...a) => ({
          ...createWalletSlice(...a),
          ...createMasternodeSlice(...a),
          ...createAppSlice(...a),
          ...createSendSlice(...a),
          ...createCoinControlSlice(...a),
          ...createReceiveSlice(...a),
          ...createTransactionsSlice(...a),
          ...createExplorerSlice(...a),
          ...createOptionsSlice(...a),
          ...createToolsSlice(...a),
          ...createSignVerifySlice(...a),
          ...createAddressBookSlice(...a),
        }))
      ),
      {
        name: 'twins-wallet-store',
        partialize: (state) => ({
          // Only persist specific parts of the state
          settings: state.settings,
          addresses: state.addresses,
          // Persist coin control view preferences
          viewMode: state.viewMode,
          filterMode: state.filterMode,
          sortMode: state.sortMode,
          sortAscending: state.sortAscending,
          // Persist display unit settings for instant availability on reload
          displayUnit: state.displayUnit,
          displayDigits: state.displayDigits,
        }),
      }
    ),
    {
      name: 'TWINS Wallet',
    }
  )
);

// Typed hooks for common selections
// Zustand v5 requires useShallow for selectors returning new object literals,
// otherwise useSyncExternalStore triggers infinite re-renders.
export const useWallet = () =>
  useStore(useShallow((state) => ({
    balance: state.balance,
    addresses: state.addresses,
    transactions: state.transactions,
  })));

export const useMasternodes = () =>
  useStore(useShallow((state) => ({
    // State - My Masternodes
    masternodes: state.masternodes,
    selectedMasternode: state.selectedMasternode,
    isLoading: state.isLoading,
    isStartingMasternode: state.isStartingMasternode,
    lastRefresh: state.lastRefresh,
    operationError: state.operationError,
    operationSuccess: state.operationSuccess,
    // State - Network Masternodes
    networkMasternodes: state.networkMasternodes,
    isLoadingNetwork: state.isLoadingNetwork,
    networkLastRefresh: state.networkLastRefresh,
    networkFilters: state.networkFilters,
    masternodeActiveTab: state.masternodeActiveTab,
    // Actions - My Masternodes
    selectMasternode: state.selectMasternode,
    setMasternodes: state.setMasternodes,
    setLoading: state.setLoading,
    setStartingMasternode: state.setStartingMasternode,
    setLastRefresh: state.setLastRefresh,
    setOperationError: state.setOperationError,
    setOperationSuccess: state.setOperationSuccess,
    clearOperationMessages: state.clearOperationMessages,
    // Actions - Network Masternodes
    setNetworkMasternodes: state.setNetworkMasternodes,
    setLoadingNetwork: state.setLoadingNetwork,
    setNetworkLastRefresh: state.setNetworkLastRefresh,
    setNetworkFilters: state.setNetworkFilters,
    setMasternodeActiveTab: state.setMasternodeActiveTab,
    // Computed - My Masternodes
    getActiveMasternodes: state.getActiveMasternodes,
    getMasternodesByStatus: state.getMasternodesByStatus,
    getTotalRewards: state.getTotalRewards,
    getMissingMasternodes: state.getMissingMasternodes,
    // Computed - Network Masternodes
    getFilteredNetworkMasternodes: state.getFilteredNetworkMasternodes,
    getNetworkMasternodeCount: state.getNetworkMasternodeCount,
  })));

export const useConnection = () =>
  useStore((state) => state.connectionStatus);

export const useSettings = () =>
  useStore((state) => state.settings);

export const useNotifications = () =>
  useStore(useShallow((state) => ({
    notifications: state.notifications,
    addNotification: state.addNotification,
    removeNotification: state.removeNotification,
  })));

export const useSend = () =>
  useStore(useShallow((state) => ({
    recipients: state.recipients,
    sendCoinControlConfig: state.sendCoinControlConfig,
    addRecipient: state.addRecipient,
    removeRecipient: state.removeRecipient,
    updateRecipient: state.updateRecipient,
    resetSendForm: state.resetSendForm,
  })));

export const useCoinControl = () =>
  useStore(useShallow((state) => ({
    utxos: state.utxos,
    isLoadingUTXOs: state.isLoadingUTXOs,
    coinControl: state.coinControl,
    viewMode: state.viewMode,
    filterMode: state.filterMode,
    sortMode: state.sortMode,
    sortAscending: state.sortAscending,
    isDialogOpen: state.isCoinControlDialogOpen,
    expandedAddresses: state.expandedAddresses,
    summary: state.summary,
    loadUTXOs: state.loadUTXOs,
    selectCoin: state.selectCoin,
    unselectCoin: state.unselectCoin,
    selectAll: state.selectAllCoins,  // Map to renamed function to avoid transactionsSlice conflict
    unselectAll: state.unselectAllCoins,  // Map to renamed function to avoid transactionsSlice conflict
    toggleCoinSelection: state.toggleCoinSelection,
    lockCoin: state.lockCoin,
    unlockCoin: state.unlockCoin,
    toggleCoinLock: state.toggleCoinLock,
    toggleAllLocks: state.toggleAllLocks,
    setViewMode: state.setViewMode,
    setFilterMode: state.setFilterMode,
    setSortMode: state.setSortMode,
    openDialog: state.openDialog,
    closeDialog: state.closeDialog,
    cancelDialog: state.cancelDialog,
    calculateSummary: state.calculateSummary,
    resetCoinControl: state.resetCoinControl,
    // Tree view actions
    toggleAddressExpanded: state.toggleAddressExpanded,
    expandAllAddresses: state.expandAllAddresses,
    collapseAllAddresses: state.collapseAllAddresses,
    selectAddressCoins: state.selectAddressCoins,
    unselectAddressCoins: state.unselectAddressCoins,
    buildTreeView: state.buildTreeView,
  })));

export const useReceive = () =>
  useStore(useShallow((state) => ({
    // State
    currentAddress: state.currentAddress,
    receivingAddresses: state.receivingAddresses,
    paymentRequests: state.paymentRequests,
    reuseAddress: state.reuseAddress,
    formState: state.formState,
    isAddressesDialogOpen: state.isAddressesDialogOpen,
    isRequestDialogOpen: state.isRequestDialogOpen,
    selectedRequest: state.selectedRequest,
    isLoading: state.isLoading,
    isGeneratingAddress: state.isGeneratingAddress,
    isCreatingRequest: state.isCreatingRequest,
    error: state.error,
    // Actions
    setCurrentAddress: state.setCurrentAddress,
    setReceivingAddresses: state.setReceivingAddresses,
    addReceivingAddress: state.addReceivingAddress,
    setPaymentRequests: state.setPaymentRequests,
    addPaymentRequest: state.addPaymentRequest,
    removePaymentRequest: state.removePaymentRequest,
    setReuseAddress: state.setReuseAddress,
    updateFormField: state.updateFormField,
    clearForm: state.clearForm,
    openAddressesDialog: state.openAddressesDialog,
    closeAddressesDialog: state.closeAddressesDialog,
    openRequestDialog: state.openRequestDialog,
    closeRequestDialog: state.closeRequestDialog,
    fetchReceivingAddresses: state.fetchReceivingAddresses,
    fetchPaymentRequests: state.fetchPaymentRequests,
    fetchCurrentAddress: state.fetchCurrentAddress,
    generateNewAddress: state.generateNewAddress,
    createPaymentRequest: state.createPaymentRequest,
    deletePaymentRequest: state.deletePaymentRequest,
    setError: state.setError,
    clearError: state.clearError,
    resetReceiveState: state.resetReceiveState,
  })));

export const useTransactions = () =>
  useStore(useShallow((state) => ({
    // Current page data (from server)
    transactions: state.transactions,
    total: state.total,
    totalAll: state.totalAll,
    totalPages: state.totalPages,
    isLoading: state.isLoadingTransactions,
    error: state.transactionsError,

    // Pagination
    currentPage: state.currentPage,
    pageSize: state.pageSize,

    // Filter state
    dateFilter: state.dateFilter,
    typeFilter: state.typeFilter,
    searchText: state.searchText,
    minAmount: state.minAmount,
    dateRangeFrom: state.dateRangeFrom,
    dateRangeTo: state.dateRangeTo,
    watchOnlyFilter: state.watchOnlyFilter,
    hasWatchOnlyAddresses: state.hasWatchOnlyAddresses,
    hideOrphanStakes: state.hideOrphanStakes,

    // Sort state
    sortColumn: state.sortColumn,
    sortDirection: state.sortDirection,

    // Selection state
    selectedTxids: state.selectedTxids,

    // Notification
    newTransactionCount: state.newTransactionCount,

    // Block explorer URLs
    blockExplorerUrls: state.blockExplorerUrls,

    // Data loading
    fetchPage: state.fetchPage,

    // Pagination actions
    setPage: state.setPage,
    setPageSize: state.setPageSize,
    goToFirstPage: state.goToFirstPage,
    goToLastPage: state.goToLastPage,
    goToPrevPage: state.goToPrevPage,
    goToNextPage: state.goToNextPage,

    // Filter actions
    setDateFilter: state.setDateFilter,
    setTypeFilter: state.setTypeFilter,
    setSearchText: state.setSearchText,
    setMinAmount: state.setMinAmount,
    setDateRange: state.setDateRange,
    setWatchOnlyFilter: state.setWatchOnlyFilter,
    syncHideOrphanStakes: state.syncHideOrphanStakes,
    syncBlockExplorerUrls: state.syncBlockExplorerUrls,
    clearFilters: state.clearFilters,

    // Sort actions
    setSortColumn: state.setSortColumn,
    toggleSortDirection: state.toggleSortDirection,

    // Selection actions
    toggleSelection: state.toggleSelection,
    selectAll: state.selectAll,
    unselectAll: state.unselectAll,
    isSelected: state.isSelected,
    getSelectedAmount: state.getSelectedAmount,
    getSelectedCount: state.getSelectedCount,

    // Export
    exportCSV: state.exportCSV,

    // Notification actions
    incrementNewTransactionCount: state.incrementNewTransactionCount,
    clearNewTransactionCount: state.clearNewTransactionCount,
  })));

export const useTools = () =>
  useStore(useShallow((state) => ({
    isToolsDialogOpen: state.isToolsDialogOpen,
    toolsActiveTab: state.toolsActiveTab,
    openToolsDialog: state.openToolsDialog,
    closeToolsDialog: state.closeToolsDialog,
    setToolsActiveTab: state.setToolsActiveTab,
    lastRepairResult: state.lastRepairResult,
    setLastRepairResult: state.setLastRepairResult,
  })));

export const useSignVerify = () =>
  useStore(useShallow((state) => ({
    isSignVerifyDialogOpen: state.isSignVerifyDialogOpen,
    signVerifyActiveTab: state.signVerifyActiveTab,
    openSignVerifyDialog: state.openSignVerifyDialog,
    closeSignVerifyDialog: state.closeSignVerifyDialog,
    setSignVerifyActiveTab: state.setSignVerifyActiveTab,
  })));

export const useAddressBook = () =>
  useStore(useShallow((state) => ({
    contacts: state.addressBookContacts,
    isLoading: state.isAddressBookLoading,
    isDialogOpen: state.isAddressBookDialogOpen,
    mode: state.addressBookMode,
    searchFilter: state.addressBookSearchFilter,
    fetchContacts: state.fetchContacts,
    addContact: state.addContact,
    editContact: state.editContact,
    deleteContact: state.deleteContact,
    openAddressBookDialog: state.openAddressBookDialog,
    closeAddressBookDialog: state.closeAddressBookDialog,
    setSearchFilter: state.setAddressBookSearchFilter,
  })));

export const useOptions = () =>
  useStore(useShallow((state) => ({
    // State
    isDialogOpen: state.isDialogOpen,
    activeTab: state.activeTab,
    workingSettings: state.workingSettings,
    originalSettings: state.originalSettings,
    metadata: state.metadata,
    availableThemes: state.availableThemes,
    isLoading: state.isLoading,
    isSaving: state.isSaving,
    error: state.error,
    dirtyFields: state.dirtyFields,
    restartRequired: state.restartRequired,
    appliedRestartPending: state.appliedRestartPending,
    platform: state.platform,
    // Daemon config state
    daemonValues: state.daemonValues,
    daemonMetadata: state.daemonMetadata,
    daemonCategories: state.daemonCategories,
    pendingDaemonChanges: state.pendingDaemonChanges,
    // Actions
    openOptionsDialog: state.openOptionsDialog,
    closeOptionsDialog: state.closeOptionsDialog,
    setActiveTab: state.setActiveTab,
    loadSettings: state.loadSettings,
    updateSetting: state.updateSetting,
    applySettings: state.applySettings,
    resetToDefaults: state.resetToDefaults,
    discardChanges: state.discardChanges,
    updateDaemonSetting: state.updateDaemonSetting,
    getDaemonWorkingValue: state.getDaemonWorkingValue,
    restartApp: state.restartApp,
    // Computed (derived as booleans to make re-render dependency explicit)
    isDirty: state.dirtyFields.size > 0 || Object.keys(state.pendingDaemonChanges).length > 0,
    hasPendingRestartChanges: Object.keys(state.pendingDaemonChanges).some(key => {
      const meta = state.daemonMetadata.find(m => m.key === key);
      return meta !== undefined && !meta.hotReload;
    }),
    getSettingMetadata: state.getSettingMetadata,
    isSettingOverridden: state.isSettingOverridden,
    isRestartRequiredForSetting: state.isRestartRequiredForSetting,
  })));