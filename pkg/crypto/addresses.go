package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
)

// Address network ID constants (TWINS legacy values from chainparams.cpp:304)
const (
	// TWINS MainNet address prefixes (legacy values)
	MainNetPubKeyHashAddrID = 0x49 // 73 decimal - W... addresses
	MainNetScriptHashAddrID = 0x53 // 83 decimal - a... addresses

	// TestNet address prefixes (Bitcoin-compatible for testing)
	TestNetPubKeyHashAddrID = 0x6F // m... or n... addresses
	TestNetScriptHashAddrID = 0xC4 // 2... addresses

	// Private key WIF prefix (TWINS legacy)
	PrivateKeyID = 0x42 // 66 decimal - TWINS WIF format

	// Deprecated Bitcoin prefixes (keep for reference)
	// BitcoinMainNetPubKeyHashAddrID = 0x00 // 1... addresses
	// BitcoinMainNetScriptHashAddrID = 0x05 // 3... addresses
	// BitcoinPrivateKeyID            = 0x80 // 5.../K.../L... addresses

	// Legacy alias for backwards compatibility
	TWINSMainNetPubKeyHashAddrID = MainNetPubKeyHashAddrID
	TWINSTestNetPubKeyHashAddrID = TestNetPubKeyHashAddrID
)

// BIP32 HD wallet prefixes (TWINS legacy from chainparams.cpp:307-308)
var (
	// Extended public key prefix (xpub equivalent)
	ExtPubKeyPrefix = []byte{0x02, 0x2D, 0x25, 0x33}

	// Extended private key prefix (xprv equivalent)
	ExtPrivKeyPrefix = []byte{0x02, 0x21, 0x31, 0x2B}

	// BIP44 coin type for TWINS (from chainparams.cpp:311)
	CoinType = uint32(970)
)

// GetScriptHashNetworkID returns the appropriate P2SH address prefix for the given network name
func GetScriptHashNetworkID(networkName string) byte {
	switch networkName {
	case "testnet":
		return TestNetScriptHashAddrID
	case "regtest":
		return TestNetScriptHashAddrID // RegTest uses testnet addresses
	default:
		return MainNetScriptHashAddrID
	}
}

// GetPubKeyHashNetworkID returns the appropriate P2PKH address prefix for the given network name.
// RegTest uses testnet prefixes to match legacy chainparams.cpp behavior.
func GetPubKeyHashNetworkID(networkName string) byte {
	switch networkName {
	case "testnet":
		return TestNetPubKeyHashAddrID
	case "regtest":
		return TestNetPubKeyHashAddrID // RegTest uses testnet addresses
	default:
		return MainNetPubKeyHashAddrID
	}
}

// Address represents a TWINS address
type Address struct {
	hash    []byte
	netID   byte
	version byte
}

// Base58 alphabet (Bitcoin standard)
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// Create reverse lookup table for Base58
var base58Map = make(map[rune]int)

func init() {
	for i, c := range base58Alphabet {
		base58Map[c] = i
	}
}

// NewAddressFromPubKey creates a new address from a public key
func NewAddressFromPubKey(pubKey *PublicKey, netID byte) *Address {
	// Use compressed public key for address generation
	pubKeyBytes := pubKey.CompressedBytes()

	// Hash160 the public key
	hash160 := Hash160(pubKeyBytes)

	return &Address{
		hash:    hash160,
		netID:   netID,
		version: 0, // Standard P2PKH version
	}
}

// NewAddressFromHash creates a new address from a hash160
func NewAddressFromHash(hash160 []byte, netID byte) (*Address, error) {
	if len(hash160) != 20 {
		return nil, errors.New("hash160 must be 20 bytes")
	}

	hashCopy := make([]byte, 20)
	copy(hashCopy, hash160)

	return &Address{
		hash:    hashCopy,
		netID:   netID,
		version: 0,
	}, nil
}

// NewScriptAddress creates a script address (P2SH)
func NewScriptAddress(script []byte, netID byte) *Address {
	// Hash160 the script
	hash160 := Hash160(script)

	// Use script hash address ID
	scriptNetID := netID
	if netID == MainNetPubKeyHashAddrID {
		scriptNetID = MainNetScriptHashAddrID
	} else if netID == TestNetPubKeyHashAddrID {
		scriptNetID = TestNetScriptHashAddrID
	}

	return &Address{
		hash:    hash160,
		netID:   scriptNetID,
		version: 0,
	}
}

