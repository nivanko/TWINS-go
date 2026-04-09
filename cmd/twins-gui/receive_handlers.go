package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/twins-dev/twins-core/internal/gui/core"
	"github.com/twins-dev/twins-core/internal/gui/tests/mocks"
	"github.com/twins-dev/twins-core/internal/wallet"
)

// ==========================================
// Receive Page Methods
// ==========================================

// GetReceivingAddresses returns all receiving addresses for the wallet,
// including keypool addresses. We deliberately use GetAllReceivingAddresses
// here (not GetReceivingAddresses) so that addresses which have received
// funds via the keypool — but were never explicitly labeled by the user —
// still appear in the Receiving Addresses dialog. Without this, addresses
// with a non-zero balance can be invisible to the user even though
// GetAddressBalances reports balances for them. This matches the same
// reasoning already applied in GetCurrentReceivingAddress below.
func (a *App) GetReceivingAddresses() ([]core.ReceivingAddress, error) {
	// Try real wallet first
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w != nil {
		addresses := w.GetAllReceivingAddresses()
		result := make([]core.ReceivingAddress, 0, len(addresses))
		for _, addr := range addresses {
			result = append(result, walletAddressToCoreAddress(addr, w))
		}
		return result, nil
	}

	// Fall back to mock for development mode
	if a.coreClient == nil {
		return nil, fmt.Errorf("core client not initialized")
	}
	if mockClient, ok := a.coreClient.(*mocks.MockCoreClient); ok {
		return mockClient.GetReceivingAddresses()
	}

	return nil, fmt.Errorf("wallet not available")
}

// GenerateReceivingAddress generates a new receiving address with optional label
func (a *App) GenerateReceivingAddress(label string) (*core.ReceivingAddress, error) {
	// Try real wallet first
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w != nil {
		address, err := w.GetReceivingAddress(label)
		if err != nil {
			return nil, fmt.Errorf("failed to generate receiving address: %w", err)
		}

		// Get the full address info to get creation time
		addresses := w.GetReceivingAddresses()
		for _, addr := range addresses {
			if addr.Address == address {
				result := walletAddressToCoreAddress(addr, w)
				return &result, nil
			}
		}

		// Address was just created, return with current time
		result := core.ReceivingAddress{
			Address: address,
			Label:   label,
		}
		return &result, nil
	}

	// Fall back to mock for development mode
	if a.coreClient == nil {
		return nil, fmt.Errorf("core client not initialized")
	}
	if mockClient, ok := a.coreClient.(*mocks.MockCoreClient); ok {
		addr, err := mockClient.GenerateReceivingAddress(label)
		if err != nil {
			return nil, fmt.Errorf("failed to generate receiving address: %w", err)
		}
		return &addr, nil
	}

	return nil, fmt.Errorf("wallet not available")
}

// GetPaymentRequests returns all payment requests
func (a *App) GetPaymentRequests() ([]core.PaymentRequest, error) {
	// Try real wallet first
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w != nil {
		requests, err := w.GetAllPaymentRequests()
		if err != nil {
			return nil, fmt.Errorf("failed to get payment requests: %w", err)
		}

		result := make([]core.PaymentRequest, 0, len(requests))
		for _, pr := range requests {
			result = append(result, walletPaymentRequestToCoreRequest(pr))
		}
		return result, nil
	}

	// Fall back to mock for development mode
	if a.coreClient == nil {
		return nil, fmt.Errorf("core client not initialized")
	}
	if mockClient, ok := a.coreClient.(*mocks.MockCoreClient); ok {
		return mockClient.GetPaymentRequests()
	}

	return nil, fmt.Errorf("wallet not available")
}

// CreatePaymentRequest creates a new payment request
func (a *App) CreatePaymentRequest(address, label, message string, amount float64) (*core.PaymentRequest, error) {
	// Try real wallet first
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w != nil {
		pr, err := w.CreatePaymentRequest(address, label, message, amount)
		if err != nil {
			return nil, fmt.Errorf("failed to create payment request: %w", err)
		}

		result := walletPaymentRequestToCoreRequest(pr)
		return &result, nil
	}

	// Fall back to mock for development mode
	if a.coreClient == nil {
		return nil, fmt.Errorf("core client not initialized")
	}
	if mockClient, ok := a.coreClient.(*mocks.MockCoreClient); ok {
		request, err := mockClient.CreatePaymentRequest(address, label, message, amount)
		if err != nil {
			return nil, fmt.Errorf("failed to create payment request: %w", err)
		}
		return &request, nil
	}

	return nil, fmt.Errorf("wallet not available")
}

