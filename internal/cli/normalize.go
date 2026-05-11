package cli

import (
	"runtime"
	"strings"

	"github.com/urfave/cli/v2"
)

// NormalizeArgs converts command-line arguments to standard Bitcoin-style format.
// It handles:
//   - Windows /arg style: /testnet → -testnet
//   - GNU --arg style: --testnet → -testnet
//   - -noX pattern: -nosplash → -splash=false
//
// This function should be called before passing args to any flag parser
// to ensure consistent behavior across platforms and compatibility with
// the legacy C++ implementation.
func NormalizeArgs(args []string) []string {
	if len(args) <= 1 {
		return []string{}
	}

	result := make([]string, 0, len(args)-1)
	for _, arg := range args[1:] { // Skip program name
		normalized := normalizeArg(arg)
		if normalized != "" {
			result = append(result, normalized)
		}
	}
	return result
}

// normalizeArg normalizes a single argument to Bitcoin-style format
func normalizeArg(arg string) string {
	// Convert Windows /arg to -arg (only on Windows)
	if runtime.GOOS == "windows" && strings.HasPrefix(arg, "/") && len(arg) > 1 {
		arg = "-" + strings.ToLower(arg[1:])
	}

	// Convert GNU --arg to -arg
	if strings.HasPrefix(arg, "--") && len(arg) > 2 {
		arg = arg[1:] // Strip one dash
	}

	return arg
}

// ProcessNegativeFlags handles -noX patterns (e.g., -nosplash → -splash=false)
// Returns:
//   - processedArgs: arguments with -noX patterns converted
//   - negatedFlags: map of flag names that were negated (e.g., "splash" → true)
//
// This is separated from NormalizeArgs because -noX handling needs to happen
// after normalization but before flag parsing, and callers may need to know
// which flags were explicitly negated.
func ProcessNegativeFlags(args []string) (processedArgs []string, negatedFlags map[string]bool) {
	processedArgs = make([]string, 0, len(args))
	negatedFlags = make(map[string]bool)

	for _, arg := range args {
		lowerArg := strings.ToLower(arg)

		// Handle -noX patterns
		if strings.HasPrefix(lowerArg, "-no") && len(arg) > 3 {
			// Extract flag name after -no (may include =value)
			flagPart := arg[3:] // Keep original case for flag name
			lowerFlagPart := lowerArg[3:]

			// Extract base flag name (before =) for special case checking
			baseFlagName := lowerFlagPart
			if idx := strings.Index(lowerFlagPart, "="); idx != -1 {
				baseFlagName = lowerFlagPart[:idx]
			}

			// Special cases that are not negations:
			// -node, -nodes, -nonce, etc. - words starting with "no" prefix
			switch baseFlagName {
			case "de", "des", "nce", "tify", "tification": // -node, -nodes, -nonce, -notify, -notification
				processedArgs = append(processedArgs, arg)
				continue
			}

			// Convert -noX to -X=false
			negatedFlags[baseFlagName] = true
			processedArgs = append(processedArgs, "-"+flagPart+"=false")
			continue
		}

		processedArgs = append(processedArgs, arg)
	}

	return processedArgs, negatedFlags
}

// NormalizeAndProcessArgs combines NormalizeArgs and ProcessNegativeFlags
// for convenience. Returns normalized args with -noX patterns converted.
func NormalizeAndProcessArgs(args []string) ([]string, map[string]bool) {
	normalized := NormalizeArgs(args)
	return ProcessNegativeFlags(normalized)
}

// CollectAppFlagInfo inspects a slice of cli.Flag definitions and returns two
// lookup maps: valueFlags contains names of flags that consume a value argument
// (string, int, duration, etc.) and boolFlags contains names of boolean flags.
// Both maps include aliases.
func CollectAppFlagInfo(flags []cli.Flag) (valueFlags, boolFlags map[string]bool) {
	valueFlags = make(map[string]bool)
	boolFlags = make(map[string]bool)
	for _, f := range flags {
		names := f.Names()
		if _, ok := f.(*cli.BoolFlag); ok {
			for _, n := range names {
				boolFlags[n] = true
			}
		} else {
			for _, n := range names {
				valueFlags[n] = true
			}
		}
	}
	return
}

