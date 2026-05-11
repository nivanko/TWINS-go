package p2p

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/internal/masternode"
	"github.com/twins-dev/twins-core/internal/masternode/debug"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/script"
	"github.com/twins-dev/twins-core/pkg/types"
)

// Advanced message handlers for TWINS-specific features
// NOTE: Budget/Governance, SwiftTX, and PrivateSend handlers have been removed (permanently disabled)

// Spork represents a network activation switch
type Spork struct {
	ID        uint32
	Value     int64 // Unix timestamp - feature active if current time > value
	Timestamp int64
	Signature []byte
}

// SporkManager manages network sporks
type SporkManager struct {
	mu        sync.RWMutex
	sporks    map[uint32]*Spork
	pubKey    *crypto.PublicKey // Current spork signing public key
	pubKeyOld *crypto.PublicKey // Old spork key for backward compatibility during transitions
}

// NewSporkManager creates a new spork manager
func NewSporkManager(sporkPubKey *crypto.PublicKey) *SporkManager {
	return &SporkManager{
		sporks: make(map[uint32]*Spork),
		pubKey: sporkPubKey,
	}
}

// SetOldPublicKey sets the old public key for backward compatibility
func (sm *SporkManager) SetOldPublicKey(oldKey *crypto.PublicKey) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.pubKeyOld = oldKey
}

// MasternodeWinner represents a masternode selected for payment
type MasternodeWinner struct {
	BlockHeight uint32
	Outpoint    types.Outpoint
	PayeeScript []byte // Payment script (P2PKH)
	Signature   []byte
	// Legacy fields (may be unused in new format)
	Tier  uint8  // Deprecated: tier is not in legacy mnw format
	Score uint64 // Deprecated: score is not in legacy mnw format
}

// MasternodeQuorum represents a quorum of masternodes
type MasternodeQuorum struct {
	Type        uint8 // QuorumType: payment, governance, etc.
	BlockHeight uint32
	Members     []types.Outpoint
	Signatures  [][]byte
}

// QuorumType defines types of masternode quorums
type QuorumType uint8

const (
	QuorumTypePayment QuorumType = iota
	QuorumTypeGovernance
	QuorumTypeOther
)

// NOTE: SwiftTX, Budget/Governance, and PrivateSend message handlers removed (permanently disabled)
// These features have been completely removed from the Go implementation

// handleSpork processes a spork message
// Sporks are network-wide activation switches for features
func (s *Server) handleSpork(peer *Peer, msg *Message) {
	if len(msg.Payload) < 20 {
		s.logger.WithField("peer", peer.GetAddress().String()).
			Warn("Invalid spork message size")
		return
	}

	// Deserialize spork data
	// Format: spork_id (4 bytes) + value (8 bytes) + timestamp (8 bytes) + signature (variable)
	sporkID := binary.LittleEndian.Uint32(msg.Payload[0:4])
	sporkValue := binary.LittleEndian.Uint64(msg.Payload[4:12])
	timestamp := binary.LittleEndian.Uint64(msg.Payload[12:20])

	s.logger.WithFields(map[string]interface{}{
		"peer":      peer.GetAddress().String(),
		"spork_id":  sporkID,
		"value":     sporkValue,
		"timestamp": timestamp,
	}).Debug("Received spork message from peer")

	// Known TWINS sporks:
	// SPORK_2_SWIFTTX = 10001 (DEPRECATED - always OFF)
	// SPORK_3_SWIFTTX_BLOCK_FILTERING = 10002 (DEPRECATED)
	// SPORK_5_MAX_VALUE = 10004
	// SPORK_7_MASTERNODE_SCANNING = 10006
	// SPORK_8_MASTERNODE_PAYMENT_ENFORCEMENT = 10007
	// SPORK_9_MASTERNODE_BUDGET_ENFORCEMENT = 10008 (OFF - budget disabled)
	// SPORK_10_MASTERNODE_PAY_UPDATED_NODES = 10009
	// SPORK_13_ENABLE_SUPERBLOCKS = 10012 (OFF - budget disabled)
	// SPORK_14_NEW_PROTOCOL_ENFORCEMENT = 10013

	// Extract signature (remaining bytes after timestamp)
	// Legacy format may include varint length prefix before signature
	signatureData := msg.Payload[20:]

	// Check if first byte is a varint length indicator
	var signature []byte
	if len(signatureData) > 0 && signatureData[0] <= 253 {
		// Single byte length prefix
		sigLen := int(signatureData[0])
		if len(signatureData) >= sigLen+1 {
			signature = signatureData[1 : sigLen+1]
			s.logger.WithFields(logrus.Fields{
				"sig_len":      sigLen,
				"prefix_bytes": 1,
				"total_bytes":  len(signatureData),
			}).Debug("Parsed signature with varint prefix")
		} else {
			signature = signatureData
		}
	} else {
		signature = signatureData
	}

	// Validate spork signature
	if err := s.validateSporkSignature(sporkID, int64(sporkValue), int64(timestamp), signature); err != nil {
		s.logger.WithError(err).WithFields(logrus.Fields{
			"peer":     peer.GetAddress().String(),
			"spork_id": sporkID,
			"value":    sporkValue,
			"sig_len":  len(signature),
			"sig_hex":  hex.EncodeToString(signature[:min(8, len(signature))]),
		}).Warn("Spork signature validation FAILED")
		return
	}

	// Store spork
	spork := &Spork{
		ID:        sporkID,
		Value:     int64(sporkValue),
		Timestamp: int64(timestamp),
		Signature: signature,
	}

	// Check if this is a new or updated spork
	isNew := s.sporkMgr.AddSpork(spork)

	// Always forward to global manager if validation passed (global manager handles its own dedup)
	if s.externalSporkHandler != nil {
		s.externalSporkHandler(int32(sporkID), int64(sporkValue), int64(timestamp), signature)
	}

	if isNew {
		s.logger.WithFields(map[string]interface{}{
			"spork_id": sporkID,
			"active":   s.isSporkActive(sporkID),
		}).Debug("Spork activated/updated")

		// Broadcast to other peers
		s.broadcastSpork(spork, peer)
	}
}

