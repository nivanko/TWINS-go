package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/twins-dev/twins-core/internal/gui/core"
	"github.com/twins-dev/twins-core/internal/masternode"
	binary "github.com/twins-dev/twins-core/internal/storage/binary"
	"github.com/twins-dev/twins-core/internal/wallet"
	"github.com/twins-dev/twins-core/pkg/crypto"
	"github.com/twins-dev/twins-core/pkg/types"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ==========================================
// Masternode Page Methods
// ==========================================

// GetMyMasternodes returns user's configured masternodes for the UI table.
// Cross-references masternode.conf entries against the network masternode list
// to display actual status (ENABLED, PRE_ENABLED, etc.) instead of a static value.
func (a *App) GetMyMasternodes() ([]core.MyMasternode, error) {
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if confFile == nil {
		return []core.MyMasternode{}, nil
	}

	entries := confFile.GetEntries()
	result := make([]core.MyMasternode, 0, len(entries))

	currentTime := time.Now()
	expireTime := time.Duration(masternode.ExpirationSeconds) * time.Second

	for _, entry := range entries {
		mn := core.MyMasternode{
			Alias:       entry.Alias,
			Address:     entry.IP,
			Protocol:    int(masternode.ActiveProtocolVersion),
			Status:      "MISSING",
			TxHash:      entry.TxHash.String(),
			OutputIndex: int(entry.OutputIndex),
		}

		// Cross-reference with network masternode list for actual status
		if components != nil && components.Masternode != nil {
			outpoint := entry.GetOutpoint()
			if networkMN, err := components.Masternode.GetMasternode(outpoint); err == nil {
				// Refresh status to match Network page behavior
				networkMN.UpdateStatus(currentTime, expireTime)

				mn.Status = strings.ToUpper(strings.ReplaceAll(networkMN.Status.String(), "-", "_"))
				mn.Protocol = int(networkMN.Protocol)

				// Populate active time as live-incrementing duration since activation
				if !networkMN.ActiveSince.IsZero() {
					activeTime := currentTime.Unix() - networkMN.ActiveSince.Unix()
					if activeTime < 0 {
						activeTime = 0
					}
					mn.ActiveSeconds = activeTime
				}

				mn.LastSeen = networkMN.LastSeen

				// Populate collateral address from network masternode's collateral public key
				mn.CollateralAddress = networkMN.GetPayee()
			}
		}

		result = append(result, mn)
	}

	return result, nil
}

// StartMasternode starts a single masternode by alias
// This creates and broadcasts a MasternodeBroadcast message to the P2P network.
// The broadcast announces this masternode to the network, enabling it to receive payments.
// For remote (cold wallet) operation, the hot node will receive this broadcast and enable itself.
func (a *App) StartMasternode(alias string) error {
	if err := a.startMasternodeInternal(alias); err != nil {
		return err
	}

	// Emit event for UI refresh
	runtime.EventsEmit(a.ctx, "masternode:updated", nil)
	return nil
}

// startMasternodeInternal contains the core logic for starting a masternode without emitting UI events.
// This allows batch operations to call it multiple times and emit a single event at the end.
//
// This implements "cold wallet" masternode start - the GUI controls remote masternodes by creating
// and broadcasting MasternodeBroadcast messages. The hot node (VPS) receives these broadcasts
// and enables itself via EnableHotColdMasterNodeRemote().
func (a *App) startMasternodeInternal(alias string) error {
	// Get required components
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	w := a.wallet
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if confFile == nil {
		return fmt.Errorf("masternode config not initialized")
	}
	if w == nil {
		return fmt.Errorf("wallet not initialized")
	}
	if components == nil || components.Masternode == nil {
		return fmt.Errorf("masternode manager not initialized")
	}

	// Get the masternode entry from masternode.conf
	entry := confFile.GetEntry(alias)
	if entry == nil {
		return fmt.Errorf("masternode alias '%s' not found in masternode.conf", alias)
	}

	// Get the collateral UTXO to find its address
	// The collateral is the UTXO specified by txHash:outputIndex in masternode.conf
	outpoint := entry.GetOutpoint()
	utxos, err := w.GetUTXOsByOutpoints([]types.Outpoint{outpoint})
	if err != nil {
		return fmt.Errorf("failed to get collateral UTXO: %w (ensure the collateral transaction is in your wallet)", err)
	}
	if len(utxos) == 0 {
		return fmt.Errorf("collateral UTXO not found in wallet: %s:%d", entry.TxHash.String(), entry.OutputIndex)
	}

	collateralUTXO := utxos[0]
	if collateralUTXO.Address == "" {
		return fmt.Errorf("collateral UTXO has no associated address")
	}

	// Get the private key for the collateral address from the wallet
	collateralKey, err := w.GetPrivateKeyForAddress(collateralUTXO.Address)
	if err != nil {
		return fmt.Errorf("failed to get collateral private key: %w (is your wallet unlocked?)", err)
	}

	// Create the masternode broadcast message using the Manager's cold wallet method
	// This doesn't require an ActiveMasternode - just blockchain access for the block hash
	broadcast, err := components.Masternode.CreateBroadcastForRemote(entry, collateralKey)
	if err != nil {
		return fmt.Errorf("failed to create masternode broadcast: %w", err)
	}

	// Process and relay the broadcast to the P2P network (no origin peer for GUI-initiated broadcasts)
	// ProcessBroadcast validates, stores in masternode list, and relays to peers
	if err := components.Masternode.ProcessBroadcast(broadcast, ""); err != nil && !errors.Is(err, masternode.ErrBroadcastAlreadySeen) {
		return fmt.Errorf("failed to process masternode broadcast: %w", err)
	}

	return nil
}

