package cli

import (
	"runtime"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestNormalizeArgs_Empty(t *testing.T) {
	args := []string{"program"}
	result := NormalizeArgs(args)
	if len(result) != 0 {
		t.Errorf("expected empty result, got %v", result)
	}
}

func TestNormalizeArgs_DoubleHyphen(t *testing.T) {
	args := []string{"program", "--testnet", "--datadir=/path"}
	result := NormalizeArgs(args)

	expected := []string{"-testnet", "-datadir=/path"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(result), result)
	}
	for i, exp := range expected {
		if result[i] != exp {
			t.Errorf("arg %d: expected %q, got %q", i, exp, result[i])
		}
	}
}

func TestNormalizeArgs_SingleHyphen(t *testing.T) {
	args := []string{"program", "-testnet", "-datadir=/path"}
	result := NormalizeArgs(args)

	expected := []string{"-testnet", "-datadir=/path"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(result), result)
	}
	for i, exp := range expected {
		if result[i] != exp {
			t.Errorf("arg %d: expected %q, got %q", i, exp, result[i])
		}
	}
}

func TestNormalizeArgs_WindowsSlash(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific test")
	}

	args := []string{"program", "/testnet", "/datadir=C:\\path"}
	result := NormalizeArgs(args)

	// Windows converts to lowercase
	if len(result) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(result), result)
	}
	if result[0] != "-testnet" {
		t.Errorf("expected -testnet, got %q", result[0])
	}
}

func TestProcessNegativeFlags_NoSplash(t *testing.T) {
	args := []string{"-nosplash", "-testnet"}
	result, negated := ProcessNegativeFlags(args)

	if !negated["splash"] {
		t.Error("expected splash to be negated")
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(result), result)
	}
	if result[0] != "-splash=false" {
		t.Errorf("expected -splash=false, got %q", result[0])
	}
	if result[1] != "-testnet" {
		t.Errorf("expected -testnet, got %q", result[1])
	}
}

func TestProcessNegativeFlags_NoTestnet(t *testing.T) {
	args := []string{"-notestnet", "-datadir=/path"}
	result, negated := ProcessNegativeFlags(args)

	if !negated["testnet"] {
		t.Error("expected testnet to be negated")
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(result), result)
	}
	if result[0] != "-testnet=false" {
		t.Errorf("expected -testnet=false, got %q", result[0])
	}
}

func TestProcessNegativeFlags_Node(t *testing.T) {
	// -node should NOT be treated as -no + de
	args := []string{"-node=127.0.0.1"}
	result, negated := ProcessNegativeFlags(args)

	if negated["de"] {
		t.Error("-node should not be treated as negation of 'de'")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 arg, got %d: %v", len(result), result)
	}
	if result[0] != "-node=127.0.0.1" {
		t.Errorf("expected -node=127.0.0.1, got %q", result[0])
	}
}

func TestNormalizeAndProcessArgs(t *testing.T) {
	args := []string{"program", "--nosplash", "-testnet"}
	result, negated := NormalizeAndProcessArgs(args)

	if !negated["splash"] {
		t.Error("expected splash to be negated")
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(result), result)
	}
	if result[0] != "-splash=false" {
		t.Errorf("expected -splash=false, got %q", result[0])
	}
	if result[1] != "-testnet" {
		t.Errorf("expected -testnet, got %q", result[1])
	}
}

func TestIsHelpFlag(t *testing.T) {
	testCases := []struct {
		arg      string
		expected bool
	}{
		{"-?", true},
		{"-h", true},
		{"-help", true},
		{"--help", true},
		{"-H", true},
		{"-HELP", true},
		{"-version", false},
		{"-testnet", false},
	}

	for _, tc := range testCases {
		result := IsHelpFlag(tc.arg)
		if result != tc.expected {
			t.Errorf("IsHelpFlag(%q): expected %v, got %v", tc.arg, tc.expected, result)
		}
	}
}

func TestIsVersionFlag(t *testing.T) {
	testCases := []struct {
		arg      string
		expected bool
	}{
		{"-v", true},
		{"-V", true},
		{"-version", true},
		{"--version", true},
		{"-VERSION", true},
		{"-help", false},
		{"-testnet", false},
	}

	for _, tc := range testCases {
		result := IsVersionFlag(tc.arg)
		if result != tc.expected {
			t.Errorf("IsVersionFlag(%q): expected %v, got %v", tc.arg, tc.expected, result)
		}
	}
}

func TestCollectAppFlagInfo(t *testing.T) {
	flags := []cli.Flag{
		&cli.StringFlag{Name: "datadir", Aliases: []string{"d"}},
		&cli.IntFlag{Name: "rpc-port"},
		&cli.BoolFlag{Name: "rpc-tls"},
		&cli.DurationFlag{Name: "rpc-timeout"},
	}

	valueFlags, boolFlags := CollectAppFlagInfo(flags)

	// Value flags
	if !valueFlags["datadir"] {
		t.Error("expected datadir in valueFlags")
	}
	if !valueFlags["d"] {
		t.Error("expected alias 'd' in valueFlags")
	}
	if !valueFlags["rpc-port"] {
		t.Error("expected rpc-port in valueFlags")
	}
	if !valueFlags["rpc-timeout"] {
		t.Error("expected rpc-timeout in valueFlags")
	}

	// Bool flags
	if !boolFlags["rpc-tls"] {
		t.Error("expected rpc-tls in boolFlags")
	}

	// Cross-check
	if valueFlags["rpc-tls"] {
		t.Error("rpc-tls should not be in valueFlags")
	}
	if boolFlags["datadir"] {
		t.Error("datadir should not be in boolFlags")
	}
}

