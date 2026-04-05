import React, { useEffect, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { X, AlertTriangle, RotateCcw, RefreshCw } from 'lucide-react';
import { useOptions } from '../../../store/useStore';
import { GeneralTab, WindowTab, DaemonTab } from './tabs';
import { SimpleConfirmDialog } from '../../../shared/components/SimpleConfirmDialog';
import { RestartingOverlay } from '../../../shared/components/RestartingOverlay';


interface Tab {
  id: string;
  label: string;
}

const TAB_IDS = ['general', 'daemon', 'window'] as const;

export const OptionsDialog: React.FC = () => {
  const { t } = useTranslation('settings');
  const {
    isDialogOpen,
    activeTab,
    workingSettings,
    metadata,
    availableThemes,
    isLoading,
    isSaving,
    error,
    restartRequired,
    platform,
    daemonValues,
    daemonMetadata,
    pendingDaemonChanges,
    closeOptionsDialog,
    setActiveTab,
    updateSetting,
    updateDaemonSetting,
    applySettings,
    resetToDefaults,
    discardChanges,
    isDirty,
    hasPendingRestartChanges,
    appliedRestartPending,
    restartApp,
  } = useOptions();

  const [showRestartConfirm, setShowRestartConfirm] = useState(false);
  const [isRestarting, setIsRestarting] = useState(false);

  const handleRestart = useCallback(() => {
    setShowRestartConfirm(true);
  }, []);

  const handleConfirmRestart = useCallback(async () => {
    setShowRestartConfirm(false);
    setIsRestarting(true);
    await restartApp();
  }, [restartApp]);

  const handleCancel = useCallback(() => {
    discardChanges();
    closeOptionsDialog();
  }, [discardChanges, closeOptionsDialog]);

  const handleApply = useCallback(async () => {
    await applySettings();
  }, [applySettings]);

  const handleOK = useCallback(async () => {
    const success = await applySettings();
    if (success) {
      closeOptionsDialog();
    }
  }, [applySettings, closeOptionsDialog]);

  const [showResetConfirm, setShowResetConfirm] = useState(false);

  const handleResetClick = useCallback(() => {
    setShowResetConfirm(true);
  }, []);

  const handleResetConfirm = useCallback(async () => {
    setShowResetConfirm(false);
    await resetToDefaults();
  }, [resetToDefaults]);

  // Handle Escape key to close dialog
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isDialogOpen && !showRestartConfirm) {
        handleCancel();
      }
    };

    if (isDialogOpen) {
      document.addEventListener('keydown', handleKeyDown);
    }

    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [isDialogOpen, handleCancel, showRestartConfirm]);

  if (!isDialogOpen) {
    return null;
  }

  // Build tabs with translated labels
  const visibleTabs: Tab[] = TAB_IDS.map(id => ({
    id,
    label: t(`tabs.${id}`),
  }));

  const daemonProps = {
    metadata: daemonMetadata,
    daemonValues,
    pendingChanges: pendingDaemonChanges,
    onChange: updateDaemonSetting,
  };

  const renderTabContent = () => {
    const tabId = visibleTabs[activeTab]?.id;

    switch (tabId) {
      case 'general':
        return (
          <GeneralTab
            settings={workingSettings}
            metadata={metadata}
            themes={availableThemes}
            onChange={updateSetting}
          />
        );
      case 'daemon':
        return <DaemonTab {...daemonProps} />;
      case 'window':
        return (
          <WindowTab
            settings={workingSettings}
            metadata={metadata}
            onChange={updateSetting}
            platform={platform}
          />
        );
      default:
        return null;
    }
  };

  const isDirtyNow = isDirty;
  const showRestartBanner = restartRequired || hasPendingRestartChanges || appliedRestartPending;
  const canRestart = appliedRestartPending && !isDirtyNow;

  return (
    <>
      {/* Backdrop */}
      <div
        style={{
          position: 'fixed',
          inset: 0,
          backgroundColor: 'rgba(0, 0, 0, 0.6)',
          zIndex: 1000,
        }}
        onClick={handleCancel}
      />

      {/* Dialog */}
      <div
        style={{
          position: 'fixed',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          width: '620px',
          height: '80vh',
          minHeight: '480px',
          backgroundColor: '#2b2b2b',
          borderRadius: '8px',
          boxShadow: '0 4px 20px rgba(0, 0, 0, 0.5)',
          zIndex: 1001,
          display: 'flex',
          flexDirection: 'column',
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            padding: '16px 20px',
            borderBottom: '1px solid #444',
          }}
        >
          <h2 style={{ margin: 0, color: '#fff', fontSize: '18px', fontWeight: 500 }}>
            {t('title')}
          </h2>
          <button
            onClick={handleCancel}
            style={{
              background: 'none',
              border: 'none',
              color: '#888',
              cursor: 'pointer',
              padding: '4px',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            <X size={20} />
          </button>
        </div>

        {/* Tab Bar */}
        <div
          style={{
            display: 'flex',
            borderBottom: '1px solid #444',
            backgroundColor: '#333',
            flexShrink: 0,
          }}
        >
          {visibleTabs.map((tab, index) => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(index)}
              style={{
                flex: 1,
                padding: '10px 16px',
                backgroundColor: activeTab === index ? '#2b2b2b' : 'transparent',
                border: 'none',
                borderBottom: activeTab === index ? '2px solid #4a9eff' : '2px solid transparent',
                color: activeTab === index ? '#fff' : '#aaa',
                cursor: 'pointer',
                fontSize: '13px',
                fontWeight: activeTab === index ? 500 : 400,
                transition: 'all 0.15s ease',
                whiteSpace: 'nowrap',
              }}
            >
              {tab.label}
            </button>
          ))}
        </div>

        {/* Content Area */}
        <div
          style={{
            flex: 1,
            overflowY: 'auto',
            overflowX: 'hidden',
          }}
        >
          {isLoading ? (
            <div
              style={{
                display: 'flex',
                justifyContent: 'center',
                alignItems: 'center',
                height: '200px',
                color: '#888',
              }}
            >
              {t('common:status.loading')}
            </div>
          ) : (
            renderTabContent()
          )}
        </div>

        {/* Restart Warning Banner */}
        {showRestartBanner && (
          <div
            style={{
              padding: '10px 20px',
              backgroundColor: '#4a2a00',
              borderTop: '1px solid #664400',
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
            }}
          >
            <AlertTriangle size={16} style={{ color: '#ffa500', flexShrink: 0 }} />
            <span style={{ color: '#ffcc00', fontSize: '13px', flex: 1 }}>
              {t('messages.restartRequired')}
            </span>
            <button
              onClick={handleRestart}
              disabled={!canRestart}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '5px',
                padding: '5px 12px',
                backgroundColor: canRestart ? '#cc7700' : 'transparent',
                border: `1px solid ${canRestart ? '#cc7700' : '#665500'}`,
                borderRadius: '4px',
                color: canRestart ? '#fff' : '#887744',
                cursor: canRestart ? 'pointer' : 'not-allowed',
                fontSize: '12px',
                fontWeight: 500,
                flexShrink: 0,
                opacity: canRestart ? 1 : 0.6,
              }}
            >
              <RefreshCw size={12} />
              {t('messages.restartButton')}
            </button>
          </div>
        )}

        {/* Error Banner */}
        {error && (
          <div
            style={{
              padding: '10px 20px',
              backgroundColor: '#4a0000',
              borderTop: '1px solid #660000',
              color: '#ff6666',
              fontSize: '13px',
            }}
          >
            Error: {error}
          </div>
        )}

        {/* Footer — unified for all tabs */}
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            padding: '16px 20px',
            borderTop: '1px solid #444',
            backgroundColor: '#333',
          }}
        >
          {/* Reset Button (left side) */}
          <button
            onClick={handleResetClick}
            disabled={isSaving}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
              padding: '8px 16px',
              backgroundColor: 'transparent',
              border: '1px solid #555',
              borderRadius: '4px',
              color: '#aaa',
              cursor: isSaving ? 'not-allowed' : 'pointer',
              fontSize: '13px',
              opacity: isSaving ? 0.5 : 1,
            }}
          >
            <RotateCcw size={14} />
            {t('buttons.resetDefaults')}
          </button>

          {/* Action Buttons (right side) */}
          <div style={{ display: 'flex', gap: '8px' }}>
            <button
              onClick={handleCancel}
              disabled={isSaving}
              style={{
                padding: '8px 20px',
                backgroundColor: 'transparent',
                border: '1px solid #555',
                borderRadius: '4px',
                color: '#ddd',
                cursor: isSaving ? 'not-allowed' : 'pointer',
                fontSize: '13px',
                opacity: isSaving ? 0.5 : 1,
              }}
            >
              {t('buttons.cancel')}
            </button>

            <button
              onClick={handleApply}
              disabled={isSaving || !isDirtyNow}
              style={{
                padding: '8px 20px',
                backgroundColor: isDirtyNow ? '#3a3a3a' : '#2a2a2a',
                border: '1px solid #555',
                borderRadius: '4px',
                color: isDirtyNow ? '#ddd' : '#666',
                cursor: (isSaving || !isDirtyNow) ? 'not-allowed' : 'pointer',
                fontSize: '13px',
                opacity: (isSaving || !isDirtyNow) ? 0.5 : 1,
              }}
            >
              {isSaving ? t('common:status.loading') : t('buttons.apply')}
            </button>

            <button
              onClick={handleOK}
              disabled={isSaving}
              style={{
                padding: '8px 24px',
                backgroundColor: '#4a9eff',
                border: '1px solid #4a9eff',
                borderRadius: '4px',
                color: '#fff',
                cursor: isSaving ? 'not-allowed' : 'pointer',
                fontSize: '13px',
                fontWeight: 500,
                opacity: isSaving ? 0.5 : 1,
              }}
            >
              {t('buttons.ok')}
            </button>
          </div>
        </div>
      </div>

      {/* Restarting Overlay */}
      {isRestarting && <RestartingOverlay message={t('messages.restarting')} />}

      {/* Reset Confirmation Dialog */}
      <SimpleConfirmDialog
        isOpen={showResetConfirm}
        title={t('messages.resetTitle', 'Reset Settings')}
        message={t('messages.resetConfirm')}
        confirmText={t('buttons.reset', 'Reset')}
        cancelText={t('buttons.cancel')}
        isDestructive
        onConfirm={handleResetConfirm}
        onCancel={() => setShowResetConfirm(false)}
        zIndex={1010}
      />

      {/* Restart Confirmation Dialog */}
      <SimpleConfirmDialog
        isOpen={showRestartConfirm}
        title={t('messages.restartConfirmTitle')}
        message={t('messages.restartConfirm')}
        confirmText={t('messages.restartButton')}
        onConfirm={handleConfirmRestart}
        onCancel={() => setShowRestartConfirm(false)}
        zIndex={1010}
      />
    </>
  );
};

export default OptionsDialog;