// handleGetSporks processes a request for all active sporks
func (s *Server) handleGetSporks(peer *Peer, msg *Message) {
	s.logger.WithField("peer", peer.GetAddress().String()).Debug("Received getsporks request")

	// Get all sporks from the spork manager
	sporks := s.sporkMgr.GetAllSporks()

	s.logger.WithFields(map[string]interface{}{
		"peer":  peer.GetAddress().String(),
		"count": len(sporks),
	}).Debug("Sending sporks to peer")

	// Send each spork as a separate MsgSpork message
	for _, spork := range sporks {
		// Serialize spork: spork_id (4) + value (8) + timestamp (8) + varint_sig_len + signature
		// C++ serialization format: nSporkID + nValue + nTimeSigned + vchSig (vector with length prefix)
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, spork.ID)
		binary.Write(&buf, binary.LittleEndian, uint64(spork.Value))
		binary.Write(&buf, binary.LittleEndian, uint64(spork.Timestamp))
		// Add varint length prefix for signature (matches C++ vector<unsigned char> serialization)
		writeCompactSize(&buf, uint64(len(spork.Signature)))
		buf.Write(spork.Signature)
		payload := buf.Bytes()

		// Use NewMessage to properly calculate checksum
		sporkMsg := NewMessage(MsgSpork, payload, s.getMagicBytes())

		if err := peer.SendMessage(sporkMsg); err != nil {
			s.logger.WithError(err).WithField("spork_id", spork.ID).
				Warn("Failed to send spork to peer")
		}
	}

	s.logger.WithField("peer", peer.GetAddress().String()).Debug("Sporks sent successfully")
}

// handleMasternodeWinner processes a masternode payment winner message
// Legacy format: vinMasternode (CTxIn) + nBlockHeight + payee (CScript) + vchSig
func (s *Server) handleMasternodeWinner(peer *Peer, msg *Message) {
	peerAddr := peer.GetAddress().String()
	s.logger.WithField("peer", peerAddr).
		Debug("Received masternode winner message")

	buf := bytes.NewReader(msg.Payload)

	// Parse vinMasternode (CTxIn: prevout + scriptSig + sequence)
	outpoint, _, _, err := DeserializeCTxIn(buf)
	if err != nil {
		s.logger.WithError(err).Debug("Failed to parse CTxIn from masternode winner")
		return
	}

	// Parse nBlockHeight (4 bytes)
	var blockHeight uint32
	if err := binary.Read(buf, binary.LittleEndian, &blockHeight); err != nil {
		s.logger.WithError(err).Debug("Failed to parse block height from masternode winner")
		return
	}

	// Parse payee (CScript - varbytes)
	payeeScript, err := readVarBytes(buf)
	if err != nil {
		s.logger.WithError(err).Debug("Failed to parse payee script from masternode winner")
		return
	}

	// Parse vchSig (signature - varbytes)
	signature, err := readVarBytes(buf)
	if err != nil {
		s.logger.WithError(err).Debug("Failed to parse signature from masternode winner")
		return
	}

	s.logger.WithFields(map[string]interface{}{
		"peer":         peer.GetAddress().String(),
		"block_height": blockHeight,
		"outpoint":     outpoint.String(),
		"payee_len":    len(payeeScript),
		"sig_len":      len(signature),
	}).Debug("Received masternode winner")

	// Validation:
	// 1. Verify block height is current or near future
	// Legacy: int nFirstBlock = nHeight - (mnodeman.CountEnabled() * 1.25);
	// Legacy: if (winner.nBlockHeight < nFirstBlock || winner.nBlockHeight > nHeight + 20) return;
	if s.blockchain != nil {
		if bestHeight, err := s.blockchain.GetBestHeight(); err == nil {
			// Calculate first valid block based on enabled masternode count
			// Legacy formula: nFirstBlock = nHeight - (mnodeman.CountEnabled() * 1.25)
			var firstBlock uint32
			if s.mnManager != nil {
				enabledCount := s.mnManager.CountEnabled(-1) // -1 uses minimum payment protocol version
				// Calculate range: enabledCount * 1.25
				rangeBlocks := uint32(float64(enabledCount) * 1.25)
				if rangeBlocks < bestHeight {
					firstBlock = bestHeight - rangeBlocks
				}
			} else {
				// Fallback if no masternode manager
				if bestHeight > 10 {
					firstBlock = bestHeight - 10
				}
			}
			// Legacy: winner.nBlockHeight > nHeight + 20
			if blockHeight < firstBlock || blockHeight > bestHeight+20 {
				s.logger.WithFields(map[string]interface{}{
					"block_height": blockHeight,
					"best_height":  bestHeight,
					"first_block":  firstBlock,
				}).Debug("Masternode winner block height out of range")
				return
			}
		}
	}

	// 2. Verify masternode exists and is active
	if s.mnManager != nil {
		if !s.mnManager.IsActive(outpoint, blockHeight) {
			s.logger.Debug("Masternode not active at height")
			return
		}
	}

	// 3. LEGACY COMPATIBILITY: Verify masternode is in top 10 to vote
	// Legacy: masternode-payments.cpp:736-747
	// int n = mnodeman.GetMasternodeRank(vinMasternode, nBlockHeight - 100, ActiveProtocol());
	// if (n > MNPAYMENTS_SIGNATURES_TOTAL) return false;
	if s.mnManager != nil && blockHeight >= 100 {
		minProto := s.mnManager.GetMinMasternodePaymentsProto()
		rank := s.mnManager.GetMasternodeRank(outpoint, blockHeight-100, minProto, true)
		if rank == -1 {
			s.logger.WithFields(map[string]interface{}{
				"outpoint":     outpoint.String(),
				"block_height": blockHeight,
			}).Debug("Unknown masternode trying to vote")
			return
		}
		// MNPAYMENTS_SIGNATURES_TOTAL = 10
		const MNPaymentsSignaturesTotal = 10
		if rank > MNPaymentsSignaturesTotal {
			// Legacy: Only penalize if rank > MNPAYMENTS_SIGNATURES_TOTAL * 2
			if rank > MNPaymentsSignaturesTotal*2 {
				s.logger.WithFields(map[string]interface{}{
					"outpoint":     outpoint.String(),
					"block_height": blockHeight,
					"rank":         rank,
					"max_rank":     MNPaymentsSignaturesTotal * 2,
				}).Warn("Masternode not in top 20, rejecting vote")
			}
			s.logger.WithFields(map[string]interface{}{
				"outpoint":     outpoint.String(),
				"rank":         rank,
				"block_height": blockHeight,
			}).Debug("Masternode not in top 10, cannot vote")
			return
		}
	}

	// 4. Verify signature using masternode's public key
	if s.mnManager != nil {
		pubKeyBytes, err := s.mnManager.GetPublicKey(outpoint)
		if err != nil {
			s.logger.WithError(err).Debug("Failed to get masternode public key")
			return
		}

		// Parse public key
		pubKey, err := crypto.ParsePublicKeyFromBytes(pubKeyBytes)
		if err != nil {
			s.logger.WithError(err).Debug("Failed to parse masternode public key")
			return
		}

		// Create message to verify (matches legacy: vinMasternode.prevout.ToStringShort() + nBlockHeight + payee.ToString())
		// Legacy format: hash.ToString() (big-endian) + "-" + index + blockHeight + payee.ToString()
		// CRITICAL: Use Hash.String() which outputs big-endian (reversed) to match legacy
		// CRITICAL: payee.ToString() returns CScript::ToString() ASM format, NOT raw hex!
		payeeASM, err := script.Disassemble(payeeScript)
		if err != nil {
			// Fallback to hex on error (should not happen for valid scripts)
			payeeASM = fmt.Sprintf("%x", payeeScript)
		}
		message := fmt.Sprintf("%s-%d%d%s",
			outpoint.Hash.String(), // Big-endian (reversed) to match legacy C++ ToString()
			outpoint.Index,
			blockHeight,
			payeeASM)

		// Verify signature using compact signature (matches legacy obfuScationSigner.VerifyMessage)
		valid, err := crypto.VerifyCompactSignature(pubKey, message, signature)
		if err != nil || !valid {
			s.logger.WithFields(map[string]interface{}{
				"outpoint":     outpoint.String(),
				"block_height": blockHeight,
				"error":        err,
			}).Warn("Invalid masternode winner signature")
			// Legacy: if (masternodeSync.IsSynced()) Misbehaving(pfrom->GetId(), 20);
			// Only penalize if we're synced (could be non-synced masternode otherwise)
			if s.syncer != nil && s.syncer.IsSynced() {
				s.Misbehaving(peer, 20, "invalid masternode winner signature")
			}
			return
		}

		s.logger.Debug("Masternode winner signature verified")
	}

	// Store winner for block validation
	winner := &MasternodeWinner{
		BlockHeight: blockHeight,
		Outpoint:    outpoint,
		PayeeScript: payeeScript,
		Signature:   signature,
	}

	s.mnWinners.Store(blockHeight, winner)

	// Store in masternode manager for persistence (mnpayments.dat)
	// Legacy: mapMasternodePayeeVotes[winner.GetHash()] = winner
	if s.mnManager != nil {
		s.mnManager.StoreWinnerVote(outpoint, blockHeight, payeeScript, signature)

		// Notify sync manager about the received winner vote
		// Legacy: masternodeSync.AddedMasternodeWinner(winner.GetHash())
		// Hash from raw message payload to match legacy GetHash() behavior
		winnerHash := types.NewHash(msg.Payload)
		s.mnManager.AddedMasternodeWinner(winnerHash)
	}

	// Forward vote to payment validator for quorum tracking
	if s.paymentValidator != nil {
		if err := s.paymentValidator.AddPaymentVote(blockHeight, outpoint, payeeScript, signature); err != nil {
			s.logger.WithError(err).Debug("Failed to add payment vote to validator")
			// Don't return error - we still want to store the winner locally
		} else {
			s.logger.WithFields(map[string]interface{}{
				"block_height": blockHeight,
				"outpoint":     outpoint.String(),
			}).Debug("Added payment vote to consensus validator")
		}
	}

	// Debug: emit winner vote accepted
	if dc := s.debugCollector.Load(); dc != nil && dc.IsEnabled() {
		dc.EmitWinner("winner_vote_accepted", peerAddr, fmt.Sprintf("Winner vote accepted from %s: outpoint=%s height=%d", peerAddr, outpoint.String(), blockHeight), map[string]any{
			"outpoint":     outpoint.String(),
			"block_height": blockHeight,
			"payee_len":    len(payeeScript),
		})
	}

	s.logger.WithField("block_height", blockHeight).
		Debug("Stored masternode winner and forwarded vote")
}