// StartAllMasternodes starts all configured masternodes
// This iterates through all entries in masternode.conf and starts each one.
// Errors for individual masternodes are collected but don't stop other starts.
func (a *App) StartAllMasternodes() error {
	// Get required components
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	a.componentsMu.RUnlock()

	if confFile == nil {
		return fmt.Errorf("masternode config not initialized")
	}

	entries := confFile.GetEntries()
	if len(entries) == 0 {
		return fmt.Errorf("no masternodes configured in masternode.conf")
	}

	var errors []string
	started := 0

	for _, entry := range entries {
		if err := a.startMasternodeInternal(entry.Alias); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", entry.Alias, err))
		} else {
			started++
		}
	}

	// Emit single event for UI refresh after all operations
	runtime.EventsEmit(a.ctx, "masternode:updated", nil)

	if len(errors) > 0 {
		if started == 0 {
			return fmt.Errorf("failed to start any masternodes: %s", strings.Join(errors, "; "))
		}
		return fmt.Errorf("started %d masternodes, but %d failed: %s", started, len(errors), strings.Join(errors, "; "))
	}

	return nil
}

// StartMissingMasternodes starts only masternodes that are not currently in the network list
// This is useful after network restarts or when masternodes have expired from the network.
// Returns the count of successfully started masternodes.
func (a *App) StartMissingMasternodes() (int, error) {
	// Get required components
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	components := a.coreComponents
	a.componentsMu.RUnlock()

	if confFile == nil {
		return 0, fmt.Errorf("masternode config not initialized")
	}
	if components == nil || components.Masternode == nil {
		return 0, fmt.Errorf("masternode manager not initialized")
	}

	entries := confFile.GetEntries()
	if len(entries) == 0 {
		return 0, nil // No masternodes configured
	}

	// Build a set of known masternode outpoints from the network
	knownOutpoints := make(map[string]bool)
	networkMNs := components.Masternode.GetMasternodes()
	for outpoint := range networkMNs {
		// Create key from outpoint (txid:vout)
		key := fmt.Sprintf("%s:%d", outpoint.Hash.String(), outpoint.Index)
		knownOutpoints[key] = true
	}

	// Start masternodes that are not in the network list
	started := 0
	for _, entry := range entries {
		key := fmt.Sprintf("%s:%d", entry.TxHash.String(), entry.OutputIndex)
		if !knownOutpoints[key] {
			// This masternode is missing from the network - start it
			if err := a.startMasternodeInternal(entry.Alias); err != nil {
				// Log error but continue with others
				logrus.Warnf("Failed to start missing masternode %s: %v", entry.Alias, err)
			} else {
				started++
			}
		}
	}

	// Emit single event for UI refresh after all operations
	if started > 0 {
		runtime.EventsEmit(a.ctx, "masternode:updated", nil)
	}
	return started, nil
}

