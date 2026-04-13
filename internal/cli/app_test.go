package cli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func TestCreateBaseApp(t *testing.T) {
	app := CreateBaseApp("test-app", "Test application", "1.0.0")

	assert.Equal(t, "test-app", app.Name)
	assert.Equal(t, "Test application", app.Usage)
	assert.Equal(t, "1.0.0", app.Version)
	assert.NotEmpty(t, app.Flags)
	assert.NotEmpty(t, app.Authors)
	assert.NotEmpty(t, app.Copyright)
}

func TestSetupLogging(t *testing.T) {
	// Create a temporary app for testing
	app := CreateBaseApp("test", "test", "1.0.0")

	tests := []struct {
		name     string
		args     []string
		expected logrus.Level
		wantErr  bool
	}{
		{
			name:     "default log level",
			args:     []string{"test"},
			expected: logrus.ErrorLevel,
		},
		{
			name:     "debug log level",
			args:     []string{"test", "--log-level", "debug"},
			expected: logrus.DebugLevel,
		},
		{
			name:     "error log level",
			args:     []string{"test", "--log-level", "error"},
			expected: logrus.ErrorLevel,
		},
		{
			name:    "invalid log level",
			args:    []string{"test", "--log-level", "invalid"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset logrus to default state
			logrus.SetLevel(logrus.InfoLevel)
			logrus.SetFormatter(&logrus.TextFormatter{})

			// Create a test context
			c := createTestContext(app, tt.args)

			err := SetupLogging(c)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, logrus.GetLevel())
		})
	}
}

func TestSetupLoggingJSON(t *testing.T) {
	app := CreateBaseApp("test", "test", "1.0.0")
	args := []string{"test", "--log-json"}

	c := createTestContext(app, args)
	err := SetupLogging(c)
	require.NoError(t, err)

	// Verify JSON formatter is set
	formatter := logrus.StandardLogger().Formatter
	assert.IsType(t, &logrus.JSONFormatter{}, formatter)
}

func TestGetConfigPath(t *testing.T) {
	app := CreateBaseApp("test", "test", "1.0.0")

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "default config path",
			args:     []string{"test"},
			expected: getDefaultConfigPath("test"),
		},
		{
			name:     "custom config path",
			args:     []string{"test", "--config", "/custom/config.yml"},
			expected: "/custom/config.yml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := createTestContext(app, tt.args)
			result := GetConfigPath(c)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetDataDir(t *testing.T) {
	app := CreateBaseApp("test", "test", "1.0.0")

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "default data dir",
			args:     []string{"test"},
			expected: getDefaultDataDir("test"),
		},
		{
			name:     "custom data dir",
			args:     []string{"test", "--datadir", "/custom/data"},
			expected: "/custom/data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := createTestContext(app, tt.args)
			result := GetDataDir(c)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetNetwork(t *testing.T) {
	app := CreateBaseApp("test", "test", "1.0.0")

	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "default network",
			args:     []string{"test"},
			expected: "mainnet",
		},
		{
			name:     "testnet network",
			args:     []string{"test", "--network", "testnet"},
			expected: "testnet",
		},
		{
			name:     "regtest network",
			args:     []string{"test", "--network", "regtest"},
			expected: "regtest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := createTestContext(app, tt.args)
			result := GetNetwork(c)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEnsureDataDir(t *testing.T) {
	// Create temporary directory for testing
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test", "nested", "dir")

	// Directory should not exist initially
	_, err := os.Stat(testDir)
	assert.True(t, os.IsNotExist(err))

	// Create the directory
	err = EnsureDataDir(testDir)
	require.NoError(t, err)

	// Directory should now exist
	stat, err := os.Stat(testDir)
	require.NoError(t, err)
	assert.True(t, stat.IsDir())

	// Should not error if directory already exists
	err = EnsureDataDir(testDir)
	require.NoError(t, err)
}

func TestGetDefaultConfigPath(t *testing.T) {
	tests := []struct {
		name    string
		appName string
	}{
		{"twinsd", "twinsd"},
		{"twins-cli", "twins-cli"},
		{"twins-wallet", "twins-wallet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := getDefaultConfigPath(tt.appName)
			assert.Contains(t, path, ".twins")
			assert.Contains(t, path, tt.appName+".yml")
		})
	}
}

func TestGetDefaultDataDir(t *testing.T) {
	tests := []struct {
		name     string
		appName  string
		contains string
	}{
		{"twinsd", "twinsd", ".twins"},
		{"twins-wallet", "twins-wallet", ".twins"},
		{"other", "other", ".twins"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := getDefaultDataDir(tt.appName)
			assert.Contains(t, path, ".twins")
			assert.Contains(t, path, tt.contains)
		})
	}
}

// Helper function to create a test context
func createTestContext(app *cli.App, args []string) *cli.Context {
	// Create a flag set
	set := flag.NewFlagSet("test", flag.ContinueOnError)

	// Apply app flags to the flag set
	for _, f := range app.Flags {
		f.Apply(set)
	}

	// Parse the arguments
	if len(args) > 1 {
		err := set.Parse(args[1:])
		if err != nil {
			panic(err)
		}
	}

	return cli.NewContext(app, set, nil)
}

func TestPropagateAppFlags(t *testing.T) {
	app := CreateBaseApp("test", "test", "1.0.0")
	app.Flags = append(app.Flags, CommonRPCClientFlags()...)

	// Add commands with and without their own flags
	app.Commands = []*cli.Command{
		{
			Name: "noflags",
			Action: func(c *cli.Context) error {
				return nil
			},
		},
		{
			Name: "hasflags",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "custom"},
			},
			Action: func(c *cli.Context) error {
				return nil
			},
		},
	}

	PropagateAppFlags(app)

	// Verify app flags were copied to both commands
	for _, cmd := range app.Commands {
		// Should contain all app-level flags
		flagNames := make(map[string]bool)
		for _, f := range cmd.Flags {
			for _, name := range f.Names() {
				flagNames[name] = true
			}
		}

		assert.True(t, flagNames["config"], "command %s should have --config flag", cmd.Name)
		assert.True(t, flagNames["datadir"], "command %s should have --datadir flag", cmd.Name)
		assert.True(t, flagNames["rpc-host"], "command %s should have --rpc-host flag", cmd.Name)
	}

	// Verify command with its own flags still has them
	hasFlagsCmd := app.Commands[1]
	flagNames := make(map[string]bool)
	for _, f := range hasFlagsCmd.Flags {
		for _, name := range f.Names() {
			flagNames[name] = true
		}
	}
	assert.True(t, flagNames["custom"], "hasflags command should retain its own --custom flag")
}

