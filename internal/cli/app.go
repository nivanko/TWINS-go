package cli

import (
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

// CreateBaseApp creates a base CLI application with common settings
func CreateBaseApp(name, usage, version string) *cli.App {
	app := &cli.App{
		Name:    name,
		Usage:   usage,
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Value:   getDefaultConfigPath(name),
				Usage:   "Path to configuration file",
				EnvVars: []string{"TWINS_CONFIG"},
			},
			&cli.StringFlag{
				Name:    "datadir",
				Aliases: []string{"d"},
				Value:   getDefaultDataDir(name),
				Usage:   "Data directory for blockchain and wallet data",
				EnvVars: []string{"TWINS_DATADIR"},
			},
			&cli.StringFlag{
				Name:    "network",
				Aliases: []string{"n"},
				Value:   "mainnet",
				Usage:   "Network to connect to (mainnet, testnet, regtest)",
				EnvVars: []string{"TWINS_NETWORK"},
			},
			&cli.StringFlag{
				Name:    "log-level",
				Value:   "error",
				Usage:   "Logging level (trace, debug, info, warn, error)",
				EnvVars: []string{"TWINS_LOG_LEVEL"},
			},
			&cli.BoolFlag{
				Name:    "log-json",
				Value:   false,
				Usage:   "Output logs in JSON format",
				EnvVars: []string{"TWINS_LOG_JSON"},
			},
		&cli.StringFlag{
			Name:    "log-file",
			Value:   "",
			Usage:   "Write logs to file (in addition to stdout)",
			EnvVars: []string{"TWINS_LOG_FILE"},
		},
		},
		Authors: []*cli.Author{
			{
				Name:  "TWINS Development Team",
				Email: "dev@twins.dev",
			},
		},
		Copyright: "Copyright © 2025 TWINS Development Team",
	}

	return app
}

// getDefaultConfigPath returns the default configuration file path
func getDefaultConfigPath(appName string) string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".twins", appName+".yml")
}

// getDefaultDataDir returns the default data directory
// All files (blockchain.db, wallet.dat, txcache.dat, etc.) are stored directly in ~/.twins
func getDefaultDataDir(appName string) string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".twins")
}

// SetupLogging configures the global logger based on CLI flags
func SetupLogging(c *cli.Context) error {
	// Parse log level
	level, err := logrus.ParseLevel(c.String("log-level"))
	if err != nil {
		return err
	}
	logrus.SetLevel(level)

	// Set formatter
	if c.Bool("log-json") {
		logrus.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02T15:04:05.000Z",
		})
	} else {
		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		})
	}

	// Log file output is deferred to after config loading (startup_improved.go / twins-gui app.go)
	// so that logging.output from twinsd.yml is respected. The --log-file CLI flag is also
	// handled there since it takes priority over config.

	logrus.WithFields(logrus.Fields{
		"app":       c.App.Name,
		"version":   c.App.Version,
		"log_level": level.String(),
		"log_json":  c.Bool("log-json"),
	}).Debug("Logging initialized")

	return nil
}

// getStringFromLineage searches for a string flag value through context lineage
// This allows flags specified before subcommand (e.g., "twinsd -d /path start")
// to be found when queried from subcommand context
func getStringFromLineage(c *cli.Context, name string) string {
	// First check current context
	if c.IsSet(name) {
		return c.String(name)
	}
	// Search through parent contexts (lineage)
	for _, ctx := range c.Lineage() {
		if ctx.IsSet(name) {
			return ctx.String(name)
		}
	}
	// Return default value from current context
	return c.String(name)
}

// IsSetInLineage checks if a flag was explicitly set in any context in the lineage.
func IsSetInLineage(c *cli.Context, name string) bool {
	if c.IsSet(name) {
		return true
	}
	for _, ctx := range c.Lineage() {
		if ctx.IsSet(name) {
			return true
		}
	}
	return false
}

// GetStringFromLineage searches for a string flag value through context lineage.
// Exported wrapper for use by cmd packages (twins-cli, twinsd).
func GetStringFromLineage(c *cli.Context, name string) string {
	return getStringFromLineage(c, name)
}

// GetIntFromLineage searches for an int flag value through context lineage.
// Same pattern as getStringFromLineage — allows flags before subcommand to be found.
func GetIntFromLineage(c *cli.Context, name string) int {
	return getIntFromLineage(c, name)
}