// GetNetworkMasternodes returns all masternodes on the network
// This provides visibility into the entire masternode network, not just user's configured nodes
func (a *App) GetNetworkMasternodes() ([]core.MasternodeInfo, error) {
	a.componentsMu.RLock()
	client := a.coreClient
	a.componentsMu.RUnlock()

	if client == nil {
		return []core.MasternodeInfo{}, nil
	}

	// MasternodeList returns all network masternodes
	// Empty filter returns all masternodes
	masternodes, err := client.MasternodeList("")
	if err != nil {
		return nil, fmt.Errorf("failed to get network masternodes: %w", err)
	}

	return masternodes, nil
}

// MasternodeStatistics contains tier distribution and network statistics
type MasternodeStatistics struct {
	TierCounts      map[string]int     `json:"tierCounts"`      // bronze: X, silver: Y, etc.
	StatusCounts    map[string]int     `json:"statusCounts"`    // ENABLED: X, PRE_ENABLED: Y, etc.
	TotalCount      int                `json:"totalCount"`
	EnabledCount    int                `json:"enabledCount"`
	TotalCollateral int64              `json:"totalCollateral"` // In whole TWINS coins (for display purposes)
	TierPercentages map[string]float64 `json:"tierPercentages"`
}

// GetMasternodeStatistics returns tier distribution and network stats
// Calculated from the network masternode list for efficiency
func (a *App) GetMasternodeStatistics() (*MasternodeStatistics, error) {
	a.componentsMu.RLock()
	client := a.coreClient
	a.componentsMu.RUnlock()

	if client == nil {
		return &MasternodeStatistics{
			TierCounts:      map[string]int{"bronze": 0, "silver": 0, "gold": 0, "platinum": 0},
			StatusCounts:    map[string]int{},
			TierPercentages: map[string]float64{"bronze": 0, "silver": 0, "gold": 0, "platinum": 0},
		}, nil
	}

	// Get all network masternodes
	masternodes, err := client.MasternodeList("")
	if err != nil {
		return nil, fmt.Errorf("failed to get network masternodes: %w", err)
	}

	// Initialize statistics
	stats := &MasternodeStatistics{
		TierCounts: map[string]int{
			"bronze":   0,
			"silver":   0,
			"gold":     0,
			"platinum": 0,
		},
		StatusCounts:    make(map[string]int),
		TierPercentages: make(map[string]float64),
	}

	// Count masternodes by tier and status
	for _, mn := range masternodes {
		stats.TotalCount++

		// Count by tier (lowercase for consistency)
		tier := strings.ToLower(mn.Tier)
		if tier == "" {
			tier = "unknown"
		}
		stats.TierCounts[tier]++

		// Count by status
		status := strings.ToUpper(mn.Status)
		stats.StatusCounts[status]++

		if status == "ENABLED" {
			stats.EnabledCount++
		}
	}

	// Calculate total collateral (in TWINS, not satoshis)
	// Bronze: 1M, Silver: 5M, Gold: 20M, Platinum: 100M
	stats.TotalCollateral =
		int64(stats.TierCounts["bronze"])*1_000_000 +
			int64(stats.TierCounts["silver"])*5_000_000 +
			int64(stats.TierCounts["gold"])*20_000_000 +
			int64(stats.TierCounts["platinum"])*100_000_000

	// Calculate percentages based on known tier counts only
	// This ensures percentages sum to 100% even if some masternodes have unknown tier
	knownTierTotal := stats.TierCounts["bronze"] + stats.TierCounts["silver"] +
		stats.TierCounts["gold"] + stats.TierCounts["platinum"]
	if knownTierTotal > 0 {
		for tier, count := range stats.TierCounts {
			if tier != "unknown" {
				stats.TierPercentages[tier] = float64(count) / float64(knownTierTotal) * 100
			}
		}
	}

	return stats, nil
}

// ==========================================
// Masternode Configuration Dialog Methods
// ==========================================

// MasternodeConfigEntry represents a masternode.conf entry for the frontend
type MasternodeConfigEntry struct {
	Alias       string `json:"alias"`
	IP          string `json:"ip"` // IP:Port format
	PrivateKey  string `json:"privateKey"`
	TxHash      string `json:"txHash"`
	OutputIndex int    `json:"outputIndex"`
}

