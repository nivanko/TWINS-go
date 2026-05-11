package daemon

import (
	"context"
	"time"

	"github.com/twins-dev/twins-core/internal/rpc"
	"github.com/twins-dev/twins-core/pkg/types"
)

// Shutdown performs graceful shutdown with proper ordering.
// This replaces:
//   - cmd/twinsd/startup_improved.go:shutdownComponentsImproved()
//   - cmd/twins-gui/app.go:shutdown()
//
// Shutdown order:
//  1. Stop accepting new work (RPC, P2P)
//  2. Stop masternode sync loop early
//  3. Drain existing work (mempool, syncer) with timeout
//  4. Save masternode data
//  5. Stop consensus engine
//  6. Save wallet transaction cache
//  7. Clean up RPC authentication (delete cookie file)
//  8. Close storage
//
// Shutdown is idempotent — safe to call multiple times.
func (n *Node) Shutdown() {
	n.shutdownOnce.Do(func() {
		n.doShutdown()
	})
}

// doShutdown performs the actual shutdown work (called once via shutdownOnce).
func (n *Node) doShutdown() {
	// Mark the node as shutting down so any concurrent ConfigManager subscriber
	// callbacks (e.g. masternode.debug toggling) bail out before allocating new
	// resources that would leak past Phase 8 (storage close).
	n.shuttingDown.Store(true)

	n.logger.Debug("Starting graceful shutdown...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), types.ShutdownTimeout)
	defer cancel()

	// Phase 1: Stop accepting new work
	n.mu.RLock()
	rpcSrv := n.RPCServer
	p2pSrv := n.P2PServer
	walletRef := n.Wallet
	n.mu.RUnlock()

	// Stop wallet rebroadcast loop before shutting down P2P transport.
	if walletRef != nil {
		walletRef.SetBroadcaster(nil)
	}

	if rpcSrv != nil {
		n.logger.Debug("Stopping RPC server...")
		rpcSrv.Stop()
	}

	if p2pSrv != nil {
		n.logger.Debug("Stopping P2P server...")
		p2pSrv.Stop()
	}

	// Phase 2: Stop masternode sync loop early to prevent it from running during shutdown
	if n.Masternode != nil {
		n.logger.Debug("Stopping masternode sync loop...")
		n.Masternode.Stop()
	}

	// Phase 3: Drain existing work with timeout
	done := make(chan struct{})
	go func() {
		defer close(done)

		if n.Mempool != nil {
			n.logger.Debug("Stopping mempool...")
			n.Mempool.Stop()
		}

		n.mu.RLock()
		syncer := n.Syncer
		n.mu.RUnlock()

		if syncer != nil {
			if healthTracker := syncer.GetHealthTracker(); healthTracker != nil {
				healthTracker.StopAnnouncementCleanup()
			}
			n.logger.Debug("Stopping syncer...")
			syncer.Stop()
		}
	}()

	select {
	case <-done:
		n.logger.Debug("Components stopped gracefully")
	case <-shutdownCtx.Done():
		n.logger.Warn("Shutdown timeout reached, forcing stop")
	}

	// Phase 4: Save masternode data
	if n.Masternode != nil && n.ChainParams != nil {
		networkMagic := n.ChainParams.NetMagicBytes[:]

		saveDone := make(chan struct{})
		go func() {
			defer close(saveDone)
			n.logger.Debug("Saving masternode cache...")
			if err := n.Masternode.SaveCache(n.Config.DataDir, networkMagic); err != nil {
				n.logger.WithError(err).Warn("Failed to save masternode cache")
			}
			n.logger.Debug("Saving payment votes...")
			if err := n.Masternode.SavePaymentVotes(n.Config.DataDir, networkMagic); err != nil {
				n.logger.WithError(err).Warn("Failed to save payment votes")
			}
		}()

		select {
		case <-saveDone:
			n.logger.Debug("Masternode data saved")
		case <-time.After(types.MasternodeSaveTimeout):
			n.logger.Warn("Masternode data save timeout - skipping")
		}
	}

	// Phase 4.5: Close masternode debug collector (flush buffered events).
	// atomic.Pointer.Swap atomically replaces the field with nil and returns the
	// previous value, so this races safely against the masternode.debug
	// hot-reload subscriber even without n.mu.
	if dc := n.DebugCollector.Swap(nil); dc != nil {
		n.logger.Debug("Closing masternode debug collector...")
		dc.Close()
	}

	// Phase 5: Stop consensus engine (with timeout to ensure wallet save and
	// storage close in Phases 6-8 always execute even if staking worker hangs)
	if n.Consensus != nil {
		n.logger.Debug("Stopping consensus engine...")
		consensusDone := make(chan struct{})
		go func() {
			defer close(consensusDone)
			n.Consensus.Stop()
		}()

		select {
		case <-consensusDone:
			n.logger.Debug("Consensus engine stopped")
		case <-time.After(types.ConsensusStopTimeout):
			n.logger.Warn("Consensus engine stop timeout - proceeding with shutdown")
		}
	}

	// Phase 6: Save wallet transaction cache
	n.mu.RLock()
	w := n.Wallet
	n.mu.RUnlock()

	if w != nil {
		n.logger.Debug("Saving wallet transaction cache...")
		if err := w.SaveTransactionCache(); err != nil {
			n.logger.WithError(err).Warn("Failed to save wallet transaction cache")
		}
	}

	// Phase 7: Clean up RPC authentication (delete cookie file)
	n.mu.RLock()
	rpcCfg := n.rpcConfig
	n.mu.RUnlock()

	if rpcCfg != nil {
		n.logger.Debug("Cleaning up RPC authentication...")
		rpc.CleanupAuthentication(rpcCfg, n.logger)
	}

	// Phase 8: Close storage (must be last)
	if n.Storage != nil {
		n.logger.Debug("Closing storage...")
		n.Storage.Close()
	}

	n.logger.Debug("Graceful shutdown completed")
}
