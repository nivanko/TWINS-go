package crypto

import (
	"testing"
)

func TestAddressGeneration(t *testing.T) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Test mainnet address generation
	address := NewAddressFromPubKey(keyPair.Public, MainNetPubKeyHashAddrID)
	if address == nil {
		t.Fatal("Address is nil")
	}

	addressStr := address.String()
	if len(addressStr) == 0 {
		t.Error("Address string should not be empty")
	}

	// Test address decoding
	decodedAddress, err := DecodeAddress(addressStr)
	if err != nil {
		t.Fatalf("DecodeAddress failed: %v", err)
	}

	if decodedAddress.NetID() != MainNetPubKeyHashAddrID {
		t.Error("Decoded address network ID doesn't match")
	}

	// Test script address
	script := []byte{0x76, 0xa9, 0x14} // OP_DUP OP_HASH160 <push 20>
	scriptAddr := NewScriptAddress(script, MainNetPubKeyHashAddrID)
	if !scriptAddr.IsScript() {
		t.Error("Script address should be identified as script")
	}

	if address.IsScript() {
		t.Error("Regular address should not be identified as script")
	}
}

func TestBase58Encoding(t *testing.T) {
	testData := []byte("Hello, Base58!")

	// Test Base58 encoding
	encoded := Base58Encode(testData)
	if len(encoded) == 0 {
		t.Error("Base58 encoded string should not be empty")
	}

	// Test Base58 decoding
	decoded, err := Base58Decode(encoded)
	if err != nil {
		t.Fatalf("Base58Decode failed: %v", err)
	}

	if string(decoded) != string(testData) {
		t.Error("Base58 round-trip failed")
	}

	// Test Base58Check encoding
	checkEncoded := Base58CheckEncode(testData)
	if len(checkEncoded) == 0 {
		t.Error("Base58Check encoded string should not be empty")
	}

	// Test Base58Check decoding
	checkDecoded, err := Base58CheckDecode(checkEncoded)
	if err != nil {
		t.Fatalf("Base58CheckDecode failed: %v", err)
	}

	if string(checkDecoded) != string(testData) {
		t.Error("Base58Check round-trip failed")
	}

	// Test invalid Base58Check (wrong checksum)
	invalidCheck := checkEncoded[:len(checkEncoded)-1] + "1" // Change last character
	_, err = Base58CheckDecode(invalidCheck)
	if err == nil {
		t.Error("Base58CheckDecode should fail with invalid checksum")
	}
}

func TestWIFEncoding(t *testing.T) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Test WIF encoding (uncompressed)
	wifUncompressed := EncodePrivateKeyWIF(keyPair.Private, false, MainNetPubKeyHashAddrID)
	if len(wifUncompressed) == 0 {
		t.Error("WIF string should not be empty")
	}

	// Test WIF encoding (compressed)
	wifCompressed := EncodePrivateKeyWIF(keyPair.Private, true, MainNetPubKeyHashAddrID)
	if len(wifCompressed) == 0 {
		t.Error("WIF compressed string should not be empty")
	}

	if wifUncompressed == wifCompressed {
		t.Error("Compressed and uncompressed WIF should be different")
	}

	// Test WIF decoding (uncompressed)
	decodedPrivate, compressed, err := DecodePrivateKeyWIF(wifUncompressed)
	if err != nil {
		t.Fatalf("DecodePrivateKeyWIF failed: %v", err)
	}

	if compressed {
		t.Error("Should decode as uncompressed")
	}

	// Test WIF decoding (compressed)
	decodedPrivateComp, compressedFlag, err := DecodePrivateKeyWIF(wifCompressed)
	if err != nil {
		t.Fatalf("DecodePrivateKeyWIF (compressed) failed: %v", err)
	}

	if !compressedFlag {
		t.Error("Should decode as compressed")
	}

	// Keys should be the same
	if decodedPrivate.Hex() != keyPair.Private.Hex() {
		t.Error("Decoded private key doesn't match original")
	}

	if decodedPrivateComp.Hex() != keyPair.Private.Hex() {
		t.Error("Decoded compressed private key doesn't match original")
	}
}

func TestAddressValidation(t *testing.T) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Create test addresses
	mainnetAddr := NewAddressFromPubKey(keyPair.Public, MainNetPubKeyHashAddrID)
	testnetAddr := NewAddressFromPubKey(keyPair.Public, TestNetPubKeyHashAddrID)
	twinsAddr := NewAddressFromPubKey(keyPair.Public, TWINSMainNetPubKeyHashAddrID)

	mainnetStr := mainnetAddr.String()
	testnetStr := testnetAddr.String()
	twinsStr := twinsAddr.String()

	// Test validation functions
	if !IsValidMainNetAddress(mainnetStr) {
		t.Error("Should validate mainnet address")
	}

	if !IsValidMainNetAddress(twinsStr) {
		t.Error("Should validate TWINS mainnet address")
	}

	if IsValidMainNetAddress(testnetStr) {
		t.Error("Should not validate testnet address as mainnet")
	}

	if !IsValidTestNetAddress(testnetStr) {
		t.Error("Should validate testnet address")
	}

	if IsValidTestNetAddress(mainnetStr) {
		t.Error("Should not validate mainnet address as testnet")
	}

	// Test address type detection
	addrType, err := GetAddressType(mainnetStr)
	if err != nil {
		t.Fatalf("GetAddressType failed: %v", err)
	}

	if addrType != "P2PKH-MainNet" {
		t.Errorf("Expected P2PKH-MainNet, got %s", addrType)
	}

	testType, err := GetAddressType(testnetStr)
	if err != nil {
		t.Fatalf("GetAddressType failed: %v", err)
	}

	if testType != "P2PKH-TestNet" {
		t.Errorf("Expected P2PKH-TestNet, got %s", testType)
	}
}