// MasternodeOutput represents a wallet UTXO valid for masternode collateral
type MasternodeOutput struct {
	TxHash        string  `json:"txHash"`
	OutputIndex   int     `json:"outputIndex"`
	Amount        float64 `json:"amount"`        // In TWINS
	Tier          string  `json:"tier"`          // Bronze/Silver/Gold/Platinum
	Confirmations int     `json:"confirmations"` // Current confirmation count
	IsReady       bool    `json:"isReady"`       // True when confirmations >= 15
}

// GetMasternodeConfig returns all masternode.conf entries
func (a *App) GetMasternodeConfig() ([]MasternodeConfigEntry, error) {
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	a.componentsMu.RUnlock()

	if confFile == nil {
		return []MasternodeConfigEntry{}, nil
	}

	// Re-read the file to get latest entries
	if err := confFile.Read(); err != nil {
		return nil, fmt.Errorf("failed to read masternode.conf: %w", err)
	}

	entries := confFile.GetEntries()
	result := make([]MasternodeConfigEntry, len(entries))
	for i, entry := range entries {
		result[i] = MasternodeConfigEntry{
			Alias:       entry.Alias,
			IP:          entry.IP,
			PrivateKey:  entry.PrivKey,
			TxHash:      entry.TxHash.String(),
			OutputIndex: int(entry.OutputIndex),
		}
	}
	return result, nil
}

// AddMasternodeConfig adds a new entry to masternode.conf
func (a *App) AddMasternodeConfig(entry MasternodeConfigEntry) error {
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	a.componentsMu.RUnlock()

	if confFile == nil {
		return fmt.Errorf("masternode config not initialized")
	}

	// Validate inputs
	if err := a.validateMasternodeEntry(entry); err != nil {
		return err
	}

	// Parse txHash
	txHash, err := types.NewHashFromString(entry.TxHash)
	if err != nil {
		return fmt.Errorf("invalid transaction hash: %w", err)
	}

	// Create masternode entry
	mnEntry := &masternode.MasternodeEntry{
		Alias:       entry.Alias,
		IP:          entry.IP,
		PrivKey:     entry.PrivateKey,
		TxHash:      txHash,
		OutputIndex: uint32(entry.OutputIndex),
	}

	// Add to config (checks for duplicate alias)
	if err := confFile.Add(mnEntry); err != nil {
		return err
	}

	// Save to file
	if err := confFile.Save(); err != nil {
		return fmt.Errorf("failed to save masternode.conf: %w", err)
	}

	// Auto-lock the collateral UTXO to prevent accidental spending
	if a.coreClient != nil {
		outpoint := core.OutPoint{
			TxID: entry.TxHash,
			Vout: uint32(entry.OutputIndex),
		}
		if err := a.coreClient.LockUnspent(false, []core.OutPoint{outpoint}); err != nil {
			// Log but don't fail - collateral is still configured
			logrus.WithError(err).WithField("alias", entry.Alias).Warn("failed to lock collateral UTXO")
		}
	}

	// Emit event for UI refresh
	runtime.EventsEmit(a.ctx, "masternode:config_updated", nil)
	runtime.EventsEmit(a.ctx, "masternode:updated", nil)

	return nil
}

// UpdateMasternodeConfig updates an existing entry by alias
func (a *App) UpdateMasternodeConfig(oldAlias string, entry MasternodeConfigEntry) error {
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	a.componentsMu.RUnlock()

	if confFile == nil {
		return fmt.Errorf("masternode config not initialized")
	}

	// Validate inputs
	if err := a.validateMasternodeEntry(entry); err != nil {
		return err
	}

	// Parse txHash
	txHash, err := types.NewHashFromString(entry.TxHash)
	if err != nil {
		return fmt.Errorf("invalid transaction hash: %w", err)
	}

	// Create new entry
	mnEntry := &masternode.MasternodeEntry{
		Alias:       entry.Alias,
		IP:          entry.IP,
		PrivKey:     entry.PrivateKey,
		TxHash:      txHash,
		OutputIndex: uint32(entry.OutputIndex),
	}

	// Use atomic Update method (validates and replaces within single lock)
	if err := confFile.Update(oldAlias, mnEntry); err != nil {
		return err
	}

	// Save to file
	if err := confFile.Save(); err != nil {
		return fmt.Errorf("failed to save masternode.conf: %w", err)
	}

	// Emit event for UI refresh
	runtime.EventsEmit(a.ctx, "masternode:config_updated", nil)
	runtime.EventsEmit(a.ctx, "masternode:updated", nil)

	return nil
}

