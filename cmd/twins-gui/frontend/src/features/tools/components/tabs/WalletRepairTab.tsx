import React, { useState, useEffect } from 'react';
import { WalletRepair } from '@wailsjs/go/main/App';
import { useTools } from '@/store/useStore';
import { RestartingOverlay } from '@/shared/components/RestartingOverlay';

interface RepairAction {
  id: string;
  title: string;
  description: string;
  isDestructive?: boolean;
}

const REPAIR_ACTIONS: RepairAction[] = [
  {
    id: 'rescan',
    title: 'Rescan blockchain transactions',
    description: 'Delete the transaction cache and rescan wallet transactions from the local blockchain. This is fast and does not re-download blocks.',
  },
  {
    id: 'resync',
    title: 'Delete local blockchain',
    description: 'Delete all local blockchain data and resync from peers. This will take a long time.',
    isDestructive: true,
  },
];

export const WalletRepairTab: React.FC = () => {
  const [confirming, setConfirming] = useState<string | null>(null);
  const [running, setRunning] = useState(false);
  const [isRestarting, setIsRestarting] = useState(false);
  const [result, setResult] = useState<{ action: string; success: boolean; error?: string; fromRestart?: boolean } | null>(null);
  const { lastRepairResult, setLastRepairResult } = useTools();

  // Show repair result from a previous restart (stored in tools slice by RepairResultHandler)
  useEffect(() => {
    if (lastRepairResult) {
      setResult({ ...lastRepairResult, fromRestart: true });
      setLastRepairResult(null);
    }
  }, [lastRepairResult, setLastRepairResult]);

  const handleExecute = async (actionId: string) => {
    setConfirming(null);
    setRunning(true);
    setResult(null);
    try {
      await WalletRepair(actionId);
      setIsRestarting(true);
      setResult({ action: actionId, success: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Unknown error';
      setResult({ action: actionId, success: false, error: msg });
    } finally {
      setRunning(false);
    }
  };

  return (
    <div style={{ padding: '16px 20px', overflowY: 'auto', height: '100%' }}>
      <p style={{ color: '#aaa', fontSize: '13px', margin: '0 0 16px', lineHeight: '1.5' }}>
        The wallet repair options below will restart the wallet with the selected command-line option.
        Running any of these will close the application and restart it.
      </p>

      {/* Result message */}
      {result && (
        <div style={{
          padding: '10px 14px',
          marginBottom: '12px',
          borderRadius: '4px',
          backgroundColor: result.success ? '#1a3a1a' : '#3a1a1a',
          border: `1px solid ${result.success ? '#2a5a2a' : '#5a2a2a'}`,
          color: result.success ? '#8f8' : '#f88',
          fontSize: '13px',
        }}>
          {result.success
            ? (result.fromRestart
              ? `Repair action "${result.action}" completed successfully.`
              : `Repair action "${result.action}" has been queued. The wallet will restart shortly.`)
            : `Error: ${result.error}`
          }
        </div>
      )}

      {/* Repair actions */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
        {REPAIR_ACTIONS.map((action) => (
          <div key={action.id} style={{
            display: 'flex', alignItems: 'flex-start', gap: '12px',
            padding: '10px 12px', borderRadius: '4px', border: '1px solid #444',
            backgroundColor: '#2a2a2a',
          }}>
            <div style={{ flex: 1 }}>
              <div style={{ color: '#fff', fontSize: '13px', fontWeight: 500, marginBottom: '4px' }}>
                {action.title}
              </div>
              <div style={{ color: '#888', fontSize: '12px', lineHeight: '1.4' }}>
                {action.description}
              </div>
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: '4px', minWidth: '100px' }}>
              {confirming === action.id ? (
                <>
                  <div style={{ color: action.isDestructive ? '#ff6666' : '#ffaa00', fontSize: '11px', marginBottom: '2px' }}>
                    {action.isDestructive ? 'WARNING: This will delete all local blockchain data!' : 'Are you sure?'}
                  </div>
                  <div style={{ display: 'flex', gap: '4px' }}>
                    <button
                      onClick={() => setConfirming(null)}
                      disabled={running}
                      style={{
                        padding: '4px 10px', fontSize: '11px', border: '1px solid #555',
                        borderRadius: '3px', backgroundColor: 'transparent', color: '#aaa', cursor: 'pointer',
                      }}
                    >
                      Cancel
                    </button>
                    <button
                      onClick={() => handleExecute(action.id)}
                      disabled={running}
                      style={{
                        padding: '4px 10px', fontSize: '11px', border: '1px solid #555',
                        borderRadius: '3px',
                        backgroundColor: action.isDestructive ? '#6a2222' : '#4a9eff',
                        color: '#fff', cursor: running ? 'not-allowed' : 'pointer',
                        opacity: running ? 0.5 : 1,
                      }}
                    >
                      {running ? 'Running...' : 'Confirm'}
                    </button>
                  </div>
                </>
              ) : (
                <button
                  onClick={() => setConfirming(action.id)}
                  disabled={running}
                  style={{
                    padding: '5px 14px', fontSize: '12px', border: '1px solid #555',
                    borderRadius: '4px', backgroundColor: '#3a3a3a', color: '#ddd',
                    cursor: running ? 'not-allowed' : 'pointer',
                    opacity: running ? 0.5 : 1,
                  }}
                >
                  Repair
                </button>
              )}
            </div>
          </div>
        ))}
      </div>
      {isRestarting && <RestartingOverlay />}
    </div>
  );
};