func TestReorderSubcommandArgs(t *testing.T) {
	valueFlags := map[string]bool{"datadir": true, "d": true, "rpc-host": true, "rpc-port": true}
	boolFlags := map[string]bool{"rpc-tls": true}

	testCases := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "flag after positional arg (=form)",
			args:     []string{"twins-cli", "rpc-cert-fingerprint", "cert.crt", "--datadir=/path"},
			expected: []string{"twins-cli", "rpc-cert-fingerprint", "--datadir=/path", "cert.crt"},
		},
		{
			name:     "flag after positional arg (space form)",
			args:     []string{"twins-cli", "getblock", "abc123", "--rpc-host", "1.2.3.4"},
			expected: []string{"twins-cli", "getblock", "--rpc-host", "1.2.3.4", "abc123"},
		},
		{
			name:     "flag before positional arg (already correct)",
			args:     []string{"twins-cli", "rpc-cert-fingerprint", "--datadir=/path", "cert.crt"},
			expected: []string{"twins-cli", "rpc-cert-fingerprint", "--datadir=/path", "cert.crt"},
		},
		{
			name:     "flag before command (untouched)",
			args:     []string{"twins-cli", "--datadir=/path", "rpc-cert-fingerprint", "cert.crt"},
			expected: []string{"twins-cli", "--datadir=/path", "rpc-cert-fingerprint", "cert.crt"},
		},
		{
			name:     "multiple flags mixed with args",
			args:     []string{"twins-cli", "sendtoaddress", "addr1", "100", "--rpc-host", "1.2.3.4", "--rpc-tls", "--datadir=/path"},
			expected: []string{"twins-cli", "sendtoaddress", "--rpc-host", "1.2.3.4", "--rpc-tls", "--datadir=/path", "addr1", "100"},
		},
		{
			name:     "bool flag after positional",
			args:     []string{"twins-cli", "getinfo", "--rpc-tls"},
			expected: []string{"twins-cli", "getinfo", "--rpc-tls"},
		},
		{
			name:     "alias flag after positional",
			args:     []string{"twins-cli", "getblock", "abc123", "-d", "/path"},
			expected: []string{"twins-cli", "getblock", "-d", "/path", "abc123"},
		},
		{
			name:     "double dash terminates reordering",
			args:     []string{"twins-cli", "cmd", "arg1", "--", "--datadir=/path"},
			expected: []string{"twins-cli", "cmd", "arg1", "--", "--datadir=/path"},
		},
		{
			name:     "unrecognized flag treated as positional",
			args:     []string{"twins-cli", "cmd", "arg1", "--unknown=val"},
			expected: []string{"twins-cli", "cmd", "arg1", "--unknown=val"},
		},
		{
			name:     "no subcommand (just program)",
			args:     []string{"twins-cli"},
			expected: []string{"twins-cli"},
		},
		{
			name:     "program and subcommand only",
			args:     []string{"twins-cli", "getinfo"},
			expected: []string{"twins-cli", "getinfo"},
		},
		{
			name:     "empty args",
			args:     []string{},
			expected: []string{},
		},
		{
			name:     "flags interleaved with multiple positional args",
			args:     []string{"twins-cli", "cmd", "pos1", "--datadir=/a", "pos2", "--rpc-port", "9999", "pos3"},
			expected: []string{"twins-cli", "cmd", "--datadir=/a", "--rpc-port", "9999", "pos1", "pos2", "pos3"},
		},
		{
			name:     "app flag before subcommand with space value",
			args:     []string{"twins-cli", "--rpc-host", "1.2.3.4", "getblock", "abc123", "--datadir=/path"},
			expected: []string{"twins-cli", "--rpc-host", "1.2.3.4", "getblock", "--datadir=/path", "abc123"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ReorderSubcommandArgs(tc.args, valueFlags, boolFlags)
			if len(result) != len(tc.expected) {
				t.Fatalf("expected %d args %v, got %d args %v", len(tc.expected), tc.expected, len(result), result)
			}
			for i, exp := range tc.expected {
				if result[i] != exp {
					t.Errorf("arg %d: expected %q, got %q\n  full result: %v", i, exp, result[i], result)
				}
			}
		})
	}
}

func TestHasHelpOrVersionFlag(t *testing.T) {
	testCases := []struct {
		args        []string
		expectHelp  bool
		expectVer   bool
	}{
		{[]string{"-testnet"}, false, false},
		{[]string{"-help"}, true, false},
		{[]string{"-version"}, false, true},
		{[]string{"-h", "-testnet"}, true, false},
		{[]string{"-testnet", "-V"}, false, true},
		{[]string{"-help", "-version"}, true, true},
	}

	for _, tc := range testCases {
		hasHelp, hasVer := HasHelpOrVersionFlag(tc.args)
		if hasHelp != tc.expectHelp {
			t.Errorf("HasHelpOrVersionFlag(%v): expected help=%v, got %v", tc.args, tc.expectHelp, hasHelp)
		}
		if hasVer != tc.expectVer {
			t.Errorf("HasHelpOrVersionFlag(%v): expected version=%v, got %v", tc.args, tc.expectVer, hasVer)
		}
	}
}