// RemovePaymentRequest removes a payment request by address and ID
// Note: Payment request IDs are per-address, not global, so both are needed
func (a *App) RemovePaymentRequest(address string, id int64) error {
	// Try real wallet first
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w != nil {
		return w.DeletePaymentRequest(address, id)
	}

	// Fall back to mock for development mode
	if a.coreClient == nil {
		return fmt.Errorf("core client not initialized")
	}
	if mockClient, ok := a.coreClient.(*mocks.MockCoreClient); ok {
		return mockClient.RemovePaymentRequest(address, id)
	}

	return fmt.Errorf("wallet not available")
}

// SetAddressLabel sets or updates the label for an address
// This is used by the Edit Label feature in the transactions context menu
func (a *App) SetAddressLabel(address string, label string) error {
	// Validate address is not empty (defensive check)
	if address == "" {
		return fmt.Errorf("cannot set label for empty address")
	}

	// Try real wallet first
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w != nil {
		if err := w.SetAddressLabel(address, label); err != nil {
			return fmt.Errorf("failed to set address label: %w", err)
		}
		return nil
	}

	// Fall back to mock for development mode
	if a.coreClient == nil {
		return fmt.Errorf("core client not initialized")
	}
	if mockClient, ok := a.coreClient.(*mocks.MockCoreClient); ok {
		return mockClient.SetAddressLabel(address, label)
	}

	return fmt.Errorf("wallet not available")
}

// GetAddressLabel returns the label for an address
func (a *App) GetAddressLabel(address string) (string, error) {
	// Try real wallet first
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w != nil {
		return w.GetAddressLabel(address), nil
	}

	// Fall back to mock for development mode
	if a.coreClient == nil {
		return "", fmt.Errorf("core client not initialized")
	}
	if mockClient, ok := a.coreClient.(*mocks.MockCoreClient); ok {
		return mockClient.GetAddressLabel(address)
	}

	return "", fmt.Errorf("wallet not available")
}

// GetCurrentReceivingAddress returns the most recent receiving address
func (a *App) GetCurrentReceivingAddress() (*core.ReceivingAddress, error) {
	// Try real wallet first
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w != nil {
		// Use GetAllReceivingAddresses to include keypool addresses
		// (GetReceivingAddresses only returns labeled/used ones)
		addresses := w.GetAllReceivingAddresses()
		if len(addresses) == 0 {
			// Generate a new address if none exist
			address, err := w.GetReceivingAddress("")
			if err != nil {
				return nil, fmt.Errorf("failed to get receiving address: %w", err)
			}
			result := core.ReceivingAddress{
				Address: address,
				Label:   "",
			}
			return &result, nil
		}

		// Return the most recently created address from the full pool
		var newest *wallet.Address
		for _, addr := range addresses {
			if newest == nil || addr.CreatedAt.After(newest.CreatedAt) {
				newest = addr
			}
		}

		if newest != nil {
			result := walletAddressToCoreAddress(newest, w)
			return &result, nil
		}
	}

	// Fall back to mock for development mode
	if a.coreClient == nil {
		return nil, fmt.Errorf("core client not initialized")
	}
	if mockClient, ok := a.coreClient.(*mocks.MockCoreClient); ok {
		addr, err := mockClient.GetCurrentReceivingAddress()
		if err != nil {
			return nil, fmt.Errorf("failed to get current receiving address: %w", err)
		}
		return &addr, nil
	}

	return nil, fmt.Errorf("wallet not available")
}

// GetAddressBalances returns the total balance for each wallet address in TWINS.
// Includes all unspent UTXOs regardless of lock state, maturity, or pending-spent status.
func (a *App) GetAddressBalances() (map[string]float64, error) {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w == nil {
		return nil, fmt.Errorf("wallet not available")
	}

	satoshiBalances := w.GetTotalBalancesByAddress()

	balances := make(map[string]float64, len(satoshiBalances))
	for addr, sats := range satoshiBalances {
		balances[addr] = float64(sats) / 1e8
	}
	return balances, nil
}

// ==========================================
// Paginated Receiving Addresses
// ==========================================

// validReceivingAddressPageSizes is the set of page sizes accepted by the
// paginated receiving address handlers. Mirrors TransactionFilter conventions.
var validReceivingAddressPageSizes = map[int]bool{25: true, 50: true, 100: true, 250: true}

