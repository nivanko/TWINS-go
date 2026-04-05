import type { SliceCreator } from '../store.types';
import type { config } from '../../../wailsjs/go/models';

// Types matching backend GUISettings struct
export interface GUISettings {
  // Window/UI Settings
  fMinimizeToTray: boolean;
  fMinimizeOnClose: boolean;
  nDisplayUnit: number;
  theme: string;
  digits: number;
  language: string;
  fHideTrayIcon: boolean;
  fShowMasternodesTab: boolean;
  strThirdPartyTxUrls: string;

  // Wallet Settings
  nStakeSplitThreshold: number;
  fAutoCombineRewards: boolean;

  // Coin Control Settings
  fCoinControlFeatures: boolean;
  nCoinControlMode: number;
  nCoinControlSortColumn: number;
  nCoinControlSortOrder: number;

  // Transaction View Settings
  transactionDate: number;
  transactionType: number;
  transactionMinAmount: number;
  fHideOrphans: boolean;
  fHideZeroBalances: boolean;

  // Send Coins Dialog Settings
  fFeeSectionMinimized: boolean;
  nFeeRadio: number;
  nCustomFeeRadio: number;
  nSmartFeeSliderPosition: number;
  nTransactionFee: number;
  fPayOnlyMinFee: boolean;
  fSendFreeTransactions: boolean;
  fSubtractFeeFromAmount: boolean;

  // Misc
  fRestartRequired: boolean;
  strDataDir: string;
}

export interface ThemeInfo {
  name: string;
  isBuiltIn: boolean;
  path?: string;
}

export interface SettingMetadata {
  key: string;
  tab: string;
  requiresRestart: boolean;
  overriddenByCLI: boolean;
  cliFlagName?: string;
  defaultValue?: unknown;
  minValue?: number;
  maxValue?: number;
  deprecated?: boolean;
  deprecatedMsg?: string;
}

export type DaemonSettingValue = { value: unknown; locked: boolean; pendingRestart: boolean };

export interface OptionsState {
  // Dialog state
  isDialogOpen: boolean;
  activeTab: number;

  // GUI settings data
  workingSettings: Partial<GUISettings>;
  originalSettings: Partial<GUISettings>;
  metadata: Record<string, SettingMetadata>;
  availableThemes: ThemeInfo[];

  // Daemon config data
  daemonValues: Record<string, DaemonSettingValue>;
  daemonMetadata: config.SettingMeta[];
  daemonCategories: string[];
  pendingDaemonChanges: Record<string, unknown>;

  // UI state
  isLoading: boolean;
  isSaving: boolean;
  error: string | null;
  dirtyFields: Set<string>;
  restartRequired: boolean;
  appliedRestartPending: boolean;

  // Platform info
  platform: string; // "darwin", "linux", "windows"
}

export interface OptionsActions {
  // Dialog management
  openOptionsDialog: () => void;
  closeOptionsDialog: () => void;
  setActiveTab: (tab: number) => void;

  // GUI settings operations
  loadSettings: () => Promise<void>;
  updateSetting: (key: string, value: unknown) => void;
  applySettings: () => Promise<boolean>;
  resetToDefaults: () => Promise<void>;
  discardChanges: () => void;

  // Daemon settings operations
  updateDaemonSetting: (key: string, value: unknown) => void;
  getDaemonWorkingValue: (key: string) => unknown;
  hasPendingRestartChanges: () => boolean;

  // Restart
  restartApp: () => Promise<void>;

  // Computed
  isDirty: () => boolean;
  getSettingMetadata: (key: string) => SettingMetadata | undefined;
  isSettingOverridden: (key: string) => boolean;
  isRestartRequiredForSetting: (key: string) => boolean;
}

export type OptionsSlice = OptionsState & OptionsActions;

const initialState: OptionsState = {
  isDialogOpen: false,
  activeTab: 0,
  workingSettings: {},
  originalSettings: {},
  metadata: {},
  availableThemes: [],
  daemonValues: {},
  daemonMetadata: [],
  daemonCategories: [],
  pendingDaemonChanges: {},
  isLoading: false,
  isSaving: false,
  error: null,
  dirtyFields: new Set(),
  restartRequired: false,
  appliedRestartPending: false,
  platform: '',
};