// handleMasternodeScanningError processes a masternode scanning error message
func (s *Server) handleMasternodeScanningError(peer *Peer, msg *Message) {
	s.logger.WithField("peer", peer.GetAddress().String()).
		Debug("Received masternode scanning error message")

	// This message reports errors during masternode network scanning
	// Used for masternode health monitoring and network diagnostics
	// Format:
	// - Error type (1 byte)
	// - Masternode outpoint (36 bytes)
	// - Error details (variable)

	// Parse and log the error for diagnostics
	// Not critical for consensus, used for network health monitoring
	if len(msg.Payload) < 37 {
		s.logger.WithField("peer", peer.GetAddress().String()).
			Warn("Received malformed masternode scanning error message")
		return
	}

	errorType := msg.Payload[0]
	// Outpoint is bytes 1-36 (32 byte hash + 4 byte index)
	// Error details start at byte 37
	var errorDetails string
	if len(msg.Payload) > 37 {
		errorDetails = string(msg.Payload[37:])
	}

	s.logger.WithFields(map[string]interface{}{
		"peer":       peer.GetAddress().String(),
		"error_type": errorType,
		"details":    errorDetails,
	}).Debug("Masternode scanning error reported")
}

// handleMasternodeQuorum processes a masternode quorum message
func (s *Server) handleMasternodeQuorum(peer *Peer, msg *Message) {
	s.logger.WithField("peer", peer.GetAddress().String()).
		Debug("Received masternode quorum message")

	// Masternode quorum for governance/payment decisions
	// Format:
	// - Quorum type (1 byte): payment, governance, other
	// - Block height (4 bytes)
	// - Member count (4 bytes)
	// - Quorum members (variable: list of masternode outpoints, 36 bytes each)
	// - Signatures (variable)

	if len(msg.Payload) < 9 { // 1 + 4 + 4 = 9 bytes minimum
		s.logger.WithField("peer", peer.GetAddress().String()).
			Warn("Invalid masternode quorum message size")
		return
	}

	// Parse quorum type and height
	quorumType := msg.Payload[0]
	blockHeight := binary.LittleEndian.Uint32(msg.Payload[1:5])
	memberCount := binary.LittleEndian.Uint32(msg.Payload[5:9])

	// Validate member count
	if memberCount > 1000 { // Reasonable limit
		s.logger.WithField("member_count", memberCount).
			Warn("Masternode quorum member count too large")
		return
	}

	// Parse member outpoints
	members := make([]types.Outpoint, 0, memberCount)
	offset := 9
	for i := uint32(0); i < memberCount; i++ {
		if offset+36 > len(msg.Payload) {
			s.logger.Warn("Masternode quorum message truncated")
			return
		}

		var txHash types.Hash
		copy(txHash[:], msg.Payload[offset:offset+32])
		outpointIndex := binary.LittleEndian.Uint32(msg.Payload[offset+32 : offset+36])

		members = append(members, types.Outpoint{
			Hash:  txHash,
			Index: outpointIndex,
		})
		offset += 36
	}

	// Remaining bytes are signatures
	signatures := [][]byte{}
	if offset < len(msg.Payload) {
		// Parse signature array (simplified - would need proper deserialization)
		signatures = append(signatures, msg.Payload[offset:])
	}

	s.logger.WithFields(map[string]interface{}{
		"peer":         peer.GetAddress().String(),
		"quorum_type":  quorumType,
		"block_height": blockHeight,
		"member_count": memberCount,
	}).Debug("Received masternode quorum")

	// Store quorum
	quorum := &MasternodeQuorum{
		Type:        quorumType,
		BlockHeight: blockHeight,
		Members:     members,
		Signatures:  signatures,
	}

	s.mnQuorums.Store(blockHeight, quorum)
	s.logger.WithFields(map[string]interface{}{
		"block_height": blockHeight,
		"members":      len(members),
	}).Debug("Stored masternode quorum")

	// Used for:
	// - Payment consensus (45% of block reward distribution)
	// - Tier-based quorum decisions
	// - Network governance (if enabled)
}

