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
      {/* TWINS Core title and version - single centered line */}
      <div
        className="absolute"
        style={{
          bottom: '90px',
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

      {/* Status Message - positioned at BOTTOM CENTER */}
      <div
        className="absolute"
        style={{
          bottom: '45px',
          left: '50%',
          transform: 'translateX(-50%)',
          color: '#999999',
          fontSize: '12px',
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