// DeleteMasternodeConfig removes an entry by alias
func (a *App) DeleteMasternodeConfig(alias string) error {
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	a.componentsMu.RUnlock()

	if confFile == nil {
		return fmt.Errorf("masternode config not initialized")
	}

	// Get entry before removal to unlock collateral
	entry := confFile.GetEntry(alias)
	if entry == nil {
		return fmt.Errorf("masternode alias '%s' not found", alias)
	}

	// Capture outpoint before removal
	outpoint := core.OutPoint{
		TxID: entry.TxHash.String(),
		Vout: entry.OutputIndex,
	}

	// Remove entry
	if !confFile.Remove(alias) {
		return fmt.Errorf("failed to remove masternode alias '%s'", alias)
	}

	// Save to file
	if err := confFile.Save(); err != nil {
		return fmt.Errorf("failed to save masternode.conf: %w", err)
	}

	// Auto-unlock the collateral UTXO since masternode is removed
	if a.coreClient != nil {
		if err := a.coreClient.LockUnspent(true, []core.OutPoint{outpoint}); err != nil {
			// Log but don't fail - masternode is already removed
			logrus.WithError(err).WithField("alias", alias).Warn("failed to unlock collateral UTXO")
		}
	}

	// Emit event for UI refresh
	runtime.EventsEmit(a.ctx, "masternode:config_updated", nil)
	runtime.EventsEmit(a.ctx, "masternode:updated", nil)

	return nil
}

// GenerateMasternodeKey creates a new masternode private key
// Returns WIF-encoded key compatible with legacy C++ wallet
func (a *App) GenerateMasternodeKey() (string, error) {
	// Generate new masternode key pair
	keyPair, err := crypto.GenerateKeyPair()
	if err != nil {
		return "", fmt.Errorf("failed to generate key: %w", err)
	}

	// Encode to WIF format (legacy C++ compatible)
	// Version byte 66 (0x42) for TWINS mainnet, uncompressed=false (matches legacy MakeNewKey(false))
	wifKey := keyPair.Private.EncodeWIF(66, false)

	return wifKey, nil
}

// GetMasternodeOutputs returns wallet UTXOs valid for masternode collateral
func (a *App) GetMasternodeOutputs() ([]MasternodeOutput, error) {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w == nil {
		return []MasternodeOutput{}, nil
	}

	// Get unspent outputs from wallet including pending (0-14 confirmations)
	// IsReady gates selectability — the 15-confirmation threshold is enforced there
	utxosRaw, err := w.ListUnspent(0, 9999999, []string{})
	if err != nil {
		return nil, fmt.Errorf("failed to get unspent outputs: %w", err)
	}

	// Type assert to slice of UnspentOutput pointers
	utxos, ok := utxosRaw.([]*wallet.UnspentOutput)
	if !ok {
		return nil, fmt.Errorf("unexpected utxos format from wallet")
	}

	result := []MasternodeOutput{}

	// Filter for valid masternode collateral amounts
	for _, utxo := range utxos {
		// Amount is in TWINS (float64), convert to satoshis for tier validation
		// Use math.Round to avoid float64 precision issues near tier boundaries
		amountSatoshis := int64(math.Round(utxo.Amount * 1e8))

		// Check if amount matches any masternode tier collateral
		tier := getTierName(amountSatoshis)
		if tier != "" {
			confirmations := int(utxo.Confirmations)
			result = append(result, MasternodeOutput{
				TxHash:        utxo.TxID,
				OutputIndex:   int(utxo.Vout),
				Amount:        utxo.Amount,
				Tier:          tier,
				Confirmations: confirmations,
				IsReady:       confirmations >= masternode.MinConfirmations,
			})
		}
	}

	return result, nil
}

// ReloadMasternodeConfig reloads the config from file
func (a *App) ReloadMasternodeConfig() error {
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	a.componentsMu.RUnlock()

	if confFile == nil {
		return fmt.Errorf("masternode config not initialized")
	}

	if err := confFile.Read(); err != nil {
		return fmt.Errorf("failed to reload masternode.conf: %w", err)
	}

	// Emit event for UI refresh
	runtime.EventsEmit(a.ctx, "masternode:config_updated", nil)
	runtime.EventsEmit(a.ctx, "masternode:updated", nil)

	return nil
}

