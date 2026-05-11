package cli

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

// SignalHandler manages graceful shutdown for applications
type SignalHandler struct {
	shutdown     chan struct{}
	done         chan struct{}
	logger       *logrus.Entry
	reloadFunc   func() error // Optional config reload callback
	reloadMu     sync.Mutex   // Protects reloadFunc (safe to call SetReloadFunc after Start)
	signals      chan os.Signal
	shutdownOnce sync.Once
	stopOnce     sync.Once
}

// NewSignalHandler creates a new signal handler
func NewSignalHandler() *SignalHandler {
	return &SignalHandler{
		shutdown: make(chan struct{}),
		done:     make(chan struct{}),
		signals:  make(chan os.Signal, 1),
		logger:   logrus.WithField("component", "signal_handler"),
	}
}

// Start begins signal monitoring
func (sh *SignalHandler) Start() {
	signal.Notify(sh.signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	sh.logger.Debug("Signal handler registered for SIGINT, SIGTERM, SIGHUP")

	go func() {
		defer close(sh.done)
		sh.logger.Debug("Signal handler goroutine started, waiting for signals...")

		shutdownInitiated := false

		for sig := range sh.signals {
			sh.logger.WithField("signal", sig.String()).Info("Received signal")

			switch sig {
			case syscall.SIGINT, syscall.SIGTERM:
				if shutdownInitiated {
					// Second signal - force exit immediately
					sh.logger.Warn("Received second shutdown signal - forcing immediate exit")
					os.Exit(1)
				}
				sh.logger.Info("Initiating graceful shutdown (press Ctrl+C again to force exit)")
				shutdownInitiated = true
				sh.shutdownOnce.Do(func() { close(sh.shutdown) })
				// Continue running to catch second signal for force exit
			case syscall.SIGHUP:
				sh.reloadMu.Lock()
				fn := sh.reloadFunc
				sh.reloadMu.Unlock()
				if fn != nil {
					sh.logger.Info("Received SIGHUP - reloading configuration")
					if err := fn(); err != nil {
						sh.logger.WithError(err).Error("Configuration reload failed")
					} else {
						sh.logger.Info("Configuration reloaded successfully")
					}
				} else {
					sh.logger.Debug("Received SIGHUP - configuration reload not configured")
				}
			}
		}
	}()
}

// Shutdown returns a channel that closes when shutdown is requested
func (sh *SignalHandler) Shutdown() <-chan struct{} {
	return sh.shutdown
}

// Wait waits for the signal handler to complete
func (sh *SignalHandler) Wait() {
	<-sh.done
}

// WaitWithTimeout waits for signal handler with a timeout
func (sh *SignalHandler) WaitWithTimeout(timeout time.Duration) bool {
	select {
	case <-sh.done:
		return true
	case <-time.After(timeout):
		sh.logger.Warn("Signal handler wait timeout")
		return false
	}
}

// Stop stops the signal handler
func (sh *SignalHandler) Stop() {
	sh.shutdownOnce.Do(func() { close(sh.shutdown) })
	sh.stopOnce.Do(func() {
		signal.Stop(sh.signals)
		close(sh.signals)
	})
}

// SetReloadFunc sets the configuration reload callback.
// Safe to call after Start() — protected by reloadMu.
func (sh *SignalHandler) SetReloadFunc(fn func() error) {
	sh.reloadMu.Lock()
	sh.reloadFunc = fn
	sh.reloadMu.Unlock()
}

// SetupGracefulShutdown sets up graceful shutdown handling for a service
func SetupGracefulShutdown(ctx context.Context, shutdownFunc func() error) {
	signalHandler := NewSignalHandler()
	signalHandler.Start()

	go func() {
		select {
		case <-signalHandler.Shutdown():
			logrus.Info("Graceful shutdown initiated")

			// Create a context with timeout for shutdown
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Channel to signal shutdown completion
			shutdownDone := make(chan error, 1)

			// Run shutdown function in goroutine
			go func() {
				shutdownDone <- shutdownFunc()
			}()

			// Wait for shutdown to complete or timeout
			select {
			case err := <-shutdownDone:
				if err != nil {
					logrus.WithError(err).Error("Error during shutdown")
				} else {
					logrus.Info("Graceful shutdown completed")
				}
			case <-shutdownCtx.Done():
				logrus.Error("Graceful shutdown timeout - forcing exit")
			}

		case <-ctx.Done():
			logrus.Info("Context cancelled - shutting down")
		}

		// Force exit if we get here
		os.Exit(0)
	}()
}

// CreateShutdownContext creates a context that cancels on shutdown signals
func CreateShutdownContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	signalHandler := NewSignalHandler()
	signalHandler.Start()

	go func() {
		<-signalHandler.Shutdown()
		logrus.Debug("Shutdown signal received - cancelling context")
		cancel()
	}()

	return ctx, cancel
}