// ReorderSubcommandArgs reorders CLI arguments so that recognized app-level
// flags appearing after positional arguments in a subcommand are moved before
// them. This fixes urfave/cli v2's parser miscounting positional args when
// flags follow them.
//
// Example:
//
//	Input:  ["program", "subcmd", "pos1", "--flag=val"]
//	Output: ["program", "subcmd", "--flag=val", "pos1"]
func ReorderSubcommandArgs(args []string, valueFlags, boolFlags map[string]bool) []string {
	if len(args) < 3 {
		return args
	}

	// Find the subcommand index: skip program name (args[0]), then skip any
	// app-level flags before the subcommand. The subcommand is the first
	// non-flag argument after the program name.
	subcmdIdx := -1
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			// Flag before subcommand — skip it (and its value if applicable)
			name := stripFlagName(arg)
			if !strings.Contains(arg, "=") && valueFlags[name] {
				i++ // skip the next arg (flag value)
			}
			continue
		}
		subcmdIdx = i
		break
	}

	if subcmdIdx < 0 || subcmdIdx >= len(args)-1 {
		return args // no subcommand found, or nothing after it
	}

	// Separate the args after the subcommand into flags and positional args.
	rest := args[subcmdIdx+1:]
	var flags []string
	var positionals []string

	for i := 0; i < len(rest); i++ {
		arg := rest[i]

		// "--" terminates flag processing; everything after is positional.
		if arg == "--" {
			positionals = append(positionals, rest[i:]...)
			break
		}

		if strings.HasPrefix(arg, "-") {
			name := stripFlagName(arg)
			if valueFlags[name] {
				if strings.Contains(arg, "=") {
					flags = append(flags, arg)
				} else if i+1 < len(rest) {
					flags = append(flags, arg, rest[i+1])
					i++
				} else {
					// Trailing flag without value — pass through, let urfave report the error.
					flags = append(flags, arg)
				}
			} else if boolFlags[name] {
				flags = append(flags, arg)
			} else {
				// Unrecognized flag — treat as positional to avoid swallowing
				// subcommand-specific flags or unknown args.
				positionals = append(positionals, arg)
			}
		} else {
			positionals = append(positionals, arg)
		}
	}

	result := make([]string, 0, len(args))
	result = append(result, args[:subcmdIdx+1]...)
	result = append(result, flags...)
	result = append(result, positionals...)
	return result
}

// stripFlagName extracts the bare flag name from an argument.
// "--datadir=/path" → "datadir", "-d" → "d", "--rpc-tls" → "rpc-tls"
func stripFlagName(arg string) string {
	name := strings.TrimLeft(arg, "-")
	if idx := strings.Index(name, "="); idx >= 0 {
		name = name[:idx]
	}
	return name
}

// IsHelpFlag checks if the argument is a help flag (-?, -h, -help, --help)
func IsHelpFlag(arg string) bool {
	lower := strings.ToLower(arg)
	switch lower {
	case "-?", "-h", "-help", "--help", "/h", "/help", "/?":
		return true
	}
	return false
}

// IsVersionFlag checks if the argument is a version flag (-v, -V, -version, --version)
func IsVersionFlag(arg string) bool {
	lower := strings.ToLower(arg)
	switch lower {
	case "-v", "-version", "--version", "/v", "/version":
		return true
	}
	return false
}

// HasHelpOrVersionFlag scans args for help or version flags
// Returns (hasHelp, hasVersion)
func HasHelpOrVersionFlag(args []string) (bool, bool) {
	hasHelp := false
	hasVersion := false

	for _, arg := range args {
		if IsHelpFlag(arg) {
			hasHelp = true
		}
		if IsVersionFlag(arg) {
			hasVersion = true
		}
	}

	return hasHelp, hasVersion
}
