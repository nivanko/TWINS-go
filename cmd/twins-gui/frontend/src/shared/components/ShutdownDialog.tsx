import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { EventsOn, EventsOff } from '@wailsjs/runtime/runtime';
import { GetWalletVersion } from '@wailsjs/go/main/App';
import splashImage from '@/assets/images/splash.png';

/**
 * ShutdownDialog Component
 *
 * Mirrors the splash screen layout to create a symmetric start/end experience.
 * Uses the same splash.png backdrop, version display, and status text positioning.
 */
const ShutdownDialog: React.FC = () => {
  const { t } = useTranslation('common');
  const [version, setVersion] = useState<string>('');

  useEffect(() => {
    GetWalletVersion().then((versionInfo) => {
      if (versionInfo && versionInfo.version) {
        setVersion(versionInfo.version);
      }
    }).catch(console.error);

    EventsOn('shutdown:complete', () => {
      console.log('Shutdown complete');
    });

    return () => {
      EventsOff('shutdown:complete');
    };
  }, []);

  return (
    <div
      className="relative"
      style={{
        width: '480px',
        height: '550px',
        backgroundImage: `url(${splashImage})`,
        backgroundSize: '480px 550px',
        backgroundPosition: '0 0',
        backgroundRepeat: 'no-repeat',
      }}
    >
      {/* TWINS Core title and version - matches splash screen positioning */}
      <div
        className="absolute"
        style={{
          bottom: '100px',
          left: '50%',
          transform: 'translateX(-50%)',
          color: '#FFFFFF',
          fontSize: '16px',
          fontWeight: '500',
          fontFamily: 'system-ui, -apple-system, sans-serif',
          whiteSpace: 'nowrap',
        }}
      >
        TWINS Core {version && `v${version}`}
      </div>

      {/* Status message */}
      <div
        className="absolute"
        style={{
          bottom: '60px',
          left: '50%',
          transform: 'translateX(-50%)',
          color: '#999999',
          fontSize: '12px',
          fontWeight: '400',
          whiteSpace: 'nowrap',
        }}
      >
        {t('shutdown.message')}
      </div>

      {/* Please wait message */}
      <div
        className="absolute"
        style={{
          bottom: '38px',
          left: '50%',
          transform: 'translateX(-50%)',
          color: '#999999',
          fontSize: '11px',
          fontWeight: '400',
          whiteSpace: 'nowrap',
        }}
      >
        {t('shutdown.pleaseWait')}
      </div>
    </div>
  );
};

export default ShutdownDialog;