// Note: Advanced message handlers are registered in the message switch statement in server.go
// No separate registration function needed - handlers are called directly based on message type

// validateSporkSignature validates a spork signature using legacy compact signature format
// Legacy format: 65-byte compact signature with Bitcoin message magic prefix
// Message: string concatenation of "SporkID" + "Value" + "TimeSigned"
func (s *Server) validateSporkSignature(sporkID uint32, value int64, timestamp int64, signature []byte) error {
	// Check timestamp is not from the future (allow 1 hour clock skew)
	// Old sporks can remain active indefinitely, so no lower bound check
	now := time.Now().Unix()
	if timestamp > now+3600 {
		return fmt.Errorf("spork timestamp is from the future: %d (current: %d)", timestamp, now)
	}

	s.logger.WithFields(logrus.Fields{
		"spork_id":   sporkID,
		"timestamp":  timestamp,
		"spork_date": time.Unix(timestamp, 0).Format(time.RFC3339),
		"age_days":   (now - timestamp) / 86400,
	}).Debug("Validating spork timestamp")

	// If no public key configured, skip signature verification
	if s.sporkMgr.pubKey == nil {
		s.logger.Debug("Spork public key not configured, skipping signature validation")
		return nil
	}

	// Create message string in legacy format (matching legacy/src/spork.cpp:193)
	// Format: std::to_string(sporkID) + std::to_string(value) + std::to_string(timestamp)
	message := fmt.Sprintf("%d%d%d", sporkID, value, timestamp)

	// Try compact signature format first (65 bytes) - this is the legacy format
	if len(signature) == 65 {
		// Verify with current key using compact signature
		valid, err := crypto.VerifyCompactSignature(s.sporkMgr.pubKey, message, signature)
		if err == nil && valid {
			return nil // Verified with current key
		}

		// Try old key for backward compatibility during transitions
		s.sporkMgr.mu.RLock()
		oldKey := s.sporkMgr.pubKeyOld
		s.sporkMgr.mu.RUnlock()

		if oldKey != nil {
			valid, err := crypto.VerifyCompactSignature(oldKey, message, signature)
			if err == nil && valid {
				s.logger.Debug("Spork verified with old public key (transition period)")
				return nil
			}
		}

		return fmt.Errorf("compact signature verification failed with both keys")
	}

	// Fallback: Try raw ECDSA signature format (64 bytes) for testing/development
	if len(signature) == 64 {
		s.logger.Debug("Using non-standard 64-byte signature format (development mode)")

		// Create message hash from spork data (binary format)
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, sporkID)
		binary.Write(&buf, binary.LittleEndian, value)
		binary.Write(&buf, binary.LittleEndian, timestamp)
		hash := sha256.Sum256(buf.Bytes())

		// Parse signature
		sig, err := crypto.ParseSignatureFromBytes(signature)
		if err != nil {
			return fmt.Errorf("invalid signature format: %w", err)
		}

		// Verify with current key
		if s.sporkMgr.pubKey.Verify(hash[:], sig) {
			return nil
		}

		// Try old key
		s.sporkMgr.mu.RLock()
		oldKey := s.sporkMgr.pubKeyOld
		s.sporkMgr.mu.RUnlock()

		if oldKey != nil && oldKey.Verify(hash[:], sig) {
			s.logger.Debug("Spork verified with old key (64-byte format)")
			return nil
		}

		return fmt.Errorf("64-byte signature verification failed")
	}

	return fmt.Errorf("invalid signature length: %d (expected 65 for compact or 64 for raw ECDSA)", len(signature))
}

// isSporkActive checks if a spork is currently active
func (s *Server) isSporkActive(sporkID uint32) bool {
	// NOTE: Deprecated features (SwiftTX, Budget, Zerocoin, PrivateSend) have been completely
	// removed from the Go implementation. Their SPORKs are permanently disabled.

	// Check stored spork value
	// Active if spork value (Unix timestamp) is in the past
	spork := s.sporkMgr.GetSpork(sporkID)
	if spork == nil {
		return false // No spork value stored = inactive
	}

	// Spork is active if its activation time has passed
	now := time.Now().Unix()
	return now >= spork.Value
}

// broadcastSpork broadcasts a spork to all connected peers except the source
func (s *Server) broadcastSpork(spork *Spork, exceptPeer *Peer) {
	// Serialize spork with varint for signature length (matches C++ format)
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, spork.ID)
	binary.Write(&buf, binary.LittleEndian, uint64(spork.Value))
	binary.Write(&buf, binary.LittleEndian, uint64(spork.Timestamp))
	writeCompactSize(&buf, uint64(len(spork.Signature)))
	buf.Write(spork.Signature)

	// Use NewMessage to properly calculate checksum
	msg := NewMessage(MsgSpork, buf.Bytes(), s.params.NetMagicBytes)

	// Broadcast to all peers except source
	s.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if exceptPeer == nil || peer.GetAddress().String() != exceptPeer.GetAddress().String() {
			if err := peer.SendMessage(msg); err != nil {
				s.logger.WithError(err).WithField("peer", peer.GetAddress().String()).
					Debug("Failed to broadcast spork")
			}
		}
		return true
	})
}

// SporkManager methods

// AddSpork adds or updates a spork, returns true if new/updated
func (sm *SporkManager) AddSpork(spork *Spork) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	existing, exists := sm.sporks[spork.ID]
	if exists && existing.Timestamp >= spork.Timestamp {
		return false // Older or same version
	}

	sm.sporks[spork.ID] = spork
	return true
}

// GetSpork retrieves a spork by ID
func (sm *SporkManager) GetSpork(sporkID uint32) *Spork {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sporks[sporkID]
}

// GetAllSporks returns all stored sporks
func (sm *SporkManager) GetAllSporks() map[uint32]*Spork {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make(map[uint32]*Spork, len(sm.sporks))
	for id, spork := range sm.sporks {
		result[id] = spork
	}
	return result
}