// ==========================================
// Validation Helpers
// ==========================================

// validateMasternodeEntry validates all fields of a masternode entry
func (a *App) validateMasternodeEntry(entry MasternodeConfigEntry) error {
	// Validate alias: non-empty, alphanumeric + underscore
	if entry.Alias == "" {
		return fmt.Errorf("alias cannot be empty")
	}
	aliasRegex := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	if !aliasRegex.MatchString(entry.Alias) {
		return fmt.Errorf("alias must contain only letters, numbers, and underscores")
	}

	// Validate IP:Port format
	if entry.IP == "" {
		return fmt.Errorf("IP address cannot be empty")
	}
	if !strings.Contains(entry.IP, ":") {
		return fmt.Errorf("IP must include port (format: IP:Port)")
	}

	// Validate IP:Port format
	parts := strings.Split(entry.IP, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid IP:Port format")
	}

	// Validate IP address format
	ipAddr := parts[0]
	if net.ParseIP(ipAddr) == nil {
		return fmt.Errorf("invalid IP address format: %s", ipAddr)
	}

	// Validate port is 37817 on mainnet
	if err := masternode.ValidatePort(mustAtoi(parts[1]), true); err != nil {
		return err
	}

	// Validate private key format (WIF)
	if entry.PrivateKey == "" {
		return fmt.Errorf("private key cannot be empty")
	}
	// Try to decode to verify it's valid WIF
	if _, err := crypto.DecodeWIF(entry.PrivateKey); err != nil {
		return fmt.Errorf("invalid private key format: must be valid WIF")
	}

	// Validate transaction hash (64 hex characters)
	if len(entry.TxHash) != 64 {
		return fmt.Errorf("invalid transaction hash: must be 64 hex characters")
	}
	txHashRegex := regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	if !txHashRegex.MatchString(entry.TxHash) {
		return fmt.Errorf("invalid transaction hash: must be hexadecimal")
	}

	// Validate output index
	if entry.OutputIndex < 0 {
		return fmt.Errorf("output index cannot be negative")
	}

	return nil
}

// mustAtoi converts string to int, returns 0 on error
func mustAtoi(s string) int {
	var result int
	fmt.Sscanf(s, "%d", &result)
	return result
}

// getTierName returns the tier name for a given collateral amount in satoshis
func getTierName(amountSatoshis int64) string {
	// Tier collateral amounts (from masternode/types.go constants)
	const (
		TierBronzeCollateral   = 1_000_000 * 1e8   // 1M TWINS
		TierSilverCollateral   = 5_000_000 * 1e8   // 5M TWINS
		TierGoldCollateral     = 20_000_000 * 1e8  // 20M TWINS
		TierPlatinumCollateral = 100_000_000 * 1e8 // 100M TWINS
	)

	switch amountSatoshis {
	case TierPlatinumCollateral:
		return "Platinum"
	case TierGoldCollateral:
		return "Gold"
	case TierSilverCollateral:
		return "Silver"
	case TierBronzeCollateral:
		return "Bronze"
	default:
		return ""
	}
}

// ==========================================
// Masternode Collateral UTXO Locking
// ==========================================

// MasternodeCollateralInfo contains information about a masternode collateral check
type MasternodeCollateralInfo struct {
	IsCollateral bool   `json:"isCollateral"`
	Alias        string `json:"alias,omitempty"`
}

// GetMasternodeCollateralOutpoints returns all collateral outpoints from masternode.conf
// Used for locking collateral UTXOs to prevent accidental spending
func (a *App) GetMasternodeCollateralOutpoints() []core.OutPoint {
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	a.componentsMu.RUnlock()

	if confFile == nil {
		return []core.OutPoint{}
	}

	entries := confFile.GetEntries()
	outpoints := make([]core.OutPoint, 0, len(entries))

	for _, entry := range entries {
		outpoints = append(outpoints, core.OutPoint{
			TxID: entry.TxHash.String(),
			Vout: entry.OutputIndex,
		})
	}

	return outpoints
}

// ==========================================
// Payment Stats Methods
// ==========================================

