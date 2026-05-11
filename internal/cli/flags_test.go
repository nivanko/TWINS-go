package cli

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v2"
)

func TestCommonDaemonFlags(t *testing.T) {
	flags := CommonDaemonFlags()
	assert.NotEmpty(t, flags)

	// Check for expected flags
	flagNames := make(map[string]bool)
	for _, flag := range flags {
		for _, name := range flag.Names() {
			flagNames[name] = true
		}
	}

	expectedFlags := []string{
		"bind", "b",
		"rpc-port",
		"rpc-user",
		"rpc-password",
		"p2p-bind",
		"p2p-port",
		"peers", "p",
		"daemon",
		"staking",
		"masternode",
		"masternode-key",
	}

	for _, expected := range expectedFlags {
		assert.True(t, flagNames[expected], "Expected flag %s not found", expected)
	}
}

func TestCommonRPCClientFlags(t *testing.T) {
	flags := CommonRPCClientFlags()
	assert.NotEmpty(t, flags)

	// Check for expected flags
	flagNames := make(map[string]bool)
	for _, flag := range flags {
		for _, name := range flag.Names() {
			flagNames[name] = true
		}
	}

	expectedFlags := []string{
		"rpc-host",
		"rpc-port",
		"rpc-user",
		"rpc-password",
		"rpc-timeout",
		"rpc-tls",
		"rpc-tls-ca",
		"rpc-tls-pin",
	}

	for _, expected := range expectedFlags {
		assert.True(t, flagNames[expected], "Expected flag %s not found", expected)
	}
}

func TestCommonWalletFlags(t *testing.T) {
	flags := CommonWalletFlags()
	assert.NotEmpty(t, flags)

	// Check for expected flags
	flagNames := make(map[string]bool)
	for _, flag := range flags {
		for _, name := range flag.Names() {
			flagNames[name] = true
		}
	}

	expectedFlags := []string{
		"wallet-dir",
		"wallet-name", "w",
		"wallet-create",
		"wallet-passphrase",
		"wallet-testnet",
	}

	for _, expected := range expectedFlags {
		assert.True(t, flagNames[expected], "Expected flag %s not found", expected)
	}
}

func TestDatabaseFlags(t *testing.T) {
	flags := DatabaseFlags()
	assert.NotEmpty(t, flags)

	// Check for expected flags
	flagNames := make(map[string]bool)
	for _, flag := range flags {
		for _, name := range flag.Names() {
			flagNames[name] = true
		}
	}

	expectedFlags := []string{
		"db-type",
		"db-path",
		"db-cache",
		"db-handles",
		"db-sync",
	}

	for _, expected := range expectedFlags {
		assert.True(t, flagNames[expected], "Expected flag %s not found", expected)
	}
}

func TestPerformanceFlags(t *testing.T) {
	flags := PerformanceFlags()
	assert.NotEmpty(t, flags)

	// Check for expected flags
	flagNames := make(map[string]bool)
	for _, flag := range flags {
		for _, name := range flag.Names() {
			flagNames[name] = true
		}
	}

	expectedFlags := []string{
		"max-peers",
		"workers",
		"validation-workers",
		"mempool-size",
		"block-time",
	}

	for _, expected := range expectedFlags {
		assert.True(t, flagNames[expected], "Expected flag %s not found", expected)
	}
}