func TestPropagateAppFlags_FlagAfterSubcommand(t *testing.T) {
	app := CreateBaseApp("test", "test", "1.0.0")

	var capturedConfig string
	app.Commands = []*cli.Command{
		{
			Name: "getinfo",
			Action: func(c *cli.Context) error {
				capturedConfig = GetConfigPath(c)
				return nil
			},
		},
	}

	PropagateAppFlags(app)

	// Simulate: test getinfo --config=/custom/path.yml
	err := app.Run([]string{"test", "getinfo", "--config=/custom/path.yml"})
	require.NoError(t, err)
	assert.Equal(t, "/custom/path.yml", capturedConfig)
}

func TestLineageHelpers_FlagBeforeSubcommand(t *testing.T) {
	app := CreateBaseApp("test", "test", "1.0.0")
	app.Flags = append(app.Flags, CommonRPCClientFlags()...)

	var capturedUser, capturedHost string
	var capturedPort int
	var capturedTLS bool
	var capturedTimeout time.Duration

	app.Commands = []*cli.Command{
		{
			Name: "getinfo",
			Action: func(c *cli.Context) error {
				capturedUser = GetStringFromLineage(c, "rpc-user")
				capturedHost = GetStringFromLineage(c, "rpc-host")
				capturedPort = GetIntFromLineage(c, "rpc-port")
				capturedTLS = GetBoolFromLineage(c, "rpc-tls")
				capturedTimeout = GetDurationFromLineage(c, "rpc-timeout")
				return nil
			},
		},
	}
	PropagateAppFlags(app)

	// Flags BEFORE subcommand name (the bug scenario)
	err := app.Run([]string{"test", "--rpc-user=myuser", "--rpc-host=10.0.0.1", "--rpc-port=9999", "--rpc-tls", "--rpc-timeout=60s", "getinfo"})
	require.NoError(t, err)

	assert.Equal(t, "myuser", capturedUser)
	assert.Equal(t, "10.0.0.1", capturedHost)
	assert.Equal(t, 9999, capturedPort)
	assert.True(t, capturedTLS)
	assert.Equal(t, 60*time.Second, capturedTimeout)
}

func TestLineageHelpers_FlagAfterSubcommand(t *testing.T) {
	app := CreateBaseApp("test", "test", "1.0.0")
	app.Flags = append(app.Flags, CommonRPCClientFlags()...)

	var capturedUser string
	var capturedPort int

	app.Commands = []*cli.Command{
		{
			Name: "getinfo",
			Action: func(c *cli.Context) error {
				capturedUser = GetStringFromLineage(c, "rpc-user")
				capturedPort = GetIntFromLineage(c, "rpc-port")
				return nil
			},
		},
	}
	PropagateAppFlags(app)

	// Flags AFTER subcommand name (should also work)
	err := app.Run([]string{"test", "getinfo", "--rpc-user=myuser", "--rpc-port=9999"})
	require.NoError(t, err)

	assert.Equal(t, "myuser", capturedUser)
	assert.Equal(t, 9999, capturedPort)
}

func TestIsSetInLineage(t *testing.T) {
	app := CreateBaseApp("test", "test", "1.0.0")
	app.Flags = append(app.Flags, CommonRPCClientFlags()...)

	var userIsSet, hostIsSet bool

	app.Commands = []*cli.Command{
		{
			Name: "getinfo",
			Action: func(c *cli.Context) error {
				userIsSet = IsSetInLineage(c, "rpc-user")
				hostIsSet = IsSetInLineage(c, "rpc-host")
				return nil
			},
		},
	}
	PropagateAppFlags(app)

	// Only set rpc-user, not rpc-host
	err := app.Run([]string{"test", "--rpc-user=myuser", "getinfo"})
	require.NoError(t, err)

	assert.True(t, userIsSet, "rpc-user was explicitly set")
	assert.False(t, hostIsSet, "rpc-host was not explicitly set")
}

// Benchmark tests
func BenchmarkCreateBaseApp(b *testing.B) {
	for i := 0; i < b.N; i++ {
		CreateBaseApp("benchmark", "benchmark test", "1.0.0")
	}
}

func BenchmarkSetupLogging(b *testing.B) {
	app := CreateBaseApp("benchmark", "benchmark test", "1.0.0")
	args := []string{"benchmark", "--log-level", "info"}
	c := createTestContext(app, args)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SetupLogging(c)
	}
}