// buildReceivingAddressRows materializes the full filtered set of receiving
// address rows for the wallet, applying HideZeroBalance and SearchText filters.
// The unfiltered total (TotalAll) is also returned. Caller is responsible for
// sorting and paginating the result.
//
// Enumeration always returns every wallet receiving address (labeled, used,
// and external keypool entries) — there is no "show only addresses with a
// payment request" mode anymore. The only optional filter is HideZeroBalance.
//
// This is the single source of truth for filter semantics, shared by both
// GetReceivingAddressesPage and ExportReceivingAddressesCSV so the page view
// and the CSV export always agree on which rows match the current filter.
func buildReceivingAddressRows(w *wallet.Wallet, filter core.ReceivingAddressFilter) (rows []core.ReceivingAddressRow, totalAll int, err error) {
	// 1) Enumerate all receiving addresses (labeled, used, and external keypool).
	//    GetAllReceivingAddresses (not GetReceivingAddresses) is intentional —
	//    same reasoning documented above for GetReceivingAddresses().
	addresses := w.GetAllReceivingAddresses()
	totalAll = len(addresses)

	// 2) Snapshot per-address balances in TWINS.
	satoshiBalances := w.GetTotalBalancesByAddress()
	balanceTWINS := func(addr string) float64 {
		return float64(satoshiBalances[addr]) / 1e8
	}

	// 3) Pre-compute lowercase search needle (only when search is non-empty).
	searchActive := strings.TrimSpace(filter.SearchText) != ""
	needle := strings.ToLower(strings.TrimSpace(filter.SearchText))

	rows = make([]core.ReceivingAddressRow, 0, len(addresses))
	for _, addr := range addresses {
		if addr == nil {
			continue
		}

		balance := balanceTWINS(addr.Address)

		// Resolve label using the same fallback as walletAddressToCoreAddress
		// so the paginated view matches what the legacy handler returns.
		label := addr.Label
		if label == "" {
			label = w.GetAddressLabel(addr.Address)
		}

		// Compute HasPaymentRequest. We treat any error from
		// GetPaymentRequestsForAddress as "no payment request" rather than
		// failing the whole page — the wallet DB can return errors for
		// addresses without destdata, and dropping the whole page over a
		// single noisy lookup would be a worse user experience.
		hasPaymentRequest := false
		if requests, prErr := w.GetPaymentRequestsForAddress(addr.Address); prErr == nil && len(requests) > 0 {
			hasPaymentRequest = true
		}

		// Filter: HideZeroBalance
		// When HideZeroBalance is true, drop rows whose balance is exactly 0.
		// (Floating-point comparison to 0 is safe here because balanceTWINS
		// originates from an integer satoshi value divided by 1e8 — a UTXO
		// of 1 satoshi yields 1e-8 which is != 0, so any genuinely
		// zero-balance row produces exactly 0.0.)
		if filter.HideZeroBalance && balance == 0 {
			continue
		}

		// Filter: Search text (case-insensitive substring on label OR address)
		if searchActive {
			haystack := strings.ToLower(label) + "\x00" + strings.ToLower(addr.Address)
			if !strings.Contains(haystack, needle) {
				continue
			}
		}

		rows = append(rows, core.ReceivingAddressRow{
			Address:           addr.Address,
			Label:             label,
			Balance:           balance,
			HasPaymentRequest: hasPaymentRequest,
			Created:           addr.CreatedAt,
		})
	}

	return rows, totalAll, nil
}

// sortReceivingAddressRows sorts rows in place by the requested column and
// direction. Sort is stable with a deterministic secondary key (address
// ascending) so two rows with identical primary keys always sort the same way.
func sortReceivingAddressRows(rows []core.ReceivingAddressRow, column, direction string) {
	asc := direction != "desc"

	// Default column: balance (matches frontend default).
	primary := func(i, j int) int {
		switch column {
		case "label":
			a := strings.ToLower(rows[i].Label)
			b := strings.ToLower(rows[j].Label)
			if a < b {
				return -1
			}
			if a > b {
				return 1
			}
			return 0
		case "balance":
			fallthrough
		default:
			if rows[i].Balance < rows[j].Balance {
				return -1
			}
			if rows[i].Balance > rows[j].Balance {
				return 1
			}
			return 0
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		cmp := primary(i, j)
		if cmp == 0 {
			// Deterministic secondary key: address ascending.
			return rows[i].Address < rows[j].Address
		}
		if asc {
			return cmp < 0
		}
		return cmp > 0
	})
}

