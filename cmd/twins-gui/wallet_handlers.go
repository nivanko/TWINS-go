package main

import (
	"fmt"
	"github.com/twins-dev/twins-core/internal/gui/core"
)

// ==========================================
// Wallet Operations
// ==========================================

// GetBalance returns wallet balance information from the core client
func (a *App) GetBalance() (*core.Balance, error) {
	if a.coreClient == nil {
		return nil, fmt.Errorf("core client not initialized")
	}
	balance, err := a.coreClient.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}
	return &balance, nil
}

// GetBlockchainInfo returns blockchain information including sync status
func (a *App) GetBlockchainInfo() (*core.BlockchainInfo, error) {
	if a.coreClient == nil {
		return nil, fmt.Errorf("core client not initialized")
	}
	info, err := a.coreClient.GetBlockchainInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get blockchain info: %w", err)
	}
	return &info, nil
}

// ValidateAddress validates a TWINS address and returns detailed information
func (a *App) ValidateAddress(address string) (*core.AddressValidation, error) {
	if a.coreClient == nil {
		return nil, fmt.Errorf("core client not initialized")
	}
	validation, err := a.coreClient.ValidateAddress(address)
	if err != nil {
		return nil, fmt.Errorf("failed to validate address: %w", err)
	}
	return &validation, nil
}

// ==========================================
// Network Operations
// ==========================================

// GetNetworkInfo returns network information including peer count
func (a *App) GetNetworkInfo() (*core.NetworkInfo, error) {
	if a.coreClient == nil {
		return nil, fmt.Errorf("core client not initialized")
	}
	info, err := a.coreClient.GetNetworkInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get network info: %w", err)
	}
	return &info, nil
}

// ==========================================
// Staking Operations
// ==========================================

// GetStakingInfo returns staking status and statistics
func (a *App) GetStakingInfo() (*core.StakingInfo, error) {
	if a.coreClient == nil {
		return nil, fmt.Errorf("core client not initialized")
	}
	info, err := a.coreClient.GetStakingInfo()
	if err != nil {
		return nil, fmt.Errorf("failed to get staking info: %w", err)
	}
	return &info, nil
}