// GetBoolFromLineage searches for a bool flag value through context lineage.
func GetBoolFromLineage(c *cli.Context, name string) bool {
	return getBoolFromLineage(c, name)
}

// GetDurationFromLineage searches for a duration flag value through context lineage.
func GetDurationFromLineage(c *cli.Context, name string) time.Duration {
	return getDurationFromLineage(c, name)
}

// getIntFromLineage searches for an int flag value through context lineage.
// Same pattern as getStringFromLineage — allows flags before subcommand to be found.
func getIntFromLineage(c *cli.Context, name string) int {
	if c.IsSet(name) {
		return c.Int(name)
	}
	for _, ctx := range c.Lineage() {
		if ctx.IsSet(name) {
			return ctx.Int(name)
		}
	}
	return c.Int(name)
}

// getBoolFromLineage searches for a bool flag value through context lineage.
func getBoolFromLineage(c *cli.Context, name string) bool {
	if c.IsSet(name) {
		return c.Bool(name)
	}
	for _, ctx := range c.Lineage() {
		if ctx.IsSet(name) {
			return ctx.Bool(name)
		}
	}
	return c.Bool(name)
}

// getDurationFromLineage searches for a duration flag value through context lineage.
func getDurationFromLineage(c *cli.Context, name string) time.Duration {
	if c.IsSet(name) {
		return c.Duration(name)
	}
	for _, ctx := range c.Lineage() {
		if ctx.IsSet(name) {
			return ctx.Duration(name)
		}
	}
	return c.Duration(name)
}

// GetConfigPath returns the configuration file path from context
// Searches through context lineage to find flag specified before subcommand
func GetConfigPath(c *cli.Context) string {
	return getStringFromLineage(c, "config")
}

// GetDataDir returns the data directory from context
// Searches through context lineage to find flag specified before subcommand
func GetDataDir(c *cli.Context) string {
	return getStringFromLineage(c, "datadir")
}

// GetNetwork returns the network name from context
// Searches through context lineage to find flag specified before subcommand
func GetNetwork(c *cli.Context) string {
	return getStringFromLineage(c, "network")
}

// EnsureDataDir creates the data directory if it doesn't exist
func EnsureDataDir(dataDir string) error {
	return os.MkdirAll(dataDir, 0755)
}

// GetTwinsBaseDir returns the base .twins directory (~/.twins)
// This is where config files live (twinsd.yml, twins.conf)
func GetTwinsBaseDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".twins")
}

// PropagateAppFlags copies app-level flags to all subcommands, allowing
// flags like --config to be specified after the subcommand name.
// This matches the legacy C++ behavior where flag position doesn't matter.
// In urfave/cli/v2, app-level flags are only parsed when placed before the
// subcommand. This function works around that limitation.
func PropagateAppFlags(app *cli.App) {
	for _, cmd := range app.Commands {
		cmd.Flags = append(cmd.Flags, app.Flags...)
	}
}

// ConfigWasExplicitlySet checks if --config flag was explicitly provided by user
func ConfigWasExplicitlySet(c *cli.Context) bool {
	for _, ctx := range c.Lineage() {
		if ctx.IsSet("config") {
			return true
		}
	}
	return false
}

// DiscoverConfigFile searches for config files in standard locations.
// Only twinsd.yml is supported. If a legacy twins.conf exists, a warning is logged.
// Returns empty string if no config file found.
func DiscoverConfigFile() string {
	baseDir := GetTwinsBaseDir()

	ymlPath := filepath.Join(baseDir, "twinsd.yml")
	if _, err := os.Stat(ymlPath); err == nil {
		return ymlPath
	}

	// Warn about legacy twins.conf if present
	confPath := filepath.Join(baseDir, "twins.conf")
	if _, err := os.Stat(confPath); err == nil {
		logrus.WithField("path", confPath).Warn(
			"Found legacy twins.conf but this format is no longer supported. " +
				"Please migrate to twinsd.yml (YAML format). " +
				"Run twinsd once to auto-generate a default twinsd.yml.")
	}

	return ""
}

// GetEffectiveConfigPath returns the config file path to use
// If --config explicitly set, returns that path and explicit=true
// Otherwise returns auto-discovered config (or empty) and explicit=false
func GetEffectiveConfigPath(c *cli.Context) (path string, explicit bool) {
	if ConfigWasExplicitlySet(c) {
		return GetConfigPath(c), true
	}
	return DiscoverConfigFile(), false
}