// GetReceivingAddressesPage returns a paginated, filtered, sorted page of
// wallet receiving addresses. Mirrors the GetTransactionsPage pattern.
//
// PageSize is clamped to one of the allowed values (25/50/100/250), defaulting
// to 25 for unrecognized sizes. Page is clamped to [1, TotalPages] when out of
// range. The returned page may be empty when the filter excludes everything.
func (a *App) GetReceivingAddressesPage(filter core.ReceivingAddressFilter) (*core.ReceivingAddressPage, error) {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w == nil {
		return nil, fmt.Errorf("wallet not available")
	}

	// Validate / clamp pagination parameters
	pageSize := filter.PageSize
	if !validReceivingAddressPageSizes[pageSize] {
		pageSize = 25
	}
	page := filter.Page
	if page < 1 {
		page = 1
	}

	rows, totalAll, err := buildReceivingAddressRows(w, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to build receiving address rows: %w", err)
	}

	sortReceivingAddressRows(rows, filter.SortColumn, filter.SortDirection)

	total := len(rows)
	totalPages := 0
	if total > 0 {
		totalPages = int(math.Ceil(float64(total) / float64(pageSize)))
	}

	// Clamp page to last page when beyond total. When the result set is
	// empty we keep page=1 (matches transactions page behavior).
	if totalPages > 0 && page > totalPages {
		page = totalPages
	}

	// Slice the page window
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	pageRows := rows[start:end]

	return &core.ReceivingAddressPage{
		Addresses:   pageRows,
		Total:       total,
		TotalAll:    totalAll,
		Page:        page,
		PageSize:    pageSize,
		TotalPages:  totalPages,
	}, nil
}

// ExportReceivingAddressesCSV generates CSV content for the full filtered set
// of receiving addresses (ignoring pagination) and opens a native save dialog.
//
// Pagination fields on the filter are ignored — the export always covers every
// row matching the current HideZeroBalance / SearchText / sort settings,
// matching ExportFilteredTransactionsCSV's "export the filtered set, not just
// the current page" semantics.
//
// Returns (true, nil) on save, (false, nil) when the user cancels the native
// dialog, or (false, error) on validation/IO failure.
func (a *App) ExportReceivingAddressesCSV(filter core.ReceivingAddressFilter) (bool, error) {
	a.componentsMu.RLock()
	w := a.wallet
	a.componentsMu.RUnlock()

	if w == nil {
		return false, fmt.Errorf("wallet not available")
	}

	rows, _, err := buildReceivingAddressRows(w, filter)
	if err != nil {
		return false, fmt.Errorf("failed to build receiving address rows: %w", err)
	}

	sortReceivingAddressRows(rows, filter.SortColumn, filter.SortDirection)

	// Build CSV content with formula injection protection.
	// Header columns: Label, Address, Balance (TWINS), Has Payment Request
	var sb strings.Builder
	sb.WriteString("\ufeff") // UTF-8 BOM for Excel compatibility
	sb.WriteString("Label,Address,Balance (TWINS),Has Payment Request\n")
	for _, r := range rows {
		hasReq := "no"
		if r.HasPaymentRequest {
			hasReq = "yes"
		}
		// Format balance with 8 decimal places (TWINS native precision)
		balanceStr := strconv.FormatFloat(r.Balance, 'f', 8, 64)

		sb.WriteString(csvEscapeReceiving(r.Label))
		sb.WriteByte(',')
		sb.WriteString(csvEscapeReceiving(r.Address))
		sb.WriteByte(',')
		sb.WriteString(csvEscapeReceiving(balanceStr))
		sb.WriteByte(',')
		sb.WriteString(csvEscapeReceiving(hasReq))
		sb.WriteByte('\n')
	}

	return a.SaveCSVFile(sb.String(), "receiving_addresses.csv", "Export Addresses")
}

// csvEscapeReceiving escapes a CSV field with formula-injection protection,
// matching the convention used elsewhere in this package (e.g.
// `internal/gui/core/go_client.go:csvEscape`):
//   - Prefix `=`, `+`, `-`, `@` with a single quote to neutralize formulas
//   - Replace control characters with spaces
//   - Wrap the field in double quotes and double any embedded quotes
func csvEscapeReceiving(value string) string {
	if value == "" {
		return `""`
	}

	// Formula injection guard
	if strings.IndexAny(value[:1], "=+-@") == 0 {
		value = "'" + value
	}

	// Replace control characters with spaces
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\t', '\n', '\r':
			b.WriteRune(' ')
		case '"':
			b.WriteString(`""`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ==========================================
// Type Conversion Helpers
// ==========================================

// walletAddressToCoreAddress converts a wallet.Address to core.ReceivingAddress
func walletAddressToCoreAddress(addr *wallet.Address, w *wallet.Wallet) core.ReceivingAddress {
	label := addr.Label
	// If no label on address, try to get it from the address book
	if label == "" && w != nil {
		label = w.GetAddressLabel(addr.Address)
	}

	return core.ReceivingAddress{
		Address: addr.Address,
		Label:   label,
		Created: addr.CreatedAt,
	}
}

// walletPaymentRequestToCoreRequest converts a wallet.PaymentRequest to core.PaymentRequest
func walletPaymentRequestToCoreRequest(pr *wallet.PaymentRequest) core.PaymentRequest {
	return core.PaymentRequest{
		ID:      pr.ID,
		Date:    pr.Date,
		Label:   pr.Label,
		Address: pr.Address,
		Message: pr.Message,
		Amount:  pr.Amount,
	}
}
