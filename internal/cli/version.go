package cli

import (
	"fmt"
	"runtime"
)

// Version information - these will be set at build time using ldflags
var (
	Version         = "4.0.39"
	GitCommit       = "unknown"
	BuildDate       = "unknown"
	DatabaseVersion = "unknown"
	GoVersion       = runtime.Version()
)

// String returns a formatted version string
func String() string {
	return fmt.Sprintf("%s-%s (%s) built with %s",
		Version, GitCommit, BuildDate, GoVersion)
}

// Full returns complete version information
func Full() map[string]string {
	return map[string]string{
		"version":    Version,
		"commit":     GitCommit,
		"buildDate":  BuildDate,
		"dbVersion":  DatabaseVersion,
		"goVersion":  GoVersion,
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"numCPU":     fmt.Sprintf("%d", runtime.NumCPU()),
		"gomaxprocs": fmt.Sprintf("%d", runtime.GOMAXPROCS(0)),
	}
}

// PrintVersion prints version information in a nice format
func PrintVersion() {
	fmt.Printf("TWINS Core %s\n", Version)
	fmt.Printf("Git Commit: %s\n", GitCommit)
	fmt.Printf("Build Date: %s\n", BuildDate)
	fmt.Printf("Database:   %s\n", DatabaseVersion)
	fmt.Printf("Go Version: %s\n", GoVersion)
	fmt.Printf("OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
}