// verifyMasternodeWinnerSignature verifies the signature on a masternode winner message
// Legacy C++ reference: CMasternodePaymentWinner::SignatureValid()
func (s *Server) verifyMasternodeWinnerSignature(blockHeight uint32, outpoint types.Outpoint, payee []byte, signature []byte) error {
	// Get masternode public key from masternode manager
	if s.mnManager == nil {
		s.logger.Debug("Masternode manager not available, skipping signature verification")
		return nil // Accept without verification if manager not available
	}

	// Legacy uses compact signatures (65 bytes)
	if len(signature) != 65 {
		return fmt.Errorf("masternode winner signature must be 65 bytes (compact), got %d", len(signature))
	}

	// Get masternode public key
	pubKeyBytes, err := s.mnManager.GetPublicKey(outpoint)
	if err != nil {
		return fmt.Errorf("failed to get masternode public key: %w", err)
	}

	// Parse public key
	pubKey, err := crypto.ParsePublicKeyFromBytes(pubKeyBytes)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}

	// Create message matching C++ format:
	// vinMasternode.prevout.ToStringShort() + std::to_string(nBlockHeight) + payee.ToString()
	var message string

	// Add outpoint short string (txhash:index format from ToStringShort())
	message += fmt.Sprintf("%s:%d", outpoint.Hash.String(), outpoint.Index)

	// Add block height as string
	message += fmt.Sprintf("%d", blockHeight)

	// Add payee script as string (ToString() returns hex representation)
	message += hex.EncodeToString(payee)

	// Verify using compact signature (matches C++ obfuScationSigner.VerifyMessage)
	valid, err := crypto.VerifyCompactSignature(pubKey, message, signature)
	if err != nil {
		return fmt.Errorf("masternode winner signature verification failed: %w", err)
	}

	if !valid {
		return fmt.Errorf("masternode winner signature verification failed: signature invalid")
	}

	return nil
}

// verifyMasternodeEligibility verifies masternode is eligible to be winner at block height
func (s *Server) verifyMasternodeEligibility(outpoint types.Outpoint, blockHeight uint32, tier uint8) error {
	// Masternode eligibility checks:
	// 1. Masternode must be registered and active
	// 2. Masternode must have correct tier collateral
	// 3. Masternode must not have been paid too recently
	// 4. Masternode must be in valid payment queue position

	// Note: This requires integration with masternode manager
	// For now, perform basic validation
	s.logger.WithFields(map[string]interface{}{
		"outpoint":     outpoint.String(),
		"block_height": blockHeight,
		"tier":         tier,
	}).Debug("Masternode eligibility check")

	// Validate tier is in valid range
	if tier > 3 { // 0=Bronze, 1=Silver, 2=Gold, 3=Platinum
		return fmt.Errorf("invalid masternode tier: %d", tier)
	}

	// Check if masternode manager is available
	if s.mnManager == nil {
		s.logger.Debug("Masternode manager not available, skipping eligibility check")
		return nil // Accept without verification if manager not available
	}

	// Check active status
	if !s.mnManager.IsActive(outpoint, blockHeight) {
		return fmt.Errorf("masternode not active at height %d", blockHeight)
	}

	// Check tier matches
	mnTier, err := s.mnManager.GetTier(outpoint)
	if err != nil {
		return fmt.Errorf("failed to get masternode tier: %w", err)
	}
	if mnTier != tier {
		return fmt.Errorf("masternode tier mismatch: expected %d, got %d", tier, mnTier)
	}

	// Check payment queue position (optional - can be lenient during initial sync)
	queuePos, err := s.mnManager.GetPaymentQueuePosition(outpoint, blockHeight)
	if err != nil {
		s.logger.WithError(err).Debug("Failed to get payment queue position")
		// Don't fail on queue position errors - this is informational
	} else {
		s.logger.WithField("queue_position", queuePos).Debug("Masternode payment queue position")
	}

	// Check last paid block (ensure not paid too recently)
	lastPaid, err := s.mnManager.GetLastPaidBlock(outpoint)
	if err != nil {
		s.logger.WithError(err).Debug("Failed to get last paid block")
		// Don't fail - this is informational
	} else if lastPaid > 0 {
		s.logger.WithFields(map[string]interface{}{
			"last_paid":     lastPaid,
			"current_block": blockHeight,
		}).Debug("Masternode last payment info")
	}

	return nil
}

// validateMasternodePaymentAmount validates payment amount for masternodes
// IMPORTANT: In TWINS protocol, ALL masternodes receive the SAME payment (80% of block reward)
// regardless of tier. Tier only affects selection probability, NOT payment amount.
func (s *Server) validateMasternodePaymentAmount(tier uint8, blockHeight uint32) error {
	// Validate tier
	if tier > 3 {
		return fmt.Errorf("invalid masternode tier: %d", tier)
	}

	s.logger.WithFields(map[string]interface{}{
		"tier":         tier,
		"payment_pct":  80, // All tiers receive 80% of block reward
		"block_height": blockHeight,
	}).Debug("Masternode payment amount validation")

	// Note: Actual payment amount validation requires:
	// 1. Get block reward for height: reward := s.blockchain.GetBlockReward(blockHeight)
	// 2. Calculate expected payment: expected := (reward * 80) / 100
	// 3. Verify payment in coinstake matches expected amount
	//
	// ALL masternodes (Bronze/Silver/Gold/Platinum) receive the SAME payment amount.
	// Tier affects selection frequency only (1x/5x/20x/100x probability weight).
	//
	// This validation is primarily performed during block validation
	// Here we just validate the tier is valid

	return nil
}

// handleMasternodeBroadcast handles incoming masternode broadcast messages
func (s *Server) handleMasternodeBroadcast(peer *Peer, msg *Message) {
	peerAddr := peer.GetAddress().String()
	s.logger.WithField("peer", peerAddr).Debug("Received masternode broadcast")

	// Check if masternode manager is available
	if s.mnManager == nil {
		s.logger.Debug("Masternode manager not available, ignoring broadcast")
		return
	}

	// Deserialize masternode broadcast from payload
	mnb, err := DeserializeMasternodeBroadcast(msg.Payload)
	if err != nil {
		s.logger.WithError(err).Warn("Failed to deserialize masternode broadcast")
		return
	}

	// Debug: emit network-layer broadcast receipt
	if dc := s.debugCollector.Load(); dc != nil && dc.IsEnabled() {
		dc.EmitNetwork("network_mnb_received", peerAddr, fmt.Sprintf("MNB received from %s for %s", peerAddr, mnb.OutPoint.String()), map[string]any{
			"outpoint": mnb.OutPoint.String(),
			"protocol": mnb.Protocol,
		})
	}

	// Process the broadcast through masternode manager
	// NOTE: ProcessBroadcast handles relay internally via broadcastRelayFunc (legacy pattern).
	// Legacy C++ ProcessMessage("mnb") does NOT relay externally - relay is solely inside
	// CheckInputsAndAdd() (masternode.cpp:702). Do NOT add relayMessage here to avoid
	// double-relay storm that overwhelms the message queue.
	err = s.mnManager.ProcessBroadcast(mnb, peer.GetAddress().String())
	if err != nil {
		if errors.Is(err, masternode.ErrBroadcastAlreadySeen) {
			// Duplicate broadcast - not an error, just skip silently
			if dc := s.debugCollector.Load(); dc != nil && dc.IsEnabled() {
				dc.EmitBroadcast(debug.TypeBroadcastDedup, peerAddr, fmt.Sprintf("Broadcast dedup for %s from %s (already seen)", mnb.OutPoint.String(), peerAddr), map[string]any{
					"outpoint": mnb.OutPoint.String(),
					"protocol": mnb.Protocol,
				})
			}
			return
		}
		s.logger.WithError(err).Debug("Failed to process masternode broadcast")
		return
	}

	s.logger.WithField("outpoint", mnb.OutPoint.String()).Debug("Masternode broadcast processed successfully")
}

