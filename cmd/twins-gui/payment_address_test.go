package main

import (
	"strings"
	"testing"

	binary "github.com/twins-dev/twins-core/internal/storage/binary"
	"github.com/twins-dev/twins-core/pkg/crypto"
)

// buildP2PKHScript constructs a standard P2PKH scriptPubKey:
// OP_DUP OP_HASH160 <20-byte hash> OP_EQUALVERIFY OP_CHECKSIG
func buildP2PKHScript(t *testing.T, pubKeyHash [20]byte) []byte {
	t.Helper()
	script := make([]byte, 25)
	script[0] = 0x76 // OP_DUP
	script[1] = 0xa9 // OP_HASH160
	script[2] = 0x14 // push 20 bytes
	copy(script[3:23], pubKeyHash[:])
	script[23] = 0x88 // OP_EQUALVERIFY
	script[24] = 0xac // OP_CHECKSIG
	return script
}

// buildP2SHScript constructs a standard P2SH scriptPubKey:
// OP_HASH160 <20-byte hash> OP_EQUAL
func buildP2SHScript(t *testing.T, scriptHash [20]byte) []byte {
	t.Helper()
	script := make([]byte, 23)
	script[0] = 0xa9 // OP_HASH160
	script[1] = 0x14 // push 20 bytes
	copy(script[2:22], scriptHash[:])
	script[22] = 0x87 // OP_EQUAL
	return script
}

// expectedPrefixForP2PKH derives the canonical address that NewAddressFromHash
// would produce for a given hash and netID, and returns its first character.
// This avoids hardcoding base58check encoding details in the test.
func expectedFirstCharForP2PKH(t *testing.T, hash [20]byte, netID byte) byte {
	t.Helper()
	addr, err := crypto.NewAddressFromHash(hash[:], netID)
	if err != nil {
		t.Fatalf("NewAddressFromHash: %v", err)
	}
	s := addr.String()
	if s == "" {
		t.Fatalf("NewAddressFromHash returned empty string")
	}
	return s[0]
}

func TestExtractAddressFromScriptPubKey_P2PKH_NetworkRouting(t *testing.T) {
	// Sanity-check our expectations: confirm the script we build is recognized
	// as P2PKH and the hash round-trips through AnalyzeScript.
	var hash [20]byte
	for i := range hash {
		hash[i] = byte(i + 1)
	}
	script := buildP2PKHScript(t, hash)
	scriptType, parsed := binary.AnalyzeScript(script)
	if scriptType != binary.ScriptTypeP2PKH {
		t.Fatalf("AnalyzeScript classified P2PKH script as type %d", scriptType)
	}
	if parsed != hash {
		t.Fatalf("AnalyzeScript hash mismatch")
	}

	cases := []struct {
		name        string
		networkName string
		netID       byte
	}{
		{"mainnet", "mainnet", crypto.MainNetPubKeyHashAddrID},
		{"testnet", "testnet", crypto.TestNetPubKeyHashAddrID},
		{"regtest uses testnet prefix", "regtest", crypto.TestNetPubKeyHashAddrID},
		{"empty falls back to mainnet", "", crypto.MainNetPubKeyHashAddrID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAddressFromScriptPubKey(script, tc.networkName)
			if got == "" {
				t.Fatalf("extractAddressFromScriptPubKey returned empty string")
			}

			expectedAddr, err := crypto.NewAddressFromHash(hash[:], tc.netID)
			if err != nil {
				t.Fatalf("NewAddressFromHash: %v", err)
			}
			want := expectedAddr.String()
			if got != want {
				t.Errorf("network=%q: got %q, want %q", tc.networkName, got, want)
			}

			// Also assert the leading character matches what the netID byte
			// produces, which is the user-visible "wrong prefix" symptom.
			if got[0] != expectedFirstCharForP2PKH(t, hash, tc.netID) {
				t.Errorf("network=%q: leading char %q does not match expected prefix for netID 0x%02x",
					tc.networkName, got[0], tc.netID)
			}
		})
	}
}

func TestExtractAddressFromScriptPubKey_P2SH_NetworkRouting(t *testing.T) {
	var hash [20]byte
	for i := range hash {
		hash[i] = byte(0xFF - i)
	}
	script := buildP2SHScript(t, hash)
	scriptType, parsed := binary.AnalyzeScript(script)
	if scriptType != binary.ScriptTypeP2SH {
		t.Fatalf("AnalyzeScript classified P2SH script as type %d", scriptType)
	}
	if parsed != hash {
		t.Fatalf("AnalyzeScript hash mismatch")
	}

	cases := []struct {
		name        string
		networkName string
		netID       byte
	}{
		{"mainnet", "mainnet", crypto.MainNetScriptHashAddrID},
		{"testnet", "testnet", crypto.TestNetScriptHashAddrID},
		{"regtest uses testnet prefix", "regtest", crypto.TestNetScriptHashAddrID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAddressFromScriptPubKey(script, tc.networkName)
			if got == "" {
				t.Fatalf("extractAddressFromScriptPubKey returned empty string")
			}

			expectedAddr, err := crypto.NewAddressFromHash(hash[:], tc.netID)
			if err != nil {
				t.Fatalf("NewAddressFromHash: %v", err)
			}
			if got != expectedAddr.String() {
				t.Errorf("network=%q: got %q, want %q", tc.networkName, got, expectedAddr.String())
			}
		})
	}
}

// TestExtractAddressFromScriptPubKey_MainnetRegression locks in the existing
// mainnet behavior by checking the leading 'W' prefix expected from
// MainNetPubKeyHashAddrID = 0x49. If this character ever changes, every
// production user's Payment Stats tab is affected.
func TestExtractAddressFromScriptPubKey_MainnetRegression(t *testing.T) {
	var hash [20]byte
	for i := range hash {
		hash[i] = byte(0xAA)
	}
	script := buildP2PKHScript(t, hash)

	got := extractAddressFromScriptPubKey(script, "mainnet")
	if got == "" {
		t.Fatalf("extractAddressFromScriptPubKey returned empty string")
	}
	if !strings.HasPrefix(got, "W") {
		t.Errorf("mainnet P2PKH address should start with 'W', got %q", got)
	}
}

func TestExtractAddressFromScriptPubKey_NonStandardScriptReturnsEmpty(t *testing.T) {
	// AnalyzeScript on a clearly invalid 3-byte script returns an unknown type.
	// extractAddressFromScriptPubKey should return "" in that case.
	got := extractAddressFromScriptPubKey([]byte{0x00, 0x01, 0x02}, "mainnet")
	if got != "" {
		t.Errorf("expected empty string for non-standard script, got %q", got)
	}
}