// String returns the Base58Check encoded address string
func (addr *Address) String() string {
	// Create payload: version + netID + hash
	payload := make([]byte, 1+len(addr.hash))
	payload[0] = addr.netID
	copy(payload[1:], addr.hash)

	return Base58CheckEncode(payload)
}

// Hash160 returns the hash160 of the address
func (addr *Address) Hash160() []byte {
	result := make([]byte, len(addr.hash))
	copy(result, addr.hash)
	return result
}

// NetID returns the network ID
func (addr *Address) NetID() byte {
	return addr.netID
}

// IsScript returns true if this is a script address (P2SH)
func (addr *Address) IsScript() bool {
	return addr.netID == MainNetScriptHashAddrID ||
		   addr.netID == TestNetScriptHashAddrID
}

// CreateScriptPubKey creates a script for this address
func (addr *Address) CreateScriptPubKey() []byte {
	if addr.IsScript() {
		// P2SH: OP_HASH160 <hash> OP_EQUAL
		script := make([]byte, 23)
		script[0] = 0xa9  // OP_HASH160
		script[1] = 0x14  // Push 20 bytes
		copy(script[2:22], addr.hash)
		script[22] = 0x87 // OP_EQUAL
		return script
	} else {
		// P2PKH: OP_DUP OP_HASH160 <hash> OP_EQUALVERIFY OP_CHECKSIG
		script := make([]byte, 25)
		script[0] = 0x76  // OP_DUP
		script[1] = 0xa9  // OP_HASH160
		script[2] = 0x14  // Push 20 bytes
		copy(script[3:23], addr.hash)
		script[23] = 0x88 // OP_EQUALVERIFY
		script[24] = 0xac // OP_CHECKSIG
		return script
	}
}

// DecodeAddress decodes a Base58Check encoded address string
func DecodeAddress(addressStr string) (*Address, error) {
	decoded, err := Base58CheckDecode(addressStr)
	if err != nil {
		return nil, fmt.Errorf("invalid address format: %v", err)
	}

	if len(decoded) != 21 {
		return nil, errors.New("decoded address must be 21 bytes")
	}

	netID := decoded[0]
	hash160 := decoded[1:]

	return &Address{
		hash:    hash160,
		netID:   netID,
		version: 0,
	}, nil
}

// ValidateAddress validates an address string
func ValidateAddress(addressStr string) error {
	_, err := DecodeAddress(addressStr)
	return err
}

// Base58 encoding functions

// Base58Encode encodes bytes to Base58 string
func Base58Encode(input []byte) string {
	if len(input) == 0 {
		return ""
	}

	// Count leading zeros
	leadingZeros := 0
	for _, b := range input {
		if b == 0 {
			leadingZeros++
		} else {
			break
		}
	}

	// Convert to big integer
	num := new(big.Int).SetBytes(input)

	// Convert to base58
	result := make([]byte, 0, len(input)*2)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)

	for num.Cmp(zero) > 0 {
		num.DivMod(num, base, mod)
		result = append(result, base58Alphabet[mod.Int64()])
	}

	// Add leading zeros as '1'
	for i := 0; i < leadingZeros; i++ {
		result = append(result, '1')
	}

	// Reverse the result
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return string(result)
}

// Base58Decode decodes a Base58 string to bytes
func Base58Decode(input string) ([]byte, error) {
	if len(input) == 0 {
		return nil, nil
	}

	// Count leading '1's (zeros)
	leadingOnes := 0
	for _, c := range input {
		if c == '1' {
			leadingOnes++
		} else {
			break
		}
	}

	// Convert from base58
	num := big.NewInt(0)
	base := big.NewInt(58)

	for _, c := range input {
		val, ok := base58Map[c]
		if !ok {
			return nil, fmt.Errorf("invalid base58 character: %c", c)
		}

		num.Mul(num, base)
		num.Add(num, big.NewInt(int64(val)))
	}

	// Convert to bytes
	decoded := num.Bytes()

	// Add leading zeros
	result := make([]byte, leadingOnes+len(decoded))
	copy(result[leadingOnes:], decoded)

	return result, nil
}

