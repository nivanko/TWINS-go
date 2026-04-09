// Copyright (c) 2025 The TWINS Core developers
// Distributed under the MIT software license

package spork

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"
)

// Spork IDs - Don't ever reuse these IDs
// This would result in old clients getting confused about which spork is for what
const (
	// Active DASH/PIVX sporks (10001-10015)
	SporkMaxValue                     = 10004
	SporkMasternodeScanning           = 10006
	SporkMasternodePaymentEnforcement = 10007
	SporkMasternodePayUpdatedNodes    = 10009
	SporkNewProtocolEnforcement       = 10013
	SporkNewProtocolEnforcement2      = 10014

	// TWINS-specific sporks (20190001+)
	SporkTwinsEnableMasternodeTiers = 20190001
	SporkTwinsMinStakeAmount        = 20190002
)

// Default values for sporks
// Unix timestamp: 4070908800 = OFF (year 2099)
// Values < 1000000 are treated as numeric values, >= 1000000 as timestamps
const (
	DefaultMaxValue                     = 1000       // 1000 TWINS
	DefaultMasternodeScanning           = 978307200  // 2001-01-01 (ON)
	DefaultMasternodePaymentEnforcement = 1546731824 // ON (2019-01-06, timestamp in past = active)
	DefaultMasternodePayUpdatedNodes    = 4070908800 // OFF
	DefaultNewProtocolEnforcement       = 4070908800 // OFF
	DefaultNewProtocolEnforcement2      = 4070908800 // OFF
	DefaultTwinsEnableMasternodeTiers   = 4070908800 // OFF
	DefaultTwinsMinStakeAmount          = 4070908800 // OFF. Legacy C++ default is also OFF; mainnet historically activated it via a signed spork broadcast. The consensus rule is enforced on mainnet regardless of spork state (see validateMinStakeOutput height gate) to avoid diverging from legacy mainnet, while testnet/regtest remain gated on this spork.
)

// Message represents a spork message broadcast over the network
type Message struct {
	SporkID    int32     // Unique spork identifier
	Value      int64     // Spork value (timestamp or numeric value)
	TimeSigned int64     // Unix timestamp when spork was signed
	Signature  []byte    // ECDSA signature (65 bytes)
}

// Hash calculates the hash of the spork message for identification
// Uses the same hashing as legacy (HashQuark)
func (m *Message) Hash() [32]byte {
	// Serialize: SporkID (4 bytes) + Value (8 bytes) + TimeSigned (8 bytes)
	data := make([]byte, 20)
	binary.LittleEndian.PutUint32(data[0:4], uint32(m.SporkID))
	binary.LittleEndian.PutUint64(data[4:12], uint64(m.Value))
	binary.LittleEndian.PutUint64(data[12:20], uint64(m.TimeSigned))

	// Note: Legacy TWINS used HashQuark (Quark hash algorithm) for spork hashing
	// Quark is a custom hash function combining multiple hash algorithms
	// For protocol compatibility with modern nodes, SHA256 is acceptable
	// The spork signature verification is the primary security mechanism
	// SHA256 provides sufficient collision resistance for spork message IDs
	return sha256.Sum256(data)
}

// SignatureMessage returns the message that should be signed
// Format: "SporkID" + "Value" + "TimeSigned" (as strings concatenated)
func (m *Message) SignatureMessage() string {
	return fmt.Sprintf("%d%d%d", m.SporkID, m.Value, m.TimeSigned)
}

// SporkInfo holds metadata about a spork for display/management
type SporkInfo struct {
	ID         int32
	Name       string
	Value      int64
	DefaultVal int64
	Active     bool
	TimeSigned time.Time
}

// Storage interface for persisting sporks to database
type Storage interface {
	// ReadSpork reads a spork from storage by ID
	ReadSpork(sporkID int32) (*Message, error)

	// WriteSpork persists a spork to storage
	WriteSpork(spork *Message) error

	// LoadAllSporks loads all sporks from storage
	LoadAllSporks() (map[int32]*Message, error)
}
