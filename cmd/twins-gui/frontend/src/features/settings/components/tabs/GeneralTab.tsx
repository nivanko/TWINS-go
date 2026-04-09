import React, { useState, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { Lock, AlertTriangle, Shield, ShieldCheck, Unlock, KeyRound, FolderOpen } from 'lucide-react';
import { GUISettings, ThemeInfo, SettingMetadata, DaemonSettingValue } from '../../../../store/slices/optionsSlice';
import { SUPPORTED_LANGUAGES } from '../../../../i18n/languages';
import { GetDataDirectoryInfo, GetWalletEncryptionStatus } from '@wailsjs/go/main/App';
import { EventsOn, EventsOff, EventsEmit } from '@wailsjs/runtime/runtime';
import { main } from '@wailsjs/go/models';

interface GeneralTabProps {
  settings: Partial<GUISettings>;
  metadata: Record<string, SettingMetadata>;
  themes: ThemeInfo[];
  onChange: (key: string, value: unknown) => void;
  daemonValues: Record<string, DaemonSettingValue>;
  pendingDaemonChanges: Record<string, unknown>;
  onDaemonChange: (key: string, value: unknown) => void;
}

interface EncryptionStatus {
  encrypted: boolean;
  locked: boolean;
}

export const GeneralTab: React.FC<GeneralTabProps> = ({ settings, metadata, themes, onChange, daemonValues, pendingDaemonChanges, onDaemonChange }) => {
  const { t } = useTranslation('settings');
  const isMounted = useRef(false);

  const [dataDirInfo, setDataDirInfo] = useState<main.DataDirectoryInfo | null>(null);
  const [encryptionStatus, setEncryptionStatus] = useState<EncryptionStatus>({
    encrypted: false,
    locked: true,
  });
  const [migrationDismissed, setMigrationDismissed] = useState(() => {
    try { return localStorage.getItem('twins_autocombine_migration_dismissed') === '1'; } catch { return false; }
  });

  useEffect(() => {
    isMounted.current = true;

    const fetchDataDirInfo = async () => {
      try {
        const info = await GetDataDirectoryInfo();
        if (isMounted.current && info) setDataDirInfo(info);
      } catch (error) {
        console.error('Failed to fetch data directory info:', error);
      }
    };

    const fetchEncryptionStatus = async () => {
      try {
        const status = await GetWalletEncryptionStatus();
        if (isMounted.current) {
          setEncryptionStatus({
            encrypted: status?.encrypted ?? false,
            locked: status?.locked ?? true,
          });
        }
      } catch (error) {
        console.error('Failed to fetch encryption status:', error);
      }
    };

    fetchDataDirInfo();
    fetchEncryptionStatus();

    const handleEncrypted = () => {
      if (!isMounted.current) return;
      setEncryptionStatus({ encrypted: true, locked: true });
    };
    const handleUnlocked = () => {
      if (!isMounted.current) return;
      setEncryptionStatus((prev) => ({ ...prev, locked: false }));
    };
    const handleLocked = () => {
      if (!isMounted.current) return;
      setEncryptionStatus((prev) => ({ ...prev, locked: true }));
    };

    EventsOn('wallet:encrypted', handleEncrypted);
    EventsOn('wallet:unlocked', handleUnlocked);
    EventsOn('wallet:locked', handleLocked);

    return () => {
      isMounted.current = false;
      EventsOff('wallet:encrypted');
      EventsOff('wallet:unlocked');
      EventsOff('wallet:locked');
    };
  }, []);

  const isOverridden = (key: string) => metadata[key]?.overriddenByCLI ?? false;
  const requiresRestart = (key: string) => metadata[key]?.requiresRestart ?? false;

  const renderRestartIcon = (key: string) => {
    if (!requiresRestart(key)) return null;
    return (
      <span title={t('common.requiresRestart')} style={{ color: '#ffa500', marginLeft: '8px' }}>
        <AlertTriangle size={14} style={{ display: 'inline', verticalAlign: 'middle' }} />
      </span>
    );
  };

  const renderOverrideIcon = (key: string) => {
    if (!isOverridden(key)) return null;
    const flagName = metadata[key]?.cliFlagName || '';
    return (
      <span title={t('common.overriddenBy', { flag: flagName })} style={{ color: '#888', marginLeft: '8px' }}>
        <Lock size={14} style={{ display: 'inline', verticalAlign: 'middle' }} />
      </span>
    );
  };

  const getSourceText = (source: string): string => {
    switch (source) {
      case 'cli': return t('main.dataDirectorySourceCli');
      case 'preference': return t('main.dataDirectorySourcePreference');
      case 'default': return t('main.dataDirectorySourceDefault');
      default: return source;
    }
  };

  const handleEncryptWallet = () => EventsEmit('settings:open-encrypt-dialog');
  const handleUnlockWallet = () => EventsEmit('settings:open-unlock-dialog');
  const handleChangePassphrase = () => EventsEmit('settings:open-change-passphrase-dialog');

  // Helper to get daemon setting value (pending change takes priority over backend value)
  const getDaemonValue = (key: string, defaultValue: unknown = '') => {
    if (key in pendingDaemonChanges) return pendingDaemonChanges[key];
    return daemonValues[key]?.value ?? defaultValue;
  };
  const stakeSplitThreshold = getDaemonValue('staking.stakeSplitThreshold', 200000) as number;
  const autoCombineEnabled = getDaemonValue('wallet.autoCombine', false) as boolean;
  const autoCombineTarget = getDaemonValue('wallet.autoCombineTarget', 10000) as number;
  const autoCombineCooldown = getDaemonValue('wallet.autoCombineCooldown', 600) as number;

  const sectionStyle: React.CSSProperties = {
    border: '1px solid #555',
    borderRadius: '4px',
    padding: '12px',
    backgroundColor: '#2a2a2a',
  };

  const sectionHeaderStyle: React.CSSProperties = {
    color: '#aaa',
    fontSize: '11px',
    textTransform: 'uppercase',
    marginBottom: '12px',
    letterSpacing: '0.5px',
  };

  const labelStyle: React.CSSProperties = {
    width: '200px',
    color: '#ddd',
    fontSize: '13px',
  };

  return (
    <div style={{ padding: '16px', display: 'flex', flexDirection: 'column', gap: '16px' }}>

      {/* === User Interface === */}
      <div style={sectionStyle}>
        <div style={sectionHeaderStyle}>{t('main.userInterface')}</div>

        <div style={{ display: 'flex', alignItems: 'center', marginBottom: '12px' }}>
          <label style={labelStyle}>
            {t('main.language')}
            {renderRestartIcon('language')}
            {renderOverrideIcon('language')}
          </label>
          <select
            value={settings.language ?? ''}
            onChange={(e) => onChange('language', e.target.value)}
            disabled={isOverridden('language')}
            style={{
              width: '200px', padding: '4px 8px',
              backgroundColor: isOverridden('language') ? '#333' : '#3a3a3a',
              border: '1px solid #555', borderRadius: '3px',
              color: isOverridden('language') ? '#888' : '#fff', fontSize: '13px',
            }}
          >
            <option value="">{t('main.languageDefault')}</option>
            {SUPPORTED_LANGUAGES.map((lang) => (
              <option key={lang.code} value={lang.code}>{lang.nativeName}</option>
            ))}
          </select>
        </div>

        <div style={{ display: 'flex', alignItems: 'center' }}>
          <label style={labelStyle}>
            {t('main.theme')}
            {renderRestartIcon('theme')}
            {renderOverrideIcon('theme')}
          </label>
          <select
            value={settings.theme ?? 'dark'}
            onChange={(e) => onChange('theme', e.target.value)}
            disabled={isOverridden('theme')}
            style={{
              width: '200px', padding: '4px 8px',
              backgroundColor: isOverridden('theme') ? '#333' : '#3a3a3a',
              border: '1px solid #555', borderRadius: '3px',
              color: isOverridden('theme') ? '#888' : '#fff', fontSize: '13px',
            }}
          >
            {themes.map((theme) => (
              <option key={theme.name} value={theme.name} disabled={theme.name !== 'dark'}>
                {theme.name.charAt(0).toUpperCase() + theme.name.slice(1)}
                {theme.isBuiltIn ? '' : ` (${t('common.custom')})`}
              </option>
            ))}
          </select>
        </div>
      </div>

      {/* === Amounts & Display === */}
      <div style={sectionStyle}>
        <div style={sectionHeaderStyle}>{t('display.units')}</div>

        <div style={{ display: 'flex', alignItems: 'center', marginBottom: '12px' }}>
          <label style={labelStyle}>{t('display.unitLabel')}</label>
          <select
            value={settings.nDisplayUnit ?? 0}
            onChange={(e) => onChange('nDisplayUnit', parseInt(e.target.value))}
            style={{
              width: '150px', padding: '4px 8px',
              backgroundColor: '#3a3a3a', border: '1px solid #555',
              borderRadius: '3px', color: '#fff', fontSize: '13px',
            }}
          >
            <option value={0}>{t('display.unitTwins')}</option>
            <option value={1}>{t('display.unitMilli')}</option>
            <option value={2}>{t('display.unitMicro')}</option>
          </select>
        </div>

        <div style={{ display: 'flex', alignItems: 'center' }}>
          <label style={labelStyle}>
            {t('display.decimalDigits')}
            {renderRestartIcon('digits')}
          </label>
          <select
            value={settings.digits ?? 8}
            onChange={(e) => onChange('digits', parseInt(e.target.value))}
            style={{
              width: '100px', padding: '4px 8px',
              backgroundColor: '#3a3a3a', border: '1px solid #555',
              borderRadius: '3px', color: '#fff', fontSize: '13px',
            }}
          >
            {[2, 3, 4, 5, 6, 7, 8].map(n => (
              <option key={n} value={n}>{n}</option>
            ))}
          </select>
        </div>
      </div>

      {/* === Transaction Display === */}
      <div style={sectionStyle}>
        <div style={sectionHeaderStyle}>{t('display.transactionDisplay')}</div>

        <div style={{ display: 'flex', alignItems: 'center', marginBottom: '12px' }}>
          <input
            type="checkbox" id="fHideOrphans"
            checked={settings.fHideOrphans ?? false}
            onChange={(e) => onChange('fHideOrphans', e.target.checked)}
            style={{ marginRight: '8px' }}
          />
          <label htmlFor="fHideOrphans" style={{ color: '#ddd', fontSize: '13px' }}>
            {t('display.hideOrphanStakes')}
          </label>
        </div>

        <div style={{ display: 'flex', alignItems: 'center' }}>
          <input
            type="checkbox" id="fHideZeroBalances"
            checked={settings.fHideZeroBalances ?? false}
            onChange={(e) => onChange('fHideZeroBalances', e.target.checked)}
            style={{ marginRight: '8px' }}
          />
          <label htmlFor="fHideZeroBalances" style={{ color: '#ddd', fontSize: '13px' }}>
            {t('display.hideZeroBalances')}
          </label>
        </div>
      </div>

      {/* === Wallet === */}
      <div style={sectionStyle}>
        <div style={sectionHeaderStyle}>{t('wallet.title')}</div>

        <div style={{ display: 'flex', alignItems: 'center', marginBottom: '12px' }}>
          <label style={labelStyle}>{t('wallet.stakeSplitThreshold')}</label>
          <input
            type="number"
            value={stakeSplitThreshold}
            onChange={(e) => onDaemonChange('staking.stakeSplitThreshold', Math.floor(parseFloat(e.target.value) || 0))}
            min={0} max={999999999}
            style={{
              width: '120px', padding: '4px 8px',
              backgroundColor: '#3a3a3a', border: '1px solid #555',
              borderRadius: '3px', color: '#fff', fontSize: '13px',
            }}
          />
          <span style={{ color: '#888', fontSize: '13px', marginLeft: '8px' }}>{t('wallet.stakeSplitThresholdNote')}</span>
        </div>

        {/* Migration notice — shown once */}
        {!migrationDismissed && (
          <div style={{
            display: 'flex', alignItems: 'flex-start', gap: '8px',
            padding: '8px 12px', marginBottom: '12px',
            backgroundColor: '#3a4a5a', border: '1px solid #4a6a8a',
            borderRadius: '4px', fontSize: '12px', color: '#aaccee',
          }}>
            <AlertTriangle size={14} style={{ flexShrink: 0, marginTop: '1px', color: '#4a9af5' }} />
            <span style={{ flex: 1 }}>{t('wallet.autoCombineMigrationNotice')}</span>
            <button
              onClick={() => {
                setMigrationDismissed(true);
                try { localStorage.setItem('twins_autocombine_migration_dismissed', '1'); } catch { /* ignore */ }
              }}
              style={{
                background: 'none', border: 'none', color: '#aaccee',
                cursor: 'pointer', fontSize: '14px', padding: '0 4px', flexShrink: 0,
              }}
            >
              &times;
            </button>
          </div>
        )}

        {/* Auto-Combine (UTXO Consolidation) */}
        <div style={{ borderTop: '1px solid #444', paddingTop: '12px', marginTop: '4px' }}>
          <div style={{ display: 'flex', alignItems: 'center', marginBottom: '12px' }}>
            <input
              type="checkbox" id="autoCombineEnabled"
              checked={autoCombineEnabled}
              onChange={(e) => onDaemonChange('wallet.autoCombine', e.target.checked)}
              style={{ marginRight: '8px' }}
            />
            <label htmlFor="autoCombineEnabled" style={{ color: '#ddd', fontSize: '13px' }}>
              {t('wallet.autoCombine')}
            </label>
          </div>

          {autoCombineEnabled && (
            <>
              <div style={{ display: 'flex', alignItems: 'center', marginBottom: '8px' }}>
                <label style={labelStyle}>{t('wallet.autoCombineTarget')}</label>
                <input
                  type="number"
                  value={autoCombineTarget}
                  onChange={(e) => onDaemonChange('wallet.autoCombineTarget', Math.floor(parseFloat(e.target.value) || 0))}
                  min={1000} max={100000000}
                  placeholder="10000"
                  style={{
                    width: '120px', padding: '4px 8px',
                    backgroundColor: '#3a3a3a', border: '1px solid #555',
                    borderRadius: '3px', color: '#fff', fontSize: '13px',
                  }}
                />
                <span style={{ color: '#888', fontSize: '13px', marginLeft: '8px' }}>TWINS</span>
              </div>
              <div style={{ color: '#666', fontSize: '11px', marginBottom: '12px', marginLeft: '200px' }}>
                {t('wallet.autoCombineTargetHint')}
              </div>

              <div style={{ display: 'flex', alignItems: 'center' }}>
                <label style={labelStyle}>{t('wallet.autoCombineCooldown')}</label>
                <input
                  type="number"
                  value={autoCombineCooldown}
                  onChange={(e) => onDaemonChange('wallet.autoCombineCooldown', Math.floor(parseFloat(e.target.value) || 0))}
                  min={60} max={86400}
                  style={{
                    width: '120px', padding: '4px 8px',
                    backgroundColor: '#3a3a3a', border: '1px solid #555',
                    borderRadius: '3px', color: '#fff', fontSize: '13px',
                  }}
                />
                <span style={{ color: '#888', fontSize: '13px', marginLeft: '8px' }}>{t('wallet.autoCombineCooldownNote')}</span>
              </div>
            </>
          )}
        </div>

      </div>

      {/* === Wallet Encryption === */}
      <div style={sectionStyle}>
        <div style={{ ...sectionHeaderStyle, display: 'flex', alignItems: 'center', gap: '8px' }}>
          {encryptionStatus.encrypted ? <ShieldCheck size={14} /> : <Shield size={14} />}
          {t('wallet.encryption')}
        </div>

        <div style={{
          display: 'flex', alignItems: 'center', gap: '8px',
          marginBottom: '12px', padding: '8px 12px',
          backgroundColor: encryptionStatus.encrypted ? '#2a3a2a' : '#3a2a2a',
          borderRadius: '4px',
          border: `1px solid ${encryptionStatus.encrypted ? '#4a6a4a' : '#6a4a4a'}`,
        }}>
          {encryptionStatus.encrypted ? (
            <>
              {encryptionStatus.locked
                ? <Lock size={16} style={{ color: '#88cc88' }} />
                : <Unlock size={16} style={{ color: '#88cc88' }} />}
              <span style={{ color: '#88cc88', fontSize: '13px' }}>
                {encryptionStatus.locked ? t('wallet.encryptionStatusLocked') : t('wallet.encryptionStatusUnlocked')}
              </span>
            </>
          ) : (
            <>
              <AlertTriangle size={16} style={{ color: '#cc8888' }} />
              <span style={{ color: '#cc8888', fontSize: '13px' }}>{t('wallet.encryptionStatusNotEncrypted')}</span>
            </>
          )}
        </div>

        <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
          {!encryptionStatus.encrypted ? (
            <button
              onClick={handleEncryptWallet}
              style={{
                display: 'flex', alignItems: 'center', gap: '6px',
                padding: '8px 16px', backgroundColor: '#4a5a4a',
                border: '1px solid #6a7a6a', borderRadius: '4px',
                color: '#ddd', fontSize: '12px', cursor: 'pointer',
              }}
              onMouseEnter={(e) => e.currentTarget.style.backgroundColor = '#5a6a5a'}
              onMouseLeave={(e) => e.currentTarget.style.backgroundColor = '#4a5a4a'}
            >
              <Shield size={14} />
              {t('wallet.encryptWalletButton')}
            </button>
          ) : (
            <>
              <button
                onClick={handleUnlockWallet}
                disabled={!encryptionStatus.locked}
                style={{
                  display: 'flex', alignItems: 'center', gap: '6px',
                  padding: '8px 16px',
                  backgroundColor: encryptionStatus.locked ? '#4a5a4a' : '#3a3a3a',
                  border: '1px solid #555', borderRadius: '4px',
                  color: encryptionStatus.locked ? '#ddd' : '#888',
                  fontSize: '12px',
                  cursor: encryptionStatus.locked ? 'pointer' : 'not-allowed',
                  opacity: encryptionStatus.locked ? 1 : 0.6,
                }}
                onMouseEnter={(e) => { if (encryptionStatus.locked) e.currentTarget.style.backgroundColor = '#5a6a5a'; }}
                onMouseLeave={(e) => { if (encryptionStatus.locked) e.currentTarget.style.backgroundColor = '#4a5a4a'; }}
              >
                <Unlock size={14} />
                {t('wallet.unlockWalletButton')}
              </button>
              <button
                onClick={handleChangePassphrase}
                style={{
                  display: 'flex', alignItems: 'center', gap: '6px',
                  padding: '8px 16px', backgroundColor: '#4a4a5a',
                  border: '1px solid #555', borderRadius: '4px',
                  color: '#ddd', fontSize: '12px', cursor: 'pointer',
                }}
                onMouseEnter={(e) => e.currentTarget.style.backgroundColor = '#5a5a6a'}
                onMouseLeave={(e) => e.currentTarget.style.backgroundColor = '#4a4a5a'}
              >
                <KeyRound size={14} />
                {t('wallet.changePassphraseButton')}
              </button>
            </>
          )}
        </div>

        <div style={{ marginTop: '12px', color: '#888', fontSize: '11px', lineHeight: '1.5' }}>
          {encryptionStatus.encrypted ? t('wallet.encryptionHelpEncrypted') : t('wallet.encryptionHelpNotEncrypted')}
        </div>
      </div>

      {/* === Expert === */}
      <div style={sectionStyle}>
        <div style={sectionHeaderStyle}>{t('wallet.expert')}</div>

        <div style={{ display: 'flex', alignItems: 'center', marginBottom: '12px' }}>
          <input
            type="checkbox" id="fCoinControlFeatures"
            checked={settings.fCoinControlFeatures ?? false}
            onChange={(e) => onChange('fCoinControlFeatures', e.target.checked)}
            style={{ marginRight: '8px' }}
          />
          <label htmlFor="fCoinControlFeatures" style={{ color: '#ddd', fontSize: '13px' }}>
            {t('wallet.coinControl')}
          </label>
        </div>

        <div style={{ display: 'flex', alignItems: 'center' }}>
          <input
            type="checkbox" id="fShowMasternodesTab"
            checked={settings.fShowMasternodesTab ?? true}
            onChange={(e) => onChange('fShowMasternodesTab', e.target.checked)}
            style={{ marginRight: '8px' }}
          />
          <label htmlFor="fShowMasternodesTab" style={{ color: '#ddd', fontSize: '13px' }}>
            {t('wallet.showMasternodes')}
            {renderRestartIcon('fShowMasternodesTab')}
          </label>
        </div>
      </div>

      {/* === Third-Party URLs === */}
      <div style={sectionStyle}>
        <div style={sectionHeaderStyle}>{t('display.thirdPartyTitle')}</div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
          <label style={{ color: '#ddd', fontSize: '13px' }}>
            {t('display.thirdPartyLabel')}
            {renderRestartIcon('strThirdPartyTxUrls')}
          </label>
          <input
            type="text"
            value={settings.strThirdPartyTxUrls ?? ''}
            onChange={(e) => onChange('strThirdPartyTxUrls', e.target.value)}
            placeholder="https://example.com/tx/%s"
            style={{
              width: '100%', padding: '6px 8px',
              backgroundColor: '#3a3a3a', border: '1px solid #555',
              borderRadius: '3px', color: '#fff', fontSize: '13px',
            }}
          />
          <div style={{ color: '#888', fontSize: '11px' }}>{t('display.thirdPartyHelp')}</div>
        </div>
      </div>

      {/* === Data Directory === */}
      <div style={sectionStyle}>
        <div style={sectionHeaderStyle}>{t('main.dataDirectory')}</div>

        {dataDirInfo ? (
          <>
            <div style={{ display: 'flex', alignItems: 'center', marginBottom: '8px' }}>
              <FolderOpen size={16} style={{ color: '#888', marginRight: '8px', flexShrink: 0 }} />
              <label style={{ width: '120px', color: '#ddd', fontSize: '13px', flexShrink: 0 }}>
                {t('main.dataDirectoryPath')}
              </label>
              <span
                style={{
                  color: '#fff', fontSize: '13px',
                  backgroundColor: '#3a3a3a', padding: '4px 8px',
                  borderRadius: '3px', border: '1px solid #555',
                  overflow: 'hidden', textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap', flex: 1, minWidth: 0,
                }}
                title={dataDirInfo.path}
              >
                {dataDirInfo.path}
              </span>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', marginLeft: '24px' }}>
              <label style={{ width: '120px', color: '#ddd', fontSize: '13px', flexShrink: 0 }}>
                {t('main.dataDirectorySource')}
              </label>
              <span style={{
                color: dataDirInfo.source === 'cli' ? '#ffa500' : '#888',
                fontSize: '13px', fontStyle: 'italic',
              }}>
                {getSourceText(dataDirInfo.source)}
              </span>
            </div>
          </>
        ) : (
          <div style={{ color: '#888', fontSize: '13px', fontStyle: 'italic' }}>Loading...</div>
        )}
      </div>

    </div>
  );
};

export default GeneralTab;