// Base58CheckEncode encodes bytes with checksum
func Base58CheckEncode(input []byte) string {
	// Calculate checksum
	checksum := ChecksumHash(input)

	// Append checksum
	payload := make([]byte, len(input)+4)
	copy(payload, input)
	copy(payload[len(input):], checksum)

	return Base58Encode(payload)
}

// Base58CheckDecode decodes a Base58Check string
func Base58CheckDecode(input string) ([]byte, error) {
	decoded, err := Base58Decode(input)
	if err != nil {
		return nil, err
	}

	if len(decoded) < 4 {
		return nil, errors.New("decoded data too short for checksum")
	}

	// Split payload and checksum
	payload := decoded[:len(decoded)-4]
	checksum := decoded[len(decoded)-4:]

	// Verify checksum
	expectedChecksum := ChecksumHash(payload)
	if !bytes.Equal(checksum, expectedChecksum) {
		return nil, errors.New("checksum verification failed")
	}

	return payload, nil
}

// WIF (Wallet Import Format) functions

// EncodePrivateKeyWIF encodes a private key in WIF format
func EncodePrivateKeyWIF(privateKey *PrivateKey, compressed bool, netID byte) string {
	keyBytes := privateKey.Bytes()

	// Pad to 32 bytes
	if len(keyBytes) < 32 {
		padded := make([]byte, 32)
		copy(padded[32-len(keyBytes):], keyBytes)
		keyBytes = padded
	}

	// Create payload
	var payload []byte
	if compressed {
		payload = make([]byte, 34)
		payload[0] = PrivateKeyID
		copy(payload[1:33], keyBytes)
		payload[33] = 0x01 // Compression flag
	} else {
		payload = make([]byte, 33)
		payload[0] = PrivateKeyID
		copy(payload[1:], keyBytes)
	}

	return Base58CheckEncode(payload)
}

// DecodePrivateKeyWIF decodes a WIF private key
func DecodePrivateKeyWIF(wif string) (*PrivateKey, bool, error) {
	decoded, err := Base58CheckDecode(wif)
	if err != nil {
		return nil, false, fmt.Errorf("invalid WIF format: %v", err)
	}

	if len(decoded) != 33 && len(decoded) != 34 {
		return nil, false, errors.New("invalid WIF length")
	}

	if decoded[0] != PrivateKeyID {
		return nil, false, errors.New("invalid WIF prefix")
	}

	compressed := false
	keyBytes := decoded[1:33]

	if len(decoded) == 34 {
		if decoded[33] != 0x01 {
			return nil, false, errors.New("invalid compression flag")
		}
		compressed = true
	}

	privateKey, err := ParsePrivateKeyFromBytes(keyBytes)
	if err != nil {
		return nil, false, fmt.Errorf("invalid private key: %v", err)
	}

	return privateKey, compressed, nil
}

// Address validation helpers

// IsValidMainNetAddress checks if an address is valid for mainnet
func IsValidMainNetAddress(addr string) bool {
	address, err := DecodeAddress(addr)
	if err != nil {
		return false
	}

	return address.netID == MainNetPubKeyHashAddrID ||
		   address.netID == MainNetScriptHashAddrID
}

// IsValidTestNetAddress checks if an address is valid for testnet
func IsValidTestNetAddress(addr string) bool {
	address, err := DecodeAddress(addr)
	if err != nil {
		return false
	}

	return address.netID == TestNetPubKeyHashAddrID ||
		   address.netID == TestNetScriptHashAddrID
}

// GetAddressType returns the type of address
func GetAddressType(addr string) (string, error) {
	address, err := DecodeAddress(addr)
	if err != nil {
		return "", err
	}

	switch address.netID {
	case MainNetPubKeyHashAddrID: // Also covers TWINSMainNetPubKeyHashAddrID (alias)
		return "P2PKH-MainNet", nil
	case MainNetScriptHashAddrID:
		return "P2SH-MainNet", nil
	case TestNetPubKeyHashAddrID: // Also covers TWINSTestNetPubKeyHashAddrID (alias)
		return "P2PKH-TestNet", nil
	case TestNetScriptHashAddrID:
		return "P2SH-TestNet", nil
	default:
		return "Unknown", nil
	}
}

// Base64Encode encodes bytes to base64 string (standard encoding)
func Base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Base64Decode decodes a base64 string to bytes
func Base64Decode(encoded string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(encoded)
}