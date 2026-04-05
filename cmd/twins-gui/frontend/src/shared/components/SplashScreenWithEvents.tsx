import React, { useEffect, useRef, useState } from 'react';
import { EventsOn, EventsOff } from '@wailsjs/runtime/runtime';
import { StartInitialization, GetWalletVersion } from '@wailsjs/go/main/App';
import splashImage from '@/assets/images/splash.png';

interface SplashScreenWithEventsProps {
  onComplete?: () => void;
  onError?: (error: string) => void;
}

interface InitProgress {
  step: string;
  description: string;
  progress: number;
  totalSteps: number;
  currentStep: number;
  isComplete: boolean;
  error?: string;
}

export const SplashScreenWithEvents: React.FC<SplashScreenWithEventsProps> = ({
  onComplete,
  onError
}) => {
  const [statusMessage, setStatusMessage] = useState('Initializing...');
  const [version, setVersion] = useState<string>('');
  const currentYear = new Date().getFullYear();

  // Use refs for callbacks to avoid re-subscribing event listeners when props change.
  // Without refs, the useEffect would depend on [onComplete, onError] and re-run
  // on every parent render, briefly unsubscribing events and losing progress messages.
  const onCompleteRef = useRef(onComplete);
  const onErrorRef = useRef(onError);
  onCompleteRef.current = onComplete;
  onErrorRef.current = onError;

  useEffect(() => {
    // Get version information
    GetWalletVersion().then((versionInfo) => {
      if (versionInfo && versionInfo.version) {
        setVersion(versionInfo.version);
      }
    }).catch(console.error);

    // Set up event listeners for initialization progress
    const handleProgress = (data: InitProgress) => {
      // Update status message with the description from the backend
      setStatusMessage(data.description || 'Loading...');
    };

    const handleComplete = (_data: InitProgress) => {
      setStatusMessage('Wallet initialized successfully!');
      // Call completion callback after a short delay
      setTimeout(() => {
        onCompleteRef.current?.();
      }, 500);
    };

    const handleError = (data: InitProgress) => {
      const errorMessage = data.error || 'Unknown error occurred';
      setStatusMessage(`Error: ${errorMessage}`);
      onErrorRef.current?.(errorMessage);
    };

    // Register event listeners
    EventsOn('initialization:progress', handleProgress);
    EventsOn('initialization:complete', handleComplete);
    EventsOn('initialization:error', handleError);

    // Start the initialization process
    StartInitialization().catch((err) => {
      console.error('Failed to start initialization:', err);
      setStatusMessage('Failed to start initialization');
      onErrorRef.current?.(err.message || 'Failed to start initialization');
    });

    // Cleanup event listeners on unmount
    return () => {
      EventsOff('initialization:progress');
      EventsOff('initialization:complete');
      EventsOff('initialization:error');
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps -- callbacks accessed via refs

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
      {/* TWINS Core text and version - positioned at BOTTOM LEFT with Qt typography */}
      <div
        className="absolute"
        style={{
          left: '14px',  // paddingLeft from Qt
          bottom: '70px',  // Positioned at bottom to match screenshot
          color: '#FFFFFF',  // QColor(255, 255, 255) from Qt
        }}
      >
        <div style={{
          fontSize: '20px',  // Adjusted for better visual match
          fontWeight: '500',
          fontFamily: 'system-ui, -apple-system, sans-serif',
          marginBottom: '2px',
        }}>
          TWINS Core
        </div>
        <div style={{
          fontSize: '14px',  // Adjusted for better visual match
          color: '#FFFFFF',
          marginBottom: '8px',
        }}>
          Version {version}
        </div>
        <div style={{
          fontSize: '10px',  // From Qt: 10 * fontFactor
          lineHeight: '12px',
          color: '#FFFFFF'
        }}>
          <div>© 2009-2020 The Bitcoin Core developers</div>
          <div>© 2014-2020 The Dash Core developers</div>
          <div>© 2015-2020 The PIVX Core developers</div>
          <div>© 2018-{currentYear} The TWINS Core developers</div>
        </div>
      </div>

      {/* Status Message - positioned at BOTTOM CENTER */}
      <div
        className="absolute"
        style={{
          bottom: '40px',  // Positioned well above window border
          left: '50%',
          transform: 'translateX(-50%)',
          color: '#D0D0D0',
          fontSize: '13px',
          fontWeight: '400',
          whiteSpace: 'nowrap',
        }}
      >
        {statusMessage}
      </div>
    </div>
  );
};

export default SplashScreenWithEvents;