// MASTERNODE_MIN_MNP_SECONDS is the minimum interval between masternode pings (10 minutes)
// Also used as rate limit interval for AskForMN requests
// Matches legacy C++ MASTERNODE_MIN_MNP_SECONDS from masternode.h:19
const MASTERNODE_MIN_MNP_SECONDS = 10 * 60

// handleMasternodePing handles incoming masternode ping messages
// Reference: legacy/src/masternodeman.cpp:868-893
func (s *Server) handleMasternodePing(peer *Peer, msg *Message) {
	peerAddr := peer.GetAddress().String()
	s.logger.WithField("peer", peerAddr).Debug("Received masternode ping")

	// Check if masternode manager is available
	if s.mnManager == nil {
		s.logger.Debug("Masternode manager not available, ignoring ping")
		return
	}

	// Deserialize masternode ping from payload
	mnp, err := DeserializeMasternodePing(msg.Payload)
	if err != nil {
		s.logger.WithError(err).Warn("Failed to deserialize masternode ping")
		return
	}
	if len(mnp.Signature) != 65 {
		s.logger.WithFields(logrus.Fields{
			"peer":      peerAddr,
			"outpoint":  mnp.OutPoint.String(),
			"sig_bytes": len(mnp.Signature),
		}).Warn("Rejecting masternode ping with invalid compact signature length")
		if s.config != nil {
			s.Misbehaving(peer, 33, "invalid masternode ping signature length")
		}
		return
	}

	// Debug: emit network-layer ping receipt
	if dc := s.debugCollector.Load(); dc != nil && dc.IsEnabled() {
		dc.EmitNetwork("network_mnp_received", peerAddr, fmt.Sprintf("MNP received from %s for %s", peerAddr, mnp.OutPoint.String()), map[string]any{
			"outpoint": mnp.OutPoint.String(),
			"sig_time": mnp.SigTime,
		})
	}

	s.logger.WithFields(map[string]interface{}{
		"peer":     peerAddr,
		"outpoint": mnp.OutPoint.String(),
	}).Debug("Processing masternode ping message")

	// Process the ping through masternode manager
	err = s.mnManager.ProcessPing(mnp, peerAddr)
	if err != nil {
		s.logger.WithError(err).WithField("outpoint", mnp.OutPoint.String()).Warn("Failed to process masternode ping")

		// Legacy behavior: If ping processing failed, check if masternode exists
		// If not found, ask the peer for the masternode broadcast
		// Reference: legacy/src/masternodeman.cpp:884-893
		_, mnErr := s.mnManager.GetMasternode(mnp.OutPoint)
		if mnErr != nil {
			// Masternode not found - ask peer for broadcast
			s.AskForMN(peer, mnp.OutPoint)
		}
		return
	}

	s.logger.WithField("outpoint", mnp.OutPoint.String()).Debug("Masternode ping processed successfully")
}

// AskForMN sends a dseg request for a specific masternode to a peer
// Used when we receive a ping from an unknown masternode
// Reference: legacy/src/masternodeman.cpp:218-232
func (s *Server) AskForMN(peer *Peer, outpoint types.Outpoint) {
	// Rate limiting: check if we've asked for this masternode recently
	s.askForMNMu.RLock()
	nextAllowed, exists := s.askForMN[outpoint]
	s.askForMNMu.RUnlock()

	now := time.Now().Unix()
	if exists && now < nextAllowed {
		s.logger.WithFields(map[string]interface{}{
			"outpoint":     outpoint.String(),
			"next_allowed": time.Unix(nextAllowed, 0).Format(time.RFC3339),
		}).Debug("AskForMN rate limited - asked recently")
		return
	}

	s.logger.WithFields(map[string]interface{}{
		"peer":     peer.GetAddress().String(),
		"outpoint": outpoint.String(),
	}).Debug("Asking peer for missing masternode entry")

	// Send dseg message requesting specific masternode
	// Format: CTxIn (prevout + scriptSig + sequence)
	var buf bytes.Buffer

	// Write prevout hash (32 bytes)
	buf.Write(outpoint.Hash[:])

	// Write prevout index (4 bytes)
	binary.Write(&buf, binary.LittleEndian, outpoint.Index)

	// Write empty scriptSig (varint 0)
	buf.WriteByte(0x00)

	// Write sequence (4 bytes) - 0xFFFFFFFF for standard
	binary.Write(&buf, binary.LittleEndian, uint32(0xFFFFFFFF))

	msg := NewMessage(MsgDSEG, buf.Bytes(), s.getMagicBytes())
	if err := peer.SendMessage(msg); err != nil {
		s.logger.WithError(err).WithField("peer", peer.GetAddress().String()).
			Warn("Failed to send dseg request to peer")
		return
	}

	// N-1: emit outbound dseg request event so the GUI's DSEGRequests count
	// includes dseg sends WE initiate, not just inbound dseg from peers.
	s.emitDSEGRequest(peer.GetAddress().String(), outpoint.String(), "out", 0)

	// Update rate limit timestamp
	s.askForMNMu.Lock()
	s.askForMN[outpoint] = now + MASTERNODE_MIN_MNP_SECONDS
	s.askForMNMu.Unlock()

	s.logger.WithField("outpoint", outpoint.String()).Debug("Sent dseg request for missing masternode")
}