// PaymentStatsEntry represents a single masternode's payment statistics for the frontend
type PaymentStatsEntry struct {
	Address      string  `json:"address"`      // TWINS address
	Tier         string  `json:"tier"`         // bronze, silver, gold, platinum, or ""
	PaymentCount int64   `json:"paymentCount"` // Total payments received
	TotalPaid    float64 `json:"totalPaid"`    // Total paid in TWINS
	LastPaidTime string  `json:"lastPaidTime"` // ISO timestamp of last payment
	LatestTxID   string  `json:"latestTxID"`   // Transaction ID of latest payment
}

// PaymentStatsFilter contains sorting and pagination parameters for GetPaymentStats
type PaymentStatsFilter struct {
	SortColumn    string `json:"sortColumn"`    // tier, paymentCount, totalPaid, lastPaidTime
	SortDirection string `json:"sortDirection"` // asc or desc
	Page          int    `json:"page"`          // 1-based page number
	PageSize      int    `json:"pageSize"`      // items per page (10, 25, 50, 100)
}

// PaymentStatsResponse contains summary and per-masternode payment data with pagination metadata
type PaymentStatsResponse struct {
	TotalPaid         float64             `json:"totalPaid"`         // Total paid across all MNs in TWINS
	TotalPayments     int64               `json:"totalPayments"`     // Total payment count
	UniquePaymentAddresses int            `json:"uniquePaymentAddresses"` // Number of unique payment addresses (not necessarily unique masternodes)
	LowestBlock       uint32              `json:"lowestBlock"`       // Lowest scanned block height
	HighestBlock      uint32              `json:"highestBlock"`      // Highest scanned block height
	Entries           []PaymentStatsEntry `json:"entries"`
	TotalEntries      int                 `json:"totalEntries"` // Total entries before pagination
	TotalPages        int                 `json:"totalPages"`   // Total number of pages
	CurrentPage       int                 `json:"currentPage"`  // Current 1-based page number
	PageSize          int                 `json:"pageSize"`     // Items per page
}

// paymentStatsSortEntries sorts entries by the given column and direction.
func paymentStatsSortEntries(entries []PaymentStatsEntry, column, direction string) {
	asc := direction != "desc"

	sort.SliceStable(entries, func(i, j int) bool {
		var less bool
		switch column {
		case "tier":
			less = entries[i].Tier < entries[j].Tier
		case "paymentCount":
			less = entries[i].PaymentCount < entries[j].PaymentCount
		case "totalPaid":
			less = entries[i].TotalPaid < entries[j].TotalPaid
		case "lastPaidTime":
			less = entries[i].LastPaidTime < entries[j].LastPaidTime
		default:
			// Default sort by totalPaid descending
			less = entries[i].TotalPaid < entries[j].TotalPaid
			return !less // default desc
		}
		if asc {
			return less
		}
		return !less
	})
}