export const createOptionsSlice: SliceCreator<OptionsSlice> = (set, get) => ({
  ...initialState,

  // Dialog management
  openOptionsDialog: () => {
    set({ isDialogOpen: true, activeTab: 0 });
    get().loadSettings();
  },

  closeOptionsDialog: () => {
    set({
      isDialogOpen: false,
      error: null,
      dirtyFields: new Set(),
      pendingDaemonChanges: {},
    });
  },

  setActiveTab: (tab: number) => {
    set({ activeTab: tab });
  },

  // Settings operations
  loadSettings: async () => {
    set({ isLoading: true, error: null });

    try {
      const {
        GetSettings,
        GetAllSettingsMetadata,
        GetAvailableThemes,
        GetDaemonConfigMetadata,
        GetDaemonConfigValues,
        GetDaemonConfigCategories,
        GetPlatform,
      } = await import('@wailsjs/go/main/App');

      const [settings, metadata, themes, daemonMeta, daemonVals, daemonCats, platform] = await Promise.all([
        GetSettings(),
        GetAllSettingsMetadata(),
        GetAvailableThemes(),
        GetDaemonConfigMetadata(),
        GetDaemonConfigValues(),
        GetDaemonConfigCategories(),
        GetPlatform(),
      ]);

      // Check if any daemon settings have pending restart (from a previous Apply)
      const daemonValsTyped = (daemonVals || {}) as Record<string, DaemonSettingValue>;
      const hasDaemonRestartPending = Object.values(daemonValsTyped).some(v => v.pendingRestart);

      set({
        workingSettings: settings as Partial<GUISettings>,
        originalSettings: settings as Partial<GUISettings>,
        metadata: metadata as Record<string, SettingMetadata>,
        availableThemes: themes as ThemeInfo[],
        daemonMetadata: (daemonMeta || []) as config.SettingMeta[],
        daemonValues: daemonValsTyped,
        daemonCategories: (daemonCats || []) as string[],
        pendingDaemonChanges: {},
        isLoading: false,
        dirtyFields: new Set(),
        restartRequired: false,
        appliedRestartPending: hasDaemonRestartPending,
        platform: platform as string,
      });
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : 'Failed to load settings';
      set({ isLoading: false, error: errorMsg });
    }
  },

  updateSetting: (key: string, value: unknown) => {
    const state = get();
    const newWorkingSettings = { ...state.workingSettings, [key]: value };
    const newDirtyFields = new Set(state.dirtyFields);

    if (state.originalSettings[key as keyof GUISettings] !== value) {
      newDirtyFields.add(key);
    } else {
      newDirtyFields.delete(key);
    }

    let restartRequired = false;
    for (const field of newDirtyFields) {
      const meta = state.metadata[field];
      if (meta?.requiresRestart) {
        restartRequired = true;
        break;
      }
    }

    set({
      workingSettings: newWorkingSettings,
      dirtyFields: newDirtyFields,
      restartRequired,
    });
  },

  updateDaemonSetting: (key: string, value: unknown) => {
    const state = get();
    set({
      pendingDaemonChanges: { ...state.pendingDaemonChanges, [key]: value },
    });
  },

  getDaemonWorkingValue: (key: string) => {
    const state = get();
    return state.pendingDaemonChanges[key] !== undefined
      ? state.pendingDaemonChanges[key]
      : state.daemonValues[key]?.value;
  },

  hasPendingRestartChanges: () => {
    const state = get();
    for (const key of Object.keys(state.pendingDaemonChanges)) {
      const meta = state.daemonMetadata.find(m => m.key === key);
      if (meta && !meta.hotReload) {
        return true;
      }
    }
    return false;
  },

  applySettings: async () => {
    const state = get();
    const hasDirtyGUI = state.dirtyFields.size > 0;
    const hasDirtyDaemon = Object.keys(state.pendingDaemonChanges).length > 0;

    if (!hasDirtyGUI && !hasDirtyDaemon) {
      return true;
    }

    const dirtyFieldsSnapshot = Array.from(state.dirtyFields);
    const workingSettingsSnapshot = { ...state.workingSettings };
    const pendingDaemonSnapshot = { ...state.pendingDaemonChanges };

    set({ isSaving: true, error: null });

    try {
      const { UpdateSettings, SetDaemonConfigValue, GetDaemonConfigValues } = await import('@wailsjs/go/main/App');

      // Save GUI settings
      if (hasDirtyGUI) {
        const changedSettings: Record<string, unknown> = {};
        for (const key of dirtyFieldsSnapshot) {
          changedSettings[key] = workingSettingsSnapshot[key as keyof GUISettings];
        }
        await UpdateSettings(changedSettings);

        // Hot reload language if changed
        if ('language' in changedSettings && changedSettings.language) {
          const newLang = changedSettings.language as string;
          localStorage.setItem('twins-language', newLang);
          const { loadLanguage } = await import('../../i18n/lazyLoader');
          await loadLanguage(newLang);
        }

        // Hot reload tray icon visibility if changed
        if ('fHideTrayIcon' in changedSettings) {
          const { SetTrayIconVisible } = await import('@wailsjs/go/main/App');
          await SetTrayIconVisible(!changedSettings.fHideTrayIcon);
        }

        // Hot reload display unit settings
        if ('nDisplayUnit' in changedSettings) {
          set({ displayUnit: changedSettings.nDisplayUnit as number });
        }
        if ('digits' in changedSettings) {
          set({ displayDigits: changedSettings.digits as number });
        }
      }

      // Save daemon settings one by one; wrap errors with the key that failed
      for (const [key, value] of Object.entries(pendingDaemonSnapshot)) {
        try {
          await SetDaemonConfigValue(key, value);
        } catch (e) {
          const detail = typeof e === 'string' ? e : (e instanceof Error ? e.message : String(e));
          throw new Error(`${key}: ${detail}`);
        }
      }

      // Reload daemon values to get canonical backend state
      const newDaemonVals = hasDirtyDaemon
        ? (await GetDaemonConfigValues()) as Record<string, DaemonSettingValue>
        : state.daemonValues;

      // Determine if this apply included restart-requiring settings
      let appliedRestartPending = get().appliedRestartPending;
      if (hasDirtyGUI) {
        const hasGUIRestart = dirtyFieldsSnapshot.some(key => state.metadata[key]?.requiresRestart);
        if (hasGUIRestart) appliedRestartPending = true;
      }
      if (hasDirtyDaemon) {
        const hasDaemonRestart = Object.keys(pendingDaemonSnapshot).some(key => {
          const meta = state.daemonMetadata.find(m => m.key === key);
          return meta !== undefined && !meta.hotReload;
        });
        if (hasDaemonRestart) appliedRestartPending = true;
      }

      set({
        originalSettings: workingSettingsSnapshot,
        dirtyFields: new Set(),
        pendingDaemonChanges: {},
        daemonValues: newDaemonVals,
        isSaving: false,
        appliedRestartPending,
      });

      // Notify transaction slice when relevant settings change
      if (hasDirtyGUI) {
        // Use dynamic import to avoid circular dependency between slices
        if (dirtyFieldsSnapshot.includes('fHideOrphans') || dirtyFieldsSnapshot.includes('strThirdPartyTxUrls')) {
          const { useStore } = await import('../useStore');
          if (dirtyFieldsSnapshot.includes('fHideOrphans')) {
            useStore.getState().syncHideOrphanStakes();
          }
          if (dirtyFieldsSnapshot.includes('strThirdPartyTxUrls')) {
            useStore.getState().syncBlockExplorerUrls();
          }
        }
      }

      // Notify app slice when coin control features setting changes
      if (hasDirtyGUI && dirtyFieldsSnapshot.includes('fCoinControlFeatures')) {
        const { useStore } = await import('../useStore');
        useStore.getState().syncCoinControlEnabled();
      }

      return true;
    } catch (error) {
      // Wails Go errors arrive as plain strings, not Error objects
      const errorMsg = error instanceof Error ? error.message : (typeof error === 'string' && error ? error : 'Failed to save settings');
      set({ isSaving: false, error: errorMsg });
      return false;
    }
  },

  resetToDefaults: async () => {
    // Snapshot before any await to avoid stale-closure reads
    const { daemonMetadata, daemonValues, workingSettings: previousSettings } = get();
    set({ isSaving: true, error: null });

    try {
      const { ResetSettings, GetSettings, SetDaemonConfigValue, GetDaemonConfigValues } = await import('@wailsjs/go/main/App');

      // Reset GUI settings
      await ResetSettings();
      const newSettings = await GetSettings();

      // Reset daemon settings to defaults from metadata
      for (const meta of daemonMetadata) {
        if (meta.default !== undefined && !daemonValues[meta.key]?.locked) {
          try {
            await SetDaemonConfigValue(meta.key, meta.default);
          } catch {
            // Ignore per-key failures (e.g. locked settings)
          }
        }
      }
      const newDaemonVals = await GetDaemonConfigValues();

      // Only show restart banner if any non-hot-reload daemon settings were actually changed from their current value.
      // Use String() coercion on both sides to guard against string/number type mismatches in the JSON wire protocol.
      // Edge case: String(null) === "null" and String("null") === "null", so a null default compared to the string
      // "null" on the wire (or vice versa) would be masked. This is an intentionally accepted trade-off — the daemon
      // config surface does not use null defaults, and the Wails JSON bridge preserves null as null.
      const daemonRestartRequired = daemonMetadata.some(
        meta =>
          meta.default !== undefined &&
          !daemonValues[meta.key]?.locked &&
          !meta.hotReload &&
          String(daemonValues[meta.key]?.value) !== String(meta.default),
      );

      set({
        workingSettings: newSettings as Partial<GUISettings>,
        originalSettings: newSettings as Partial<GUISettings>,
        dirtyFields: new Set(),
        pendingDaemonChanges: {},
        daemonValues: newDaemonVals as Record<string, DaemonSettingValue>,
        restartRequired: daemonRestartRequired,
        appliedRestartPending: daemonRestartRequired || get().appliedRestartPending,
        isSaving: false,
        // Reset display units to defaults
        displayUnit: (newSettings as any).nDisplayUnit ?? 0,
        displayDigits: (newSettings as any).digits ?? 8,
      });

      // Hot reload language if it changed from previous value
      const newLang = (newSettings as any).language as string | undefined;
      if (newLang !== undefined && newLang !== previousSettings.language) {
        const { loadLanguage } = await import('../../i18n/lazyLoader');
        if (newLang) {
          localStorage.setItem('twins-language', newLang);
          await loadLanguage(newLang);
        } else {
          // Empty string = system default = English
          localStorage.removeItem('twins-language');
          await loadLanguage('en');
        }
      }

      // Hot reload tray icon visibility if it changed
      const newHideTray = (newSettings as any).fHideTrayIcon;
      if (newHideTray !== undefined && newHideTray !== previousSettings.fHideTrayIcon) {
        const { SetTrayIconVisible } = await import('@wailsjs/go/main/App');
        await SetTrayIconVisible(!newHideTray);
      }

      // Sync reactive store state for settings consumed outside the options dialog
      const { useStore } = await import('../useStore');
      useStore.getState().syncHideOrphanStakes();
      useStore.getState().syncCoinControlEnabled();

      // Show success notification
      useStore.getState().addNotification({
        type: 'success',
        title: 'Settings',
        message: 'Settings reset to defaults',
        duration: 3000,
      });
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : (typeof error === 'string' && error ? error : 'Failed to reset settings');
      set({ isSaving: false, error: errorMsg });
    }
  },

  discardChanges: () => {
    const state = get();
    set({
      workingSettings: { ...state.originalSettings },
      dirtyFields: new Set(),
      pendingDaemonChanges: {},
      restartRequired: false,
      error: null,
    });
  },

  // Restart
  restartApp: async () => {
    const { RestartApp } = await import('@wailsjs/go/main/App');
    await RestartApp();
  },

  // Computed
  isDirty: () => {
    const state = get();
    return state.dirtyFields.size > 0 || Object.keys(state.pendingDaemonChanges).length > 0;
  },

  getSettingMetadata: (key: string) => {
    return get().metadata[key];
  },

  isSettingOverridden: (key: string) => {
    const meta = get().metadata[key];
    return meta?.overriddenByCLI ?? false;
  },

  isRestartRequiredForSetting: (key: string) => {
    const meta = get().metadata[key];
    return meta?.requiresRestart ?? false;
  },
});