func TestScriptGeneration(t *testing.T) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	// Test P2PKH script generation
	address := NewAddressFromPubKey(keyPair.Public, MainNetPubKeyHashAddrID)
	script := address.CreateScriptPubKey()

	// P2PKH script should be 25 bytes: OP_DUP OP_HASH160 <20-byte-hash> OP_EQUALVERIFY OP_CHECKSIG
	if len(script) != 25 {
		t.Errorf("P2PKH script should be 25 bytes, got %d", len(script))
	}

	if script[0] != 0x76 { // OP_DUP
		t.Error("P2PKH script should start with OP_DUP")
	}

	if script[1] != 0xa9 { // OP_HASH160
		t.Error("P2PKH script should have OP_HASH160")
	}

	if script[2] != 0x14 { // Push 20 bytes
		t.Error("P2PKH script should push 20 bytes")
	}

	// Test P2SH script generation
	testScript := []byte{0x51} // OP_TRUE
	scriptAddress := NewScriptAddress(testScript, MainNetPubKeyHashAddrID)
	p2shScript := scriptAddress.CreateScriptPubKey()

	// P2SH script should be 23 bytes: OP_HASH160 <20-byte-hash> OP_EQUAL
	if len(p2shScript) != 23 {
		t.Errorf("P2SH script should be 23 bytes, got %d", len(p2shScript))
	}

	if p2shScript[0] != 0xa9 { // OP_HASH160
		t.Error("P2SH script should start with OP_HASH160")
	}
}

func TestAddressErrors(t *testing.T) {
	// Test invalid address decoding
	_, err := DecodeAddress("invalid_address")
	if err == nil {
		t.Error("Should fail with invalid address")
	}

	// Test address validation
	err = ValidateAddress("invalid_address")
	if err == nil {
		t.Error("Should fail address validation")
	}

	// Test invalid WIF
	_, _, err = DecodePrivateKeyWIF("invalid_wif")
	if err == nil {
		t.Error("Should fail with invalid WIF")
	}

	// Test Base58 with invalid characters
	_, err = Base58Decode("0OIl") // Contains invalid characters
	if err == nil {
		t.Error("Should fail with invalid Base58 characters")
	}
}

func BenchmarkAddressGeneration(b *testing.B) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		b.Fatalf("GenerateKeyPair failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewAddressFromPubKey(keyPair.Public, MainNetPubKeyHashAddrID)
	}
}

func BenchmarkBase58Encode(b *testing.B) {
	data := []byte("benchmark data for Base58 encoding performance test")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Base58Encode(data)
	}
}

func BenchmarkBase58Decode(b *testing.B) {
	data := []byte("benchmark data for Base58 decoding")
	encoded := Base58Encode(data)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Base58Decode(encoded)
	}
}

func BenchmarkBase58CheckEncode(b *testing.B) {
	data := []byte("benchmark data for Base58Check encoding")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Base58CheckEncode(data)
	}
}

func BenchmarkWIFEncoding(b *testing.B) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		b.Fatalf("GenerateKeyPair failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EncodePrivateKeyWIF(keyPair.Private, true, MainNetPubKeyHashAddrID)
	}
}

func TestGetPubKeyHashNetworkID(t *testing.T) {
	cases := []struct {
		name    string
		network string
		want    byte
	}{
		{"mainnet", "mainnet", MainNetPubKeyHashAddrID},
		{"testnet", "testnet", TestNetPubKeyHashAddrID},
		{"regtest uses testnet prefix", "regtest", TestNetPubKeyHashAddrID},
		{"empty string falls back to mainnet", "", MainNetPubKeyHashAddrID},
		{"unknown falls back to mainnet", "wonderland", MainNetPubKeyHashAddrID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetPubKeyHashNetworkID(tc.network); got != tc.want {
				t.Errorf("GetPubKeyHashNetworkID(%q) = 0x%02x, want 0x%02x", tc.network, got, tc.want)
			}
		})
	}
}

func TestGetScriptHashNetworkID(t *testing.T) {
	cases := []struct {
		name    string
		network string
		want    byte
	}{
		{"mainnet", "mainnet", MainNetScriptHashAddrID},
		{"testnet", "testnet", TestNetScriptHashAddrID},
		{"regtest uses testnet prefix", "regtest", TestNetScriptHashAddrID},
		{"empty string falls back to mainnet", "", MainNetScriptHashAddrID},
		{"unknown falls back to mainnet", "wonderland", MainNetScriptHashAddrID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetScriptHashNetworkID(tc.network); got != tc.want {
				t.Errorf("GetScriptHashNetworkID(%q) = 0x%02x, want 0x%02x", tc.network, got, tc.want)
			}
		})
	}
}