// handleMNGet processes a masternode list request
func (s *Server) handleMNGet(peer *Peer, msg *Message) {
	s.logger.WithField("peer", peer.GetAddress().String()).Debug("Received masternode list request (mnget)")

	// Check if masternode manager is available
	if s.mnManager == nil {
		s.logger.Debug("Masternode manager not available, ignoring mnget request")
		return
	}

	// Get all active masternodes from the manager
	mnList := s.mnManager.GetMasternodeList()
	if mnList == nil {
		s.logger.Debug("No masternode list available")
		return
	}

	s.logger.WithFields(map[string]interface{}{
		"peer":  peer.GetAddress().String(),
		"count": len(mnList.Masternodes),
	}).Debug("Sending masternode list to peer")

	// Send each masternode as a separate MsgMasternode message
	sentCount := 0
	for _, mn := range mnList.Masternodes {
		// Convert Masternode to MasternodeBroadcast for transmission
		// Use the original collateral key and signature from the broadcast
		mnb := &masternode.MasternodeBroadcast{
			OutPoint:         mn.OutPoint,
			Addr:             mn.Addr,
			PubKeyCollateral: mn.PubKeyCollateral, // Original collateral key from broadcast
			PubKeyMasternode: mn.PubKey,           // Operator key
			Signature:        mn.Signature,        // Original broadcast signature
			SigTime:          mn.SigTime,          // Original signature timestamp
			Protocol:         mn.Protocol,
			LastPing:         mn.LastPingMessage, // Use the stored ping message (critical for legacy nodes)
			LastDsq:          0,                  // Set to 0 for now
		}

		// Serialize the masternode broadcast
		payload, err := SerializeMasternodeBroadcast(mnb)
		if err != nil {
			s.logger.WithFields(map[string]interface{}{
				"outpoint": mn.OutPoint.String(),
				"error":    err,
			}).Warn("Failed to serialize masternode broadcast")
			continue
		}

		// Create and send the message
		msg := NewMessage(MsgMasternode, payload, s.params.NetMagicBytes)
		if err := peer.SendMessage(msg); err != nil {
			s.logger.WithFields(map[string]interface{}{
				"outpoint": mn.OutPoint.String(),
				"error":    err,
			}).Warn("Failed to send masternode broadcast to peer")
			continue
		}

		sentCount++
		s.logger.WithFields(map[string]interface{}{
			"outpoint": mn.OutPoint.String(),
			"tier":     mn.Tier,
		}).Debug("Sent masternode to peer")
	}

	s.logger.WithFields(map[string]interface{}{
		"peer":  peer.GetAddress().String(),
		"sent":  sentCount,
		"total": len(mnList.Masternodes),
	}).Info("Masternode list sent successfully")
}

// MASTERNODES_DSEG_SECONDS is the rate limit interval for dseg requests (3 hours)
// Matches legacy C++ MASTERNODES_DSEG_SECONDS from masternodeman.h:19
const MASTERNODES_DSEG_SECONDS = 3 * 60 * 60

// MASTERNODE_SYNC_LIST is the sync status code for masternode list sync
// Matches legacy C++ MASTERNODE_SYNC_LIST from masternode-sync.h:12
const MASTERNODE_SYNC_LIST = 2

// handleDSEG processes a masternode list segment request (dseg)
// This is NOT governance - dseg is used to sync the masternode list from peers
// Reference: legacy/src/masternodeman.cpp:894-946
func (s *Server) handleDSEG(peer *Peer, msg *Message) {
	peerAddr := peer.GetAddress().String()
	s.logger.WithField("peer", peerAddr).Debug("Received masternode list request (dseg)")

	// Check if masternode manager is available
	if s.mnManager == nil {
		s.logger.Debug("Masternode manager not available, ignoring dseg request")
		return
	}

	// Inbound dseg from peer asking us for masternode list. N-1: tag with
	// direction:"in" so the GUI can distinguish inbound vs outbound dseg.
	s.emitDSEGRequest(peerAddr, "", "in", len(msg.Payload))

	// Parse CTxIn from payload to determine if requesting all or specific masternode
	// Empty CTxIn (null hash + index 0xFFFFFFFF) = request all masternodes
	// Specific CTxIn = request single masternode by outpoint
	var requestedOutpoint types.Outpoint
	requestAll := true

	if len(msg.Payload) >= 36 {
		buf := bytes.NewReader(msg.Payload)
		outpoint, _, _, err := DeserializeCTxIn(buf)
		if err != nil {
			s.logger.WithError(err).Debug("Failed to parse CTxIn from dseg request, treating as request-all")
		} else {
			// Check if this is an empty CTxIn (request all)
			// Legacy uses CTxIn() default constructor which has null hash and index 0xFFFFFFFF
			emptyHash := types.Hash{}
			if outpoint.Hash != emptyHash || outpoint.Index != 0xFFFFFFFF {
				requestAll = false
				requestedOutpoint = outpoint
				s.logger.WithField("outpoint", outpoint.String()).Debug("dseg request for specific masternode")
			}
		}
	}

	// Rate limiting for full list requests (not for specific masternode requests)
	if requestAll {
		peerAddr := peer.GetAddress().String()

		// Check if peer is on local network (RFC1918 or localhost) - skip rate limiting
		isLocal := false
		if addr := peer.GetAddress(); addr != nil && addr.IP != nil {
			isLocal = addr.IP.IsLoopback() || addr.IP.IsPrivate()
		}

		if !isLocal {
			s.dsegRateLimitMu.RLock()
			nextAllowed, exists := s.dsegRateLimit[peerAddr]
			s.dsegRateLimitMu.RUnlock()

			now := time.Now().Unix()
			if exists && now < nextAllowed {
				s.logger.WithFields(map[string]interface{}{
					"peer":         peerAddr,
					"next_allowed": time.Unix(nextAllowed, 0).Format(time.RFC3339),
				}).Debug("dseg request rate limited - peer already asked recently")
				return
			}

			// Update rate limit timestamp
			s.dsegRateLimitMu.Lock()
			s.dsegRateLimit[peerAddr] = now + MASTERNODES_DSEG_SECONDS
			s.dsegRateLimitMu.Unlock()
		} else {
			// Local peer - no rate limiting needed
		}
	}

	// Get masternode list
	mnList := s.mnManager.GetMasternodeList()
	if mnList == nil || len(mnList.Masternodes) == 0 {
		s.logger.Debug("No masternodes available to send")
		// Still send ssc with count=0 for full list requests
		if requestAll {
			s.sendSyncStatusCount(peer, MASTERNODE_SYNC_LIST, 0)
		}
		return
	}

	// Send actual masternode broadcasts (mnb messages), NOT inventory items
	// This matches the legacy C++ behavior in masternodeman.cpp:926-929
	// Legacy: pfrom->PushMessage("mnb", mnb) - sends actual mnb directly
	sentCount := 0

	for _, mn := range mnList.Masternodes {
		// Skip masternodes on RFC1918 (private) networks
		if mn.Addr != nil {
			if tcpAddr, ok := mn.Addr.(*net.TCPAddr); ok {
				if tcpAddr.IP.IsPrivate() {
					continue
				}
			}
		}

		// Only send enabled masternodes
		if !mn.IsActive() {
			continue
		}

		// If requesting specific masternode, only send that one
		if !requestAll {
			if mn.OutPoint != requestedOutpoint {
				continue
			}
		}

		// Create masternode broadcast for this masternode
		mnb := &masternode.MasternodeBroadcast{
			OutPoint:         mn.OutPoint,
			Addr:             mn.Addr,
			PubKeyCollateral: mn.PubKeyCollateral,
			PubKeyMasternode: mn.PubKey,
			Signature:        mn.Signature,
			SigTime:          mn.SigTime,
			Protocol:         mn.Protocol,
			LastPing:         mn.LastPingMessage,
			LastDsq:          0,
		}

		// Seed seenBroadcasts to prevent relay-bounce: when the receiving peer relays
		// this mnb back to us, we must recognize it as already-seen and not reprocess it.
		// Without this, dseg responses cause a relay storm (each broadcast round-trips).
		s.mnManager.MarkBroadcastSeen(mnb)

		// Serialize the masternode broadcast
		payload, err := SerializeMasternodeBroadcast(mnb)
		if err != nil {
			s.logger.WithFields(map[string]interface{}{
				"outpoint": mn.OutPoint.String(),
				"error":    err,
			}).Warn("Failed to serialize masternode broadcast for dseg")
			continue
		}

		// Send actual mnb message (NOT inv!)
		// This is the critical fix - legacy C++ sends mnb directly
		mnbMsg := NewMessage(MsgMasternode, payload, s.getMagicBytes())
		if err := peer.SendMessage(mnbMsg); err != nil {
			s.logger.WithFields(map[string]interface{}{
				"outpoint": mn.OutPoint.String(),
				"error":    err,
			}).Warn("Failed to send masternode broadcast in dseg response")
			continue
		}

		sentCount++
		s.logger.WithFields(map[string]interface{}{
			"outpoint": mn.OutPoint.String(),
			"tier":     mn.Tier,
		}).Debug("dseg - sent masternode broadcast to peer")

		// If requesting specific masternode, we're done after finding it
		if !requestAll {
			s.logger.WithField("outpoint", requestedOutpoint.String()).Debug("dseg - sent 1 masternode entry")
			return
		}
	}

	// Send sync status count for full list requests
	// Format: ssc message with MASTERNODE_SYNC_LIST type and count
	if requestAll {
		s.sendSyncStatusCount(peer, MASTERNODE_SYNC_LIST, sentCount)

		// Debug: emit dseg response event
		if dc := s.debugCollector.Load(); dc != nil && dc.IsEnabled() {
			dc.EmitNetwork("dseg_response", peerAddr, fmt.Sprintf("DSEG response to %s: sent %d masternodes", peerAddr, sentCount), map[string]any{
				"sent_count":  sentCount,
				"request_all": true,
			})
		}

		s.logger.WithFields(map[string]interface{}{
			"peer":  peerAddr,
			"count": sentCount,
		}).Debug("dseg - sent masternode broadcasts to peer")
	}
}