func TestFlagDefaults(t *testing.T) {
	tests := []struct {
		name         string
		flagFunc     func() []cli.Flag
		flagName     string
		expectedType interface{}
	}{
		{
			name:         "daemon RPC port default",
			flagFunc:     CommonDaemonFlags,
			flagName:     "rpc-port",
			expectedType: 37818,
		},
		{
			name:         "RPC client port default",
			flagFunc:     CommonRPCClientFlags,
			flagName:     "rpc-port",
			expectedType: 37818,
		},
		{
			name:         "wallet name default",
			flagFunc:     CommonWalletFlags,
			flagName:     "wallet-name",
			expectedType: "default",
		},
		{
			name:         "database type default",
			flagFunc:     DatabaseFlags,
			flagName:     "db-type",
			expectedType: "pebble",
		},
		{
			name:         "max peers default",
			flagFunc:     PerformanceFlags,
			flagName:     "max-peers",
			expectedType: 125,
		},
		{
			name:         "workers default (CPU-aware)",
			flagFunc:     PerformanceFlags,
			flagName:     "workers",
			expectedType: cpuAwareDefault(4, 2),
		},
		{
			name:         "validation-workers default (CPU-aware)",
			flagFunc:     PerformanceFlags,
			flagName:     "validation-workers",
			expectedType: cpuAwareDefault(2, 1),
		},
		{
			name:         "db-cache default (CPU-aware)",
			flagFunc:     DatabaseFlags,
			flagName:     "db-cache",
			expectedType: cpuAwareDefault(256, 128),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := tt.flagFunc()
			found := false

			for _, flag := range flags {
				for _, name := range flag.Names() {
					if name == tt.flagName {
						found = true
						// Check the default value based on type
						switch f := flag.(type) {
						case *cli.IntFlag:
							if intVal, ok := tt.expectedType.(int); ok {
								assert.Equal(t, intVal, f.Value)
							}
						case *cli.StringFlag:
							if strVal, ok := tt.expectedType.(string); ok {
								assert.Equal(t, strVal, f.Value)
							}
						case *cli.BoolFlag:
							if boolVal, ok := tt.expectedType.(bool); ok {
								assert.Equal(t, boolVal, f.Value)
							}
						}
						break
					}
				}
				if found {
					break
				}
			}

			assert.True(t, found, "Flag %s not found", tt.flagName)
		})
	}
}

func TestFlagEnvironmentVariables(t *testing.T) {
	tests := []struct {
		name     string
		flagFunc func() []cli.Flag
		flagName string
		envVar   string
	}{
		{
			name:     "config environment variable",
			flagFunc: func() []cli.Flag { return CreateBaseApp("test", "test", "1.0.0").Flags },
			flagName: "config",
			envVar:   "TWINS_CONFIG",
		},
		{
			name:     "RPC user environment variable",
			flagFunc: CommonDaemonFlags,
			flagName: "rpc-user",
			envVar:   "TWINS_RPC_USER",
		},
		{
			name:     "wallet directory environment variable",
			flagFunc: CommonWalletFlags,
			flagName: "wallet-dir",
			envVar:   "TWINS_WALLET_DIR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := tt.flagFunc()
			found := false

			for _, flag := range flags {
				for _, name := range flag.Names() {
					if name == tt.flagName {
						found = true
						// Check environment variables
						switch f := flag.(type) {
						case *cli.StringFlag:
							assert.Contains(t, f.EnvVars, tt.envVar)
						case *cli.IntFlag:
							assert.Contains(t, f.EnvVars, tt.envVar)
						case *cli.BoolFlag:
							assert.Contains(t, f.EnvVars, tt.envVar)
						case *cli.StringSliceFlag:
							assert.Contains(t, f.EnvVars, tt.envVar)
						case *cli.DurationFlag:
							assert.Contains(t, f.EnvVars, tt.envVar)
						}
						break
					}
				}
				if found {
					break
				}
			}

			assert.True(t, found, "Flag %s not found", tt.flagName)
		})
	}
}

// Test flag combinations
func TestFlagCombinations(t *testing.T) {
	// Test that daemon flags can be combined with others
	daemonFlags := CommonDaemonFlags()
	dbFlags := DatabaseFlags()
	perfFlags := PerformanceFlags()

	combined := append(daemonFlags, dbFlags...)
	combined = append(combined, perfFlags...)

	// Should be able to create a flag set with all flags
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	for _, f := range combined {
		err := f.Apply(set)
		assert.NoError(t, err, "Failed to apply flag to set")
	}
}

// Benchmark flag creation
func BenchmarkCommonDaemonFlags(b *testing.B) {
	for i := 0; i < b.N; i++ {
		CommonDaemonFlags()
	}
}

func BenchmarkCommonRPCClientFlags(b *testing.B) {
	for i := 0; i < b.N; i++ {
		CommonRPCClientFlags()
	}
}

func BenchmarkCommonWalletFlags(b *testing.B) {
	for i := 0; i < b.N; i++ {
		CommonWalletFlags()
	}
}