// GetPaymentStats returns masternode payment statistics from the in-memory PaymentTracker.
// Decodes scriptPubKeys to TWINS addresses, cross-references against known masternodes for tier,
// then applies server-side sorting and pagination per the filter parameters.
func (a *App) GetPaymentStats(filter PaymentStatsFilter) (*PaymentStatsResponse, error) {
	a.componentsMu.RLock()
	node := a.node
	client := a.coreClient
	a.componentsMu.RUnlock()

	emptyResp := &PaymentStatsResponse{
		Entries:     []PaymentStatsEntry{},
		CurrentPage: 1,
		PageSize:    filter.PageSize,
		TotalPages:  1,
	}

	if node == nil || node.PaymentTracker == nil {
		return emptyResp, nil
	}

	// Resolve the active network name once so address prefixes match the running chain
	// (mainnet "W…", testnet/regtest "m…"/"n…"). Empty string falls back to mainnet.
	networkName := ""
	if node.ChainParams != nil {
		networkName = node.ChainParams.Name
	}

	allStats := node.PaymentTracker.GetAllStats()
	if len(allStats) == 0 {
		return emptyResp, nil
	}

	// Build reverse map: payment address → tier from known masternodes
	addrToTier := make(map[string]string)
	if client != nil {
		if mnList, err := client.MasternodeList(""); err == nil {
			for _, mn := range mnList {
				if mn.PaymentAddress != "" && mn.Tier != "" {
					addrToTier[mn.PaymentAddress] = strings.ToLower(mn.Tier)
				}
			}
		}
	}

	allEntries := make([]PaymentStatsEntry, 0, len(allStats))
	var globalLowest, globalHighest uint32
	var totalPaidSatoshis int64

	for hexScript, stats := range allStats {
		// Decode hex scriptPubKey to bytes
		scriptBytes, err := hex.DecodeString(hexScript)
		if err != nil {
			continue
		}

		// Extract TWINS address from scriptPubKey using the active network's prefix
		address := extractAddressFromScriptPubKey(scriptBytes, networkName)
		if address == "" {
			continue
		}

		// Convert satoshis to TWINS
		totalPaidTWINS := float64(stats.TotalPaid) / 1e8

		// Format last paid time as ISO 8601
		var lastPaidStr string
		if !stats.LastPaid.IsZero() {
			lastPaidStr = stats.LastPaid.UTC().Format(time.RFC3339)
		}

		// Cross-reference for tier
		tier := addrToTier[address]

		entry := PaymentStatsEntry{
			Address:      address,
			Tier:         tier,
			PaymentCount: stats.PaymentCount,
			TotalPaid:    totalPaidTWINS,
			LastPaidTime: lastPaidStr,
			LatestTxID:   stats.LatestTxID,
		}
		allEntries = append(allEntries, entry)

		// Aggregate summary (accumulate satoshis to avoid float precision loss)
		totalPaidSatoshis += stats.TotalPaid

		// Track global block range
		if globalLowest == 0 || stats.LowestBlock < globalLowest {
			globalLowest = stats.LowestBlock
		}
		if stats.HighestBlock > globalHighest {
			globalHighest = stats.HighestBlock
		}
	}

	// Sort entries
	paymentStatsSortEntries(allEntries, filter.SortColumn, filter.SortDirection)

	// Calculate pagination
	totalEntries := len(allEntries)
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 10
	} else if pageSize > 100 {
		pageSize = 100
	}
	totalPages := (totalEntries + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	// Slice to requested page
	start := (page - 1) * pageSize
	end := start + pageSize
	if end > totalEntries {
		end = totalEntries
	}
	pageEntries := allEntries[start:end]

	// Calculate totalPayments from all entries (not just the page)
	var totalPayments int64
	for _, e := range allEntries {
		totalPayments += e.PaymentCount
	}

	return &PaymentStatsResponse{
		TotalPaid:         float64(totalPaidSatoshis) / 1e8,
		TotalPayments:     totalPayments,
		UniquePaymentAddresses: totalEntries,
		LowestBlock:       globalLowest,
		HighestBlock:      globalHighest,
		Entries:           pageEntries,
		TotalEntries:      totalEntries,
		TotalPages:        totalPages,
		CurrentPage:       page,
		PageSize:          pageSize,
	}, nil
}

// extractAddressFromScriptPubKey decodes a scriptPubKey to a TWINS address string
// using the address prefix appropriate for networkName ("mainnet", "testnet", or "regtest").
// Supports P2PKH, P2PK, and P2SH. An empty or unknown networkName falls back to mainnet
// prefixes, matching the fallback pattern used by getDefaultPort in tools_handlers.go.
func extractAddressFromScriptPubKey(scriptBytes []byte, networkName string) string {
	scriptType, scriptHash := binary.AnalyzeScript(scriptBytes)

	var netID byte
	switch scriptType {
	case binary.ScriptTypeP2PKH, binary.ScriptTypeP2PK:
		netID = crypto.GetPubKeyHashNetworkID(networkName)
	case binary.ScriptTypeP2SH:
		netID = crypto.GetScriptHashNetworkID(networkName)
	default:
		return ""
	}

	addr, err := crypto.NewAddressFromHash(scriptHash[:], netID)
	if err != nil {
		return ""
	}
	return addr.String()
}

// IsMasternodeCollateral checks if a UTXO is used as masternode collateral
// Returns the collateral info including whether it's collateral and the associated alias
func (a *App) IsMasternodeCollateral(txid string, vout uint32) MasternodeCollateralInfo {
	a.componentsMu.RLock()
	confFile := a.masternodeConf
	a.componentsMu.RUnlock()

	if confFile == nil {
		return MasternodeCollateralInfo{IsCollateral: false}
	}

	entries := confFile.GetEntries()
	for _, entry := range entries {
		if entry.TxHash.String() == txid && entry.OutputIndex == vout {
			return MasternodeCollateralInfo{
				IsCollateral: true,
				Alias:        entry.Alias,
			}
		}
	}

	return MasternodeCollateralInfo{IsCollateral: false}
}