// sendSyncStatusCount sends an ssc (sync status count) message to a peer
// This tells the peer how many items were sent for a particular sync type
// Reference: legacy/src/masternodeman.cpp:943
func (s *Server) sendSyncStatusCount(peer *Peer, syncType int, count int) {
	// Format: syncType (4 bytes) + count (4 bytes)
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, int32(syncType))
	binary.Write(&buf, binary.LittleEndian, int32(count))

	msg := NewMessage(MsgSSC, buf.Bytes(), s.getMagicBytes())
	if err := peer.SendMessage(msg); err != nil {
		s.logger.WithError(err).WithField("peer", peer.GetAddress().String()).
			Warn("Failed to send sync status count to peer")
	}
}

// handleSyncStatusCount handles an incoming ssc (sync status count) message from a peer
// This tells us how many items the peer has sent for a particular sync type
// Reference: legacy/src/masternode-sync.cpp ProcessMessage for "ssc"
func (s *Server) handleSyncStatusCount(peer *Peer, msg *Message) {
	if len(msg.Payload) < 8 {
		return
	}

	syncType := int32(binary.LittleEndian.Uint32(msg.Payload[0:4]))
	count := int32(binary.LittleEndian.Uint32(msg.Payload[4:8]))

	peerAddr := peer.GetAddress().String()

	s.logger.WithFields(logrus.Fields{
		"peer":      peerAddr,
		"sync_type": syncType,
		"count":     count,
	}).Debug("Received sync status count from peer")

	// Sync types from legacy masternode-sync.h:
	// MASTERNODE_SYNC_INITIAL = 0
	// MASTERNODE_SYNC_SPORKS = 1
	// MASTERNODE_SYNC_LIST = 2 (masternode list)
	// MASTERNODE_SYNC_MNW = 3 (masternode winners)
	// MASTERNODE_SYNC_BUDGET = 4 (budget - disabled)
	// MASTERNODE_SYNC_FAILED = 998
	// MASTERNODE_SYNC_FINISHED = 999

	// Forward to masternode sync manager for per-peer tracking
	if s.mnManager != nil {
		s.mnManager.ProcessSyncStatusCount(peerAddr, int(syncType), int(count))
	}
}
// emitDSEGRequest emits a "dseg_request" debug event with a structured payload.
// direction is "in" (peer asking us) or "out" (we asking a peer). outpoint is
// either a specific masternode outpoint string or "" for a full-list request.
// payloadSize is informational (only meaningful for inbound; pass 0 for outbound).
//
// Centralized so all three dseg-request emit sites (AskForMN outbound,
// RequestMasternodeList outbound full-list, handleDSEG inbound) share the
// same payload schema. N-1 from the masternode-debug-tab research audit.
func (s *Server) emitDSEGRequest(peerAddr, outpoint, direction string, payloadSize int) {
	dc := s.debugCollector.Load()
	if dc == nil || !dc.IsEnabled() {
		return
	}
	var summary string
	switch direction {
	case "in":
		summary = fmt.Sprintf("DSEG request from %s (%d bytes)", peerAddr, payloadSize)
	case "out":
		if outpoint == "" {
			summary = fmt.Sprintf("DSEG outbound to %s (full list)", peerAddr)
		} else {
			summary = fmt.Sprintf("DSEG outbound to %s for %s", peerAddr, outpoint)
		}
	}
	payload := map[string]any{
		"peer":      peerAddr,
		"direction": direction,
	}
	if outpoint != "" {
		payload["outpoint"] = outpoint
	}
	if payloadSize > 0 {
		payload["payload_size"] = payloadSize
	}
	dc.EmitNetwork("dseg_request", peerAddr, summary, payload)
}
