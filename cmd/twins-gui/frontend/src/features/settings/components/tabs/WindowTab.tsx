import React from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle } from 'lucide-react';
import { GUISettings, SettingMetadata } from '../../../../store/slices/optionsSlice';

interface WindowTabProps {
  settings: Partial<GUISettings>;
  metadata: Record<string, SettingMetadata>;
  onChange: (key: string, value: unknown) => void;
  platform: string;
}

export const WindowTab: React.FC<WindowTabProps> = ({ settings, metadata: _metadata, onChange, platform }) => {
  const { t } = useTranslation('settings');
  // Note: metadata available for future use when CLI overrides are added to Window settings
  void _metadata;

  const hideTrayIcon = settings.fHideTrayIcon ?? false;
  const minimizeToTray = settings.fMinimizeToTray ?? false;
  const isUnsupportedPlatform = platform !== '' && platform !== 'darwin';

  return (
    <div style={{ padding: '16px', display: 'flex', flexDirection: 'column', gap: '16px' }}>
      {/* Platform Warning Banner */}
      {isUnsupportedPlatform && (
        <div
          style={{
            padding: '10px 14px',
            backgroundColor: '#4a2a00',
            border: '1px solid #664400',
            borderRadius: '4px',
            display: 'flex',
            alignItems: 'center',
            gap: '8px',
          }}
        >
          <AlertTriangle size={16} style={{ color: '#ffa500', flexShrink: 0 }} />
          <span style={{ color: '#ffcc00', fontSize: '13px' }}>
            {t('window.platformNotSupported')}
          </span>
        </div>
      )}

      {/* Window Behavior Group */}
      <div style={{
        border: '1px solid #555',
        borderRadius: '4px',
        padding: '12px',
        backgroundColor: '#2a2a2a',
        opacity: isUnsupportedPlatform ? 0.5 : 1,
      }}>
        <div style={{
          color: '#aaa',
          fontSize: '11px',
          textTransform: 'uppercase',
          marginBottom: '12px',
          letterSpacing: '0.5px'
        }}>
          Window Behavior
        </div>

        {/* Minimize to Tray — disabled when tray icon is hidden */}
        <div style={{ marginBottom: '12px' }}>
          <div style={{ display: 'flex', alignItems: 'center' }}>
            <input
              type="checkbox"
              id="fMinimizeToTray"
              checked={minimizeToTray}
              disabled={isUnsupportedPlatform || hideTrayIcon}
              onChange={(e) => onChange('fMinimizeToTray', e.target.checked)}
              style={{ marginRight: '8px' }}
            />
            <label
              htmlFor="fMinimizeToTray"
              style={{ color: (isUnsupportedPlatform || hideTrayIcon) ? '#777' : '#ddd', fontSize: '13px' }}
            >
              Minimize to the tray instead of the taskbar
            </label>
          </div>
          {!isUnsupportedPlatform && hideTrayIcon && (
            <div style={{ color: '#998800', fontSize: '11px', marginLeft: '24px', marginTop: '4px' }}>
              Disabled because "Hide tray icon" is enabled
            </div>
          )}
        </div>

        {/* Minimize on Close */}
        <div style={{ display: 'flex', alignItems: 'center' }}>
          <input
            type="checkbox"
            id="fMinimizeOnClose"
            checked={settings.fMinimizeOnClose ?? false}
            disabled={isUnsupportedPlatform}
            onChange={(e) => onChange('fMinimizeOnClose', e.target.checked)}
            style={{ marginRight: '8px' }}
          />
          <label htmlFor="fMinimizeOnClose" style={{ color: isUnsupportedPlatform ? '#777' : '#ddd', fontSize: '13px' }}>
            Minimize on close
          </label>
        </div>
      </div>

      {/* Tray Icon Group */}
      <div style={{
        border: '1px solid #555',
        borderRadius: '4px',
        padding: '12px',
        backgroundColor: '#2a2a2a',
        opacity: isUnsupportedPlatform ? 0.5 : 1,
      }}>
        <div style={{
          color: '#aaa',
          fontSize: '11px',
          textTransform: 'uppercase',
          marginBottom: '12px',
          letterSpacing: '0.5px'
        }}>
          System Tray
        </div>

        {/* Hide Tray Icon — disabled when minimize-to-tray is enabled */}
        <div>
          <div style={{ display: 'flex', alignItems: 'center' }}>
            <input
              type="checkbox"
              id="fHideTrayIcon"
              checked={hideTrayIcon}
              disabled={isUnsupportedPlatform || minimizeToTray}
              onChange={(e) => onChange('fHideTrayIcon', e.target.checked)}
              style={{ marginRight: '8px' }}
            />
            <label
              htmlFor="fHideTrayIcon"
              style={{ color: (isUnsupportedPlatform || minimizeToTray) ? '#777' : '#ddd', fontSize: '13px' }}
            >
              Hide tray icon
            </label>
          </div>
          {!isUnsupportedPlatform && minimizeToTray && (
            <div style={{ color: '#998800', fontSize: '11px', marginLeft: '24px', marginTop: '4px' }}>
              Disabled because "Minimize to the tray instead of the taskbar" is enabled
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

export default WindowTab;
