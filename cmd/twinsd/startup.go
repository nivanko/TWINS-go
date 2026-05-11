package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof" // Register pprof handlers
	"os"
	"path/filepath"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	twinslib "github.com/twins-dev/twins-core/internal/cli"
	"github.com/twins-dev/twins-core/internal/daemon"
	"github.com/twins-dev/twins-core/internal/startup"
	"github.com/twins-dev/twins-core/pkg/types"
)

// startDaemonImproved is the main daemon startup using the unified startup sequence.
func startDaemonImproved(c *cli.Context) error {
	logger := logrus.WithField("component", types.ComponentDaemon)
	logger.Info("Starting TWINS daemon...")

	// Phase 1: Basic initialization
	dataDir := twinslib.GetDataDir(c)
	if err := twinslib.EnsureDataDir(dataDir); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	cm, cliOnly, err := buildConfigManager(c, dataDir)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Apply logging level from ConfigManager (handles defaults → YAML → env → CLI)
	if logLevel := cm.GetString("logging.level"); logLevel != "" {
		level, err := logrus.ParseLevel(logLevel)
		if err != nil {
			logger.WithFields(logrus.Fields{
				"configured": logLevel,
				"error":      err.Error(),
			}).Warn("Invalid logging.level in config, using default")
		} else {
			logrus.SetLevel(level)
			logger.WithField("level", level.String()).Debug("Applied logging level from config")
		}
	}

	// Apply logging format from ConfigManager (text or json)
	if logFormat := cm.GetString("logging.format"); logFormat != "" {
		switch logFormat {
		case "json":
			logrus.SetFormatter(&logrus.JSONFormatter{TimestampFormat: "2006-01-02T15:04:05.000Z"})
		default: // "text" or unrecognised
			logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, TimestampFormat: "2006-01-02 15:04:05"})
		}
	}

	// Apply log output: CLI --log-file flag takes highest priority, then config logging.output.
	{
		var logOutput string
		if c.IsSet("log-file") {
			logOutput = c.String("log-file")
		} else {
			logOutput = cm.GetString("logging.output")
		}
		if logOutput != "" && logOutput != "stdout" {
			switch logOutput {
			case "stderr":
				logrus.SetOutput(os.Stderr)
			default:
				// Resolve relative paths against data directory.
				if !filepath.IsAbs(logOutput) {
					logOutput = filepath.Join(dataDir, logOutput)
				}
				if f, err := os.OpenFile(logOutput, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
					logrus.SetOutput(io.MultiWriter(os.Stdout, f))
					logger.WithField("path", logOutput).Debug("Log output redirected to file")
				} else {
					logger.WithError(err).WithField("path", logOutput).Warn("Failed to open log file, keeping stdout")
				}
			}
		}
	}

	// Build bind addresses from ConfigManager
	rpcBind := fmt.Sprintf("%s:%d", cm.GetString("rpc.host"), cm.GetInt("rpc.port"))
	p2pBind := fmt.Sprintf("%s:%d", cm.GetString("network.listenAddr"), cm.GetInt("network.port"))

	logger.WithFields(logrus.Fields{
		"network":  cliOnly.Network,
		"data_dir": dataDir,
		"rpc_bind": rpcBind,
		"p2p_bind": p2pBind,
		"config":   cm.YAMLPath(),
	}).Debug("Configuration loaded")

	// Create signal handler directly (not via CreateShutdownContext) so we can
	// wire SetReloadFunc for SIGHUP → TLS cert reload after RPC initialization.
	sh := twinslib.NewSignalHandler()
	sh.Start()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-sh.Shutdown()
		cancel()
	}()
	defer cancel()

	var shutdownOnce sync.Once
	doShutdown := func() {
		shutdownOnce.Do(func() {
			logger.Info("Shutdown requested")
			cancel()
		})
	}

	// Start pprof server if enabled
	if c.Bool("pprof") {
		pprofAddr := c.String("pprof-addr")
		go func() {
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				logger.WithError(err).Error("pprof server failed")
			}
		}()
		logger.WithField("address", pprofAddr).Info("pprof server started")
	}

	// Build P2P config from ConfigManager
	p2pCfg := daemon.P2PConfig{
		ListenAddr: p2pBind,
		TestNet:    cliOnly.Network == "testnet",
		Listen:     cm.GetBool("network.listen"),
		Seeds:      cm.GetStringSlice("network.seeds"),
	}

	// Get a config snapshot for components that still need *config.Config
	configSnapshot := cm.Snapshot()

	// Build RPC config
	rpcCfg := daemon.RPCConfig{
		ListenAddr:     rpcBind,
		FullConfig:     configSnapshot,
		ExplicitConfig: cliOnly.ExplicitConfig,
		ShutdownFunc:   doShutdown,
	}
	if cm.GetBool("masternode.enabled") {
		rpcCfg.MasternodeEnabled = true
		rpcCfg.MasternodePrivateKey = cm.GetString("masternode.privateKey")
		rpcCfg.MasternodeServiceAddr = cm.GetString("masternode.serviceAddr")
	}

	// Run unified startup sequence
	// Wire masternode debug config from config snapshot
	mnDebug := configSnapshot.Masternode.Debug
	mnDebugMaxMB := configSnapshot.Masternode.DebugMaxMB
	mnDebugMaxFiles := configSnapshot.Masternode.DebugMaxFiles

	node, err := startup.Start(ctx, startup.Config{
		Network:                 cliOnly.Network,
		DataDir:                 dataDir,
		Logger:                  logger,
		Reindex:                 cliOnly.Reindex,
		Workers:                 cliOnly.Workers,
		ValidationWorkers:       cliOnly.ValidationWorkers,
		DbCacheMB:               cliOnly.DbCacheMB,
		MasternodeDebug:         mnDebug,
		MasternodeDebugMaxMB:    mnDebugMaxMB,
		MasternodeDebugMaxFiles: mnDebugMaxFiles,
		DisableWallet:           !cm.GetBool("wallet.enabled"),
		WalletConfig: daemon.WalletConfig{
			FullConfig:     configSnapshot,
			ReserveBalance: cm.GetInt64("staking.reserveBalance"),
			// IsLocked == true when --reservebalance was passed on CLI, which is
			// semantically equivalent to the old ReserveBalanceSet flag for the
			// daemon path. Phase 3 (GUI) will set values via cm.Set() and should
			// revisit this mapping since GUI-set values are not "locked".
			ReserveBalanceSet: cm.IsLocked("staking.reserveBalance"),
			UseTxCache:        true,
		},
		P2PConfig:     p2pCfg,
		Staking:       cm.GetBool("staking.enabled"),
		ConfigManager: cm,
		RPCConfig:     &rpcCfg,
	})
	if err != nil {
		return err
	}

	// Wire SIGHUP → TLS certificate reload (if TLS is active)
	if node.RPCServer != nil {
		if tm := node.RPCServer.GetTLSManager(); tm != nil {
			sh.SetReloadFunc(tm.Reload)
			logger.Info("SIGHUP wired to TLS certificate reload")
		}
	}

	// All components started successfully
	fmt.Println("✓ TWINS daemon started successfully")
	fmt.Printf("✓ Network: %s\n", cliOnly.Network)
	fmt.Printf("✓ RPC server: %s\n", rpcBind)
	if node.P2PServer == nil {
		fmt.Println("⚠ P2P: failed to start (running without network)")
	} else if !cm.GetBool("network.listen") {
		fmt.Println("✓ P2P: outbound only (listen disabled)")
	} else {
		fmt.Printf("✓ P2P server: %s\n", p2pBind)
	}
	fmt.Println("✓ Press Ctrl+C to stop")

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("Shutdown signal received, stopping daemon...")

	node.Shutdown()

	logger.Info("TWINS daemon stopped")
	return nil
}
