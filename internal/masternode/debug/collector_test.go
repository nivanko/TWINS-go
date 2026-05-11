package debug

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCollectorEmit(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3) // 1MB max

	// Emit when disabled should be a no-op
	c.Emit(Event{Type: "test", Category: CategorySync, Summary: "should not appear"})

	// Enable and emit
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	c.Emit(Event{
		Type:     TypeSyncStateChange,
		Category: CategorySync,
		Source:   "local",
		Summary:  "switched to MasternodeListSyncing",
	})
	c.EmitBroadcast(TypeBroadcastReceived, "192.168.1.1:37817", "received MNB", map[string]any{
		"outpoint": "abc:0",
		"tier":     "Gold",
	})

	// Verify stats (3 total: session_start + sync + broadcast)
	stats := c.Stats()
	if stats.Total != 3 {
		t.Errorf("expected 3 events, got %d", stats.Total)
	}
	if stats.ByCategory[CategorySession] != 1 {
		t.Errorf("expected 1 session event, got %d", stats.ByCategory[CategorySession])
	}
	if stats.ByCategory[CategorySync] != 1 {
		t.Errorf("expected 1 sync event, got %d", stats.ByCategory[CategorySync])
	}
	if stats.ByCategory[CategoryBroadcast] != 1 {
		t.Errorf("expected 1 broadcast event, got %d", stats.ByCategory[CategoryBroadcast])
	}
	if !stats.Enabled {
		t.Error("expected enabled=true")
	}

	// Verify file has content
	if stats.FileSize == 0 {
		t.Error("expected non-zero file size")
	}
}

func TestCollectorQuery(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	now := time.Now()

	// Emit events with different categories
	c.Emit(Event{Timestamp: now.Add(-3 * time.Second), Type: TypeSyncStateChange, Category: CategorySync, Source: "local", Summary: "sync started"})
	c.Emit(Event{Timestamp: now.Add(-2 * time.Second), Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "192.168.1.1:37817", Summary: "received MNB for gold tier"})
	c.Emit(Event{Timestamp: now.Add(-1 * time.Second), Type: TypePingStageResult, Category: CategoryPing, Source: "10.0.0.1:37817", Summary: "ping accepted"})

	// Query all (4 total: session_start + 3 emitted)
	res, err := c.Query(Filter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(res.Events))
	}

	// Query by category
	res, err = c.Query(Filter{Category: CategoryBroadcast})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Events) != 1 {
		t.Errorf("expected 1 broadcast event, got %d", len(res.Events))
	}
	if res.Events[0].Summary != "received MNB for gold tier" {
		t.Errorf("unexpected summary: %s", res.Events[0].Summary)
	}

	// Query by type
	res, err = c.Query(Filter{Type: TypePingStageResult})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Events) != 1 {
		t.Errorf("expected 1 ping event, got %d", len(res.Events))
	}

	// Query by source: session_start (local) + sync event (local) = 2
	res, err = c.Query(Filter{Source: "local"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Events) != 2 {
		t.Errorf("expected 2 local events (session_start + sync), got %d", len(res.Events))
	}

	// Query by text search (case-insensitive)
	res, err = c.Query(Filter{Search: "GOLD TIER"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Events) != 1 {
		t.Errorf("expected 1 event matching 'GOLD TIER', got %d", len(res.Events))
	}

	// Query with limit
	res, err = c.Query(Filter{Limit: 1})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Events) != 1 {
		t.Errorf("expected 1 event with limit, got %d", len(res.Events))
	}
	// TotalMatched should reflect the full unfiltered set even when limited
	if res.TotalMatched != 4 {
		t.Errorf("expected TotalMatched=4, got %d", res.TotalMatched)
	}
	if !res.Truncated {
		t.Errorf("expected Truncated=true when limit < TotalMatched")
	}
}

func TestCollectorQueryNewest(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	now := time.Now()

	// Emit 10 events with sequential timestamps
	for i := 0; i < 10; i++ {
		c.Emit(Event{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Type:      TypeSyncStateChange,
			Category:  CategorySync,
			Source:    "local",
			Summary:   fmt.Sprintf("event-%d", i),
		})
	}

	// Query with Newest=false and Limit=3, filtered to sync category to skip session_start
	res, err := c.Query(Filter{Limit: 3, Newest: false, Category: CategorySync})
	if err != nil {
		t.Fatalf("Query (oldest) failed: %v", err)
	}
	if len(res.Events) != 3 {
		t.Fatalf("expected 3 oldest sync events, got %d", len(res.Events))
	}
	if res.Events[0].Summary != "event-0" {
		t.Errorf("expected oldest event-0, got %s", res.Events[0].Summary)
	}
	if res.Events[2].Summary != "event-2" {
		t.Errorf("expected oldest event-2, got %s", res.Events[2].Summary)
	}

	// Query with Newest=true and Limit=3, filtered to sync category.
	// Results are returned newest-first (reverse chronological order).
	res, err = c.Query(Filter{Limit: 3, Newest: true, Category: CategorySync})
	if err != nil {
		t.Fatalf("Query (newest) failed: %v", err)
	}
	if len(res.Events) != 3 {
		t.Fatalf("expected 3 newest sync events, got %d", len(res.Events))
	}
	if res.Events[0].Summary != "event-9" {
		t.Errorf("expected newest-first event-9, got %s", res.Events[0].Summary)
	}
	if res.Events[2].Summary != "event-7" {
		t.Errorf("expected newest-first event-7 at end, got %s", res.Events[2].Summary)
	}

	// Query with Newest=true and Limit=0 uses the default cap (defaultQueryLimit=1000).
	// With 11 total events (< 1000) all are returned.
	res, err = c.Query(Filter{Newest: true})
	if err != nil {
		t.Fatalf("Query (newest, default limit) failed: %v", err)
	}
	if len(res.Events) != 11 {
		t.Errorf("expected 11 events (all under default limit), got %d", len(res.Events))
	}
}

func TestCollectorRotation(t *testing.T) {
	dir := t.TempDir()
	// Very small max size to trigger rotation quickly
	c := NewCollector(dir, 0, 3) // Will use default 50MB
	// Override to tiny size for testing
	c.maxSizeBytes = 500 // 500 bytes

	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	// Emit enough events to trigger rotation
	for i := 0; i < 20; i++ {
		c.Emit(Event{
			Type:     TypeSyncStateChange,
			Category: CategorySync,
			Source:   "local",
			Summary:  "sync state change event with enough text to fill the buffer quickly",
		})
	}

	// Check that rotated files exist
	base := filepath.Join(dir, "mn-debug")
	if _, err := os.Stat(base + ".1.jsonl"); os.IsNotExist(err) {
		t.Error("expected rotated file .1.jsonl to exist")
	}

	// Current file should exist and be smaller than max
	info, err := os.Stat(filepath.Join(dir, defaultFilename))
	if err != nil {
		t.Fatalf("current file should exist: %v", err)
	}
	if info.Size() > 500 {
		t.Errorf("current file too large after rotation: %d bytes", info.Size())
	}
}

func TestCollectorMaxFiles(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 0, 2) // max 2 rotated files
	c.maxSizeBytes = 200

	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	// Emit many events to trigger multiple rotations
	for i := 0; i < 50; i++ {
		c.Emit(Event{
			Type:     TypeSyncStateChange,
			Category: CategorySync,
			Source:   "local",
			Summary:  "rotation test event with some padding text here",
		})
	}

	base := filepath.Join(dir, "mn-debug")

	// Files .1 and .2 should exist (maxFiles=2)
	if _, err := os.Stat(base + ".1.jsonl"); os.IsNotExist(err) {
		t.Error("expected .1.jsonl to exist")
	}
	if _, err := os.Stat(base + ".2.jsonl"); os.IsNotExist(err) {
		t.Error("expected .2.jsonl to exist")
	}

	// File .3 should NOT exist (exceeds maxFiles)
	if _, err := os.Stat(base + ".3.jsonl"); !os.IsNotExist(err) {
		t.Error("expected .3.jsonl to NOT exist (maxFiles=2)")
	}
}

func TestCollectorClear(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	c.Emit(Event{Type: TypeSyncStateChange, Category: CategorySync, Source: "local", Summary: "test"})
	c.Emit(Event{Type: TypePingStageResult, Category: CategoryPing, Source: "local", Summary: "test2"})

	if err := c.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Stats should be reset
	stats := c.Stats()
	if stats.Total != 0 {
		t.Errorf("expected 0 total after clear, got %d", stats.Total)
	}
	if stats.FileSize != 0 {
		t.Errorf("expected 0 file size after clear, got %d", stats.FileSize)
	}

	// Query should return empty
	res, err := c.Query(Filter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Events) != 0 {
		t.Errorf("expected 0 events after clear, got %d", len(res.Events))
	}

	// Should still be enabled and accepting events
	c.Emit(Event{Type: TypeSyncStateChange, Category: CategorySync, Source: "local", Summary: "after clear"})
	stats = c.Stats()
	if stats.Total != 1 {
		t.Errorf("expected 1 event after clear+emit, got %d", stats.Total)
	}
}

func TestCollectorDisabledEmit(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)

	// Not enabled - all emit methods should be no-ops
	c.Emit(Event{Type: "test", Category: "test"})
	c.EmitSync("test", "local", "test", nil)
	c.EmitBroadcast("test", "local", "test", nil)
	c.EmitPing("test", "local", "test", nil)
	c.EmitStatus("test", "local", "test", nil)
	c.EmitWinner("test", "local", "test", nil)
	c.EmitActive("test", "local", "test", nil)
	c.EmitNetwork("test", "local", "test", nil)

	stats := c.Stats()
	if stats.Total != 0 {
		t.Errorf("expected 0 events when disabled, got %d", stats.Total)
	}
	if stats.Enabled {
		t.Error("expected enabled=false")
	}

	// File should not exist
	if _, err := os.Stat(filepath.Join(dir, defaultFilename)); !os.IsNotExist(err) {
		t.Error("expected no file when never enabled")
	}
}

func TestCollectorDoubleEnable(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)

	// First Enable — should write one session_start and open the file.
	if err := c.Enable(); err != nil {
		t.Fatalf("first Enable failed: %v", err)
	}
	// Second Enable without Disable — early-return path (file still open).
	// Must be a no-op: no second session_start, enabled flag stays true.
	if err := c.Enable(); err != nil {
		t.Fatalf("second Enable failed: %v", err)
	}
	c.Emit(Event{Type: TypeSyncStateChange, Category: CategorySync, Source: "local", Summary: "after double enable"})
	c.Close()

	c2 := NewCollector(dir, 1, 3)
	res, err := c2.Query(Filter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	// Exactly 2 events: one session_start (first Enable only) + one user event.
	// The second Enable must NOT emit a second session_start.
	if len(res.Events) != 2 {
		t.Errorf("expected 2 events (1 session_start + 1 user), got %d", len(res.Events))
	}
	if res.Events[0].Type != TypeSessionStart {
		t.Errorf("expected first event to be session_start, got %s", res.Events[0].Type)
	}
	sessionCount := 0
	for _, e := range res.Events {
		if e.Type == TypeSessionStart {
			sessionCount++
		}
	}
	if sessionCount != 1 {
		t.Errorf("expected exactly 1 session_start event, got %d", sessionCount)
	}
}

func TestCollectorEnableDisableToggle(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)

	// Enable
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	if !c.IsEnabled() {
		t.Error("expected IsEnabled=true after Enable")
	}

	c.Emit(Event{Type: "test", Category: CategorySync, Source: "local", Summary: "while enabled"})

	// Disable
	c.Disable()
	if c.IsEnabled() {
		t.Error("expected IsEnabled=false after Disable")
	}

	c.Emit(Event{Type: "test", Category: CategorySync, Source: "local", Summary: "while disabled"})

	// Re-enable
	if err := c.Enable(); err != nil {
		t.Fatalf("Re-Enable failed: %v", err)
	}
	c.Emit(Event{Type: "test", Category: CategorySync, Source: "local", Summary: "after re-enable"})
	c.Close()

	// Should have 4 events: session_start (1st Enable) + "while enabled" +
	// session_start (re-Enable) + "after re-enable". The disabled emit is skipped.
	c2 := NewCollector(dir, 1, 3)
	res, err := c2.Query(Filter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Events) != 4 {
		t.Errorf("expected 4 events (2 session_start + 2 user, disabled emit skipped), got %d", len(res.Events))
	}
}

func TestCollectorEventPayload(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	payload := map[string]any{
		"outpoint": "abc123:0",
		"tier":     "Gold",
		"protocol": 70928,
	}
	c.EmitBroadcast(TypeBroadcastAccepted, "10.0.0.1:37817", "accepted MNB", payload)

	res, err := c.Query(Filter{Category: CategoryBroadcast})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(res.Events))
	}

	// Verify payload round-trips
	var p map[string]any
	if err := json.Unmarshal(res.Events[0].Payload, &p); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if p["tier"] != "Gold" {
		t.Errorf("expected tier=Gold, got %v", p["tier"])
	}
	if p["outpoint"] != "abc123:0" {
		t.Errorf("expected outpoint=abc123:0, got %v", p["outpoint"])
	}
}

func TestCollectorQueryNoFile(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)

	// Query without ever enabling (no file exists)
	res, err := c.Query(Filter{})
	if err != nil {
		t.Fatalf("Query should not error on missing file: %v", err)
	}
	if res == nil || len(res.Events) != 0 {
		t.Errorf("expected empty result for missing file, got %v", res)
	}
}

func TestCollectorSummary(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)

	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	// Emit a variety of events to exercise all summary aggregation paths
	c.EmitBroadcast(TypeBroadcastReceived, "10.0.0.1:37817", "received MNB", map[string]any{
		"outpoint": "aaa:0", "tier": "Gold",
	})
	c.EmitBroadcast(TypeBroadcastAccepted, "10.0.0.1:37817", "accepted MNB", map[string]any{
		"outpoint": "aaa:0", "tier": "Gold",
	})
	c.EmitBroadcast(TypeBroadcastReceived, "10.0.0.2:37817", "received MNB", map[string]any{
		"outpoint": "bbb:0", "tier": "Silver",
	})
	c.EmitBroadcast(TypeBroadcastRejected, "10.0.0.2:37817", "rejected MNB", map[string]any{
		"outpoint": "bbb:0", "reason": "bad signature",
	})
	c.EmitBroadcast(TypeBroadcastDedup, "10.0.0.1:37817", "dedup MNB", map[string]any{
		"outpoint": "aaa:0",
	})

	c.EmitPing("ping_received", "10.0.0.1:37817", "ping received", nil)
	c.EmitPing("ping_accepted", "10.0.0.1:37817", "ping accepted", nil)

	c.EmitStatus(TypeStatusUpdate, "aaa:0", "PreEnabled -> Enabled", map[string]any{
		"prev_status": "PreEnabled", "new_status": "Enabled",
	})

	c.EmitActive("active_ping_sent", "local", "ping sent ok", map[string]any{"success": true})
	c.EmitActive("active_ping_sent", "local", "ping sent fail", map[string]any{"success": false})
	c.EmitActive("active_state_change", "local", "state changed", map[string]any{
		"prev_status": "Initial", "new_status": "Started",
	})

	c.EmitNetwork("network_mnb_received", "10.0.0.3:37817", "MNB from net", map[string]any{"outpoint": "ccc:0"})
	c.EmitNetwork("dseg_request", "10.0.0.3:37817", "DSEG request", map[string]any{"payload_size": 100})
	c.EmitNetwork("dseg_response", "10.0.0.3:37817", "DSEG response", map[string]any{"sent_count": float64(500)})

	c.EmitSync(TypeSyncStateChange, "local", "sync state change", map[string]any{
		"prev_state": "Initial", "new_state": "MasternodeListSyncing",
	})

	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	// Overview
	if summary.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", summary.SessionCount)
	}
	if summary.FirstEvent == "" || summary.LastEvent == "" {
		t.Error("Expected non-empty FirstEvent/LastEvent")
	}

	// Broadcast
	if summary.BroadcastReceived != 2 {
		t.Errorf("BroadcastReceived = %d, want 2", summary.BroadcastReceived)
	}
	if summary.BroadcastAccepted != 1 {
		t.Errorf("BroadcastAccepted = %d, want 1", summary.BroadcastAccepted)
	}
	if summary.BroadcastRejected != 1 {
		t.Errorf("BroadcastRejected = %d, want 1", summary.BroadcastRejected)
	}
	if summary.BroadcastDedup != 1 {
		t.Errorf("BroadcastDedup = %d, want 1", summary.BroadcastDedup)
	}
	// After H-5 fix: only outpoints from TypeBroadcastAccepted events count.
	// aaa:0 has Accepted → counted. bbb:0 has only Received+Rejected → excluded.
	if summary.UniqueMasternodes != 1 {
		t.Errorf("UniqueMasternodes = %d, want 1 (only aaa:0 has Accepted)", summary.UniqueMasternodes)
	}
	// After H-4 fix: TierBreakdown counts unique MNs per tier (post-loop pass).
	// One unique MN (aaa:0) at Gold → TierBreakdown[Gold] = 1.
	if summary.TierBreakdown["Gold"] != 1 {
		t.Errorf("TierBreakdown[Gold] = %d, want 1", summary.TierBreakdown["Gold"])
	}
	if len(summary.RejectReasons) == 0 || summary.RejectReasons[0].Label != "bad signature" {
		t.Errorf("Expected 'bad signature' as top reject reason, got %v", summary.RejectReasons)
	}
	if len(summary.TopSources) == 0 {
		t.Error("Expected non-empty TopSources")
	}

	// Ping
	if summary.PingReceived != 1 {
		t.Errorf("PingReceived = %d, want 1", summary.PingReceived)
	}
	if summary.PingAccepted != 1 {
		t.Errorf("PingAccepted = %d, want 1", summary.PingAccepted)
	}

	// Active
	if summary.ActivePingsSent != 2 {
		t.Errorf("ActivePingsSent = %d, want 2", summary.ActivePingsSent)
	}
	if summary.ActivePingsSuccess != 1 {
		t.Errorf("ActivePingsSuccess = %d, want 1", summary.ActivePingsSuccess)
	}
	if summary.ActivePingsFailed != 1 {
		t.Errorf("ActivePingsFailed = %d, want 1", summary.ActivePingsFailed)
	}
	if len(summary.ActiveMNChanges) != 1 {
		t.Errorf("ActiveMNChanges length = %d, want 1", len(summary.ActiveMNChanges))
	}

	// Network
	if summary.NetworkMNBCount != 1 {
		t.Errorf("NetworkMNBCount = %d, want 1", summary.NetworkMNBCount)
	}
	if summary.DSEGRequests != 1 {
		t.Errorf("DSEGRequests = %d, want 1", summary.DSEGRequests)
	}
	if summary.DSEGResponses != 1 {
		t.Errorf("DSEGResponses = %d, want 1", summary.DSEGResponses)
	}
	if summary.AvgMNsServed != 500 {
		t.Errorf("AvgMNsServed = %f, want 500", summary.AvgMNsServed)
	}
	// After H-6 fix: peer counting includes CategoryBroadcast events.
	// Distinct IPs across all events: 10.0.0.1 (broadcast+ping), 10.0.0.2 (broadcast),
	// 10.0.0.3 (network) → 3 unique peers.
	if summary.UniquePeers != 3 {
		t.Errorf("UniquePeers = %d, want 3 (broadcast peers now included)", summary.UniquePeers)
	}

	// Sync
	if len(summary.SyncTransitions) != 1 {
		t.Errorf("SyncTransitions length = %d, want 1", len(summary.SyncTransitions))
	}

	// Status
	if len(summary.StatusChanges) != 1 {
		t.Errorf("StatusChanges length = %d, want 1", len(summary.StatusChanges))
	}
	if summary.StatusChanges[0].Label != "PreEnabled → Enabled" {
		t.Errorf("StatusChanges[0].Label = %q, want 'PreEnabled → Enabled'", summary.StatusChanges[0].Label)
	}

	// Accept rates
	if summary.AcceptRate <= 0 {
		t.Errorf("AcceptRate = %f, want > 0", summary.AcceptRate)
	}
}

// TestCollectorSummary_BroadcastSkipped verifies that TypeBroadcastSkipped events
// increment BroadcastSkipped and do NOT pollute RejectReasons (which is reserved
// for validation failures).
func TestCollectorSummary_BroadcastSkipped(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	c.EmitBroadcast(TypeBroadcastSkipped, "10.0.0.1:37817", "skip ibd_gate", map[string]any{
		"outpoint": "aaa:0", "reason": "ibd_gate",
	})
	c.EmitBroadcast(TypeBroadcastSkipped, "10.0.0.2:37817", "skip already_seen", map[string]any{
		"outpoint": "bbb:0", "reason": "already_seen",
	})

	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if summary.BroadcastSkipped != 2 {
		t.Errorf("BroadcastSkipped = %d, want 2", summary.BroadcastSkipped)
	}
	if summary.BroadcastRejected != 0 {
		t.Errorf("BroadcastRejected = %d, want 0 (skipped should not bleed into rejected)", summary.BroadcastRejected)
	}
	for _, r := range summary.RejectReasons {
		if r.Label == "ibd_gate" || r.Label == "already_seen" {
			t.Errorf("Skip reason %q leaked into RejectReasons", r.Label)
		}
	}
}

// TestCollectorSummary_PingRejected verifies that TypePingRejected events
// increment PingFailed and the reason is captured in RejectReasons.
func TestCollectorSummary_PingRejected(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	c.EmitPing(TypePingReceived, "10.0.0.1:37817", "ping received", nil)
	c.EmitPing(TypePingRejected, "10.0.0.1:37817", "ping rejected sig", map[string]any{
		"reason": "signature_invalid",
	})
	c.EmitPing(TypePingRejected, "10.0.0.1:37817", "ping rejected sigtime", map[string]any{
		"reason": "sigtime_too_old",
	})

	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if summary.PingReceived != 1 {
		t.Errorf("PingReceived = %d, want 1", summary.PingReceived)
	}
	if summary.PingFailed != 2 {
		t.Errorf("PingFailed = %d, want 2", summary.PingFailed)
	}
	if summary.PingSkipped != 0 {
		t.Errorf("PingSkipped = %d, want 0", summary.PingSkipped)
	}

	foundSig := false
	foundSigTime := false
	for _, r := range summary.RejectReasons {
		if r.Label == "signature_invalid" {
			foundSig = true
		}
		if r.Label == "sigtime_too_old" {
			foundSigTime = true
		}
	}
	if !foundSig {
		t.Error("Expected 'signature_invalid' in RejectReasons")
	}
	if !foundSigTime {
		t.Error("Expected 'sigtime_too_old' in RejectReasons")
	}
}

// TestCollectorSummary_PingSkipped verifies that TypePingSkipped events
// increment PingSkipped (not PingFailed) and do NOT pollute RejectReasons.
func TestCollectorSummary_PingSkipped(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	c.EmitPing(TypePingSkipped, "10.0.0.1:37817", "skip already_seen", map[string]any{
		"reason": "already_seen",
	})
	c.EmitPing(TypePingSkipped, "10.0.0.2:37817", "skip ibd_gate", map[string]any{
		"reason": "ibd_gate",
	})

	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if summary.PingSkipped != 2 {
		t.Errorf("PingSkipped = %d, want 2", summary.PingSkipped)
	}
	if summary.PingFailed != 0 {
		t.Errorf("PingFailed = %d, want 0 (skipped should not bleed into failed)", summary.PingFailed)
	}
	for _, r := range summary.RejectReasons {
		if r.Label == "already_seen" || r.Label == "ibd_gate" {
			t.Errorf("Skip reason %q leaked into RejectReasons", r.Label)
		}
	}
}

// TestCollectorSummary_MixedReasonTaxonomy verifies the full taxonomy across
// broadcasts and pings — rejected reasons feed RejectReasons; skipped reasons do not.
func TestCollectorSummary_MixedReasonTaxonomy(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	// Broadcasts: 2 received, 1 accepted, 1 rejected, 1 skipped
	c.EmitBroadcast(TypeBroadcastReceived, "10.0.0.1:37817", "received", map[string]any{"outpoint": "aaa:0"})
	c.EmitBroadcast(TypeBroadcastAccepted, "10.0.0.1:37817", "accepted", map[string]any{"outpoint": "aaa:0", "tier": "Gold"})
	c.EmitBroadcast(TypeBroadcastReceived, "10.0.0.2:37817", "received", map[string]any{"outpoint": "bbb:0"})
	c.EmitBroadcast(TypeBroadcastRejected, "10.0.0.2:37817", "rejected", map[string]any{"outpoint": "bbb:0", "reason": "bad_signature"})
	c.EmitBroadcast(TypeBroadcastSkipped, "10.0.0.3:37817", "skipped", map[string]any{"outpoint": "ccc:0", "reason": "ibd_gate"})

	// Pings: 2 received, 1 accepted, 1 rejected, 1 skipped
	c.EmitPing(TypePingReceived, "10.0.0.1:37817", "received", nil)
	c.EmitPing(TypePingAccepted, "10.0.0.1:37817", "accepted", nil)
	c.EmitPing(TypePingReceived, "10.0.0.2:37817", "received", nil)
	c.EmitPing(TypePingRejected, "10.0.0.2:37817", "rejected", map[string]any{"reason": "spacing_violation"})
	c.EmitPing(TypePingSkipped, "10.0.0.3:37817", "skipped", map[string]any{"reason": "already_seen"})

	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	// Broadcast counts
	if summary.BroadcastReceived != 2 {
		t.Errorf("BroadcastReceived = %d, want 2", summary.BroadcastReceived)
	}
	if summary.BroadcastAccepted != 1 {
		t.Errorf("BroadcastAccepted = %d, want 1", summary.BroadcastAccepted)
	}
	if summary.BroadcastRejected != 1 {
		t.Errorf("BroadcastRejected = %d, want 1", summary.BroadcastRejected)
	}
	if summary.BroadcastSkipped != 1 {
		t.Errorf("BroadcastSkipped = %d, want 1", summary.BroadcastSkipped)
	}

	// Ping counts
	if summary.PingReceived != 2 {
		t.Errorf("PingReceived = %d, want 2", summary.PingReceived)
	}
	if summary.PingAccepted != 1 {
		t.Errorf("PingAccepted = %d, want 1", summary.PingAccepted)
	}
	if summary.PingFailed != 1 {
		t.Errorf("PingFailed = %d, want 1", summary.PingFailed)
	}
	if summary.PingSkipped != 1 {
		t.Errorf("PingSkipped = %d, want 1", summary.PingSkipped)
	}

	// Reason taxonomy: rejected reasons in RejectReasons, skip reasons NOT
	rejectLabels := make(map[string]bool)
	for _, r := range summary.RejectReasons {
		rejectLabels[r.Label] = true
	}
	if !rejectLabels["bad_signature"] {
		t.Error("Expected 'bad_signature' in RejectReasons")
	}
	if !rejectLabels["spacing_violation"] {
		t.Error("Expected 'spacing_violation' in RejectReasons")
	}
	if rejectLabels["ibd_gate"] {
		t.Error("'ibd_gate' (skip reason) leaked into RejectReasons")
	}
	if rejectLabels["already_seen"] {
		t.Error("'already_seen' (skip reason) leaked into RejectReasons")
	}
}

// TestCollectorSummary_AcceptRateDenominator verifies that AcceptRate and
// PingAcceptRate use the validation-outcome denominator (Accepted + Rejected),
// not the inflated total that triple-counts received events.
//
// For each broadcast/ping the system emits a *Received event followed by exactly
// one outcome (*Accepted, *Rejected, *Dedup, or *Skipped). Summing Received +
// Accepted + Rejected double- or triple-counts the same logical event and
// artificially deflates the accept rate.
func TestCollectorSummary_AcceptRateDenominator(t *testing.T) {
	cases := []struct {
		name           string
		emit           func(c *Collector)
		wantAccept     float64
		wantPingAccept float64
	}{
		{
			name: "1 received + 1 accepted -> 100",
			emit: func(c *Collector) {
				c.EmitBroadcast(TypeBroadcastReceived, "10.0.0.1:37817", "r", map[string]any{"outpoint": "a:0"})
				c.EmitBroadcast(TypeBroadcastAccepted, "10.0.0.1:37817", "a", map[string]any{"outpoint": "a:0", "tier": "Gold"})
				c.EmitPing(TypePingReceived, "10.0.0.1:37817", "r", nil)
				c.EmitPing(TypePingAccepted, "10.0.0.1:37817", "a", nil)
			},
			wantAccept:     100.0,
			wantPingAccept: 100.0,
		},
		{
			name: "1 received + 1 rejected -> 0",
			emit: func(c *Collector) {
				c.EmitBroadcast(TypeBroadcastReceived, "10.0.0.1:37817", "r", map[string]any{"outpoint": "a:0"})
				c.EmitBroadcast(TypeBroadcastRejected, "10.0.0.1:37817", "x", map[string]any{"outpoint": "a:0", "reason": "bad"})
				c.EmitPing(TypePingReceived, "10.0.0.1:37817", "r", nil)
				c.EmitPing(TypePingRejected, "10.0.0.1:37817", "x", map[string]any{"reason": "bad"})
			},
			wantAccept:     0.0,
			wantPingAccept: 0.0,
		},
		{
			name: "2 accepted + 0 rejected (no received) -> 100",
			emit: func(c *Collector) {
				c.EmitBroadcast(TypeBroadcastAccepted, "10.0.0.1:37817", "a", map[string]any{"outpoint": "a:0"})
				c.EmitBroadcast(TypeBroadcastAccepted, "10.0.0.2:37817", "a", map[string]any{"outpoint": "b:0"})
				c.EmitPing(TypePingAccepted, "10.0.0.1:37817", "a", nil)
				c.EmitPing(TypePingAccepted, "10.0.0.2:37817", "a", nil)
			},
			wantAccept:     100.0,
			wantPingAccept: 100.0,
		},
		{
			name: "no events -> 0 (no division by zero)",
			emit: func(c *Collector) {
				// emit nothing
			},
			wantAccept:     0.0,
			wantPingAccept: 0.0,
		},
		{
			name: "3 accepted + 1 rejected -> 75 (skipped/dedup excluded)",
			emit: func(c *Collector) {
				c.EmitBroadcast(TypeBroadcastAccepted, "10.0.0.1:37817", "a", map[string]any{"outpoint": "a:0"})
				c.EmitBroadcast(TypeBroadcastAccepted, "10.0.0.2:37817", "a", map[string]any{"outpoint": "b:0"})
				c.EmitBroadcast(TypeBroadcastAccepted, "10.0.0.3:37817", "a", map[string]any{"outpoint": "c:0"})
				c.EmitBroadcast(TypeBroadcastRejected, "10.0.0.4:37817", "x", map[string]any{"outpoint": "d:0", "reason": "bad"})
				c.EmitBroadcast(TypeBroadcastSkipped, "10.0.0.5:37817", "s", map[string]any{"outpoint": "e:0", "reason": "ibd_gate"})
				c.EmitBroadcast(TypeBroadcastDedup, "10.0.0.6:37817", "d", map[string]any{"outpoint": "f:0"})
				c.EmitPing(TypePingAccepted, "10.0.0.1:37817", "a", nil)
				c.EmitPing(TypePingAccepted, "10.0.0.2:37817", "a", nil)
				c.EmitPing(TypePingAccepted, "10.0.0.3:37817", "a", nil)
				c.EmitPing(TypePingRejected, "10.0.0.4:37817", "x", map[string]any{"reason": "bad"})
				c.EmitPing(TypePingSkipped, "10.0.0.5:37817", "s", map[string]any{"reason": "ibd_gate"})
			},
			wantAccept:     75.0,
			wantPingAccept: 75.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			c := NewCollector(dir, 1, 3)
			if err := c.Enable(); err != nil {
				t.Fatalf("Enable failed: %v", err)
			}
			defer c.Close()

			tc.emit(c)

			summary, err := c.Summary()
			if err != nil {
				t.Fatalf("Summary() error: %v", err)
			}

			if summary.AcceptRate != tc.wantAccept {
				t.Errorf("AcceptRate = %v, want %v (Accepted=%d Rejected=%d Received=%d)",
					summary.AcceptRate, tc.wantAccept,
					summary.BroadcastAccepted, summary.BroadcastRejected, summary.BroadcastReceived)
			}
			if summary.PingAcceptRate != tc.wantPingAccept {
				t.Errorf("PingAcceptRate = %v, want %v (Accepted=%d Failed=%d Received=%d)",
					summary.PingAcceptRate, tc.wantPingAccept,
					summary.PingAccepted, summary.PingFailed, summary.PingReceived)
			}
		})
	}
}

func TestCollectorSummaryNoFile(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)

	// Summary without ever enabling should return empty summary
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary should not error on missing file: %v", err)
	}
	if summary.TotalEvents != 0 {
		t.Errorf("expected 0 total events, got %d", summary.TotalEvents)
	}
	if summary.RejectReasons == nil {
		t.Error("expected non-nil RejectReasons slice")
	}
}

// seedJSONL writes the given events to path as newline-delimited JSON.
// The directory must already exist; the file is overwritten if present.
func seedJSONL(t *testing.T, path string, events []Event) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("seedJSONL: create %s: %v", path, err)
	}
	defer f.Close()
	for i, ev := range events {
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().Add(time.Duration(i) * time.Millisecond)
		}
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("seedJSONL: marshal event %d: %v", i, err)
		}
		data = append(data, '\n')
		if _, err := f.Write(data); err != nil {
			t.Fatalf("seedJSONL: write event %d: %v", i, err)
		}
	}
}

// rawPayload marshals a payload map; helper to keep test seeds compact.
func rawPayload(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	if m == nil {
		return nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("rawPayload: %v", err)
	}
	return data
}

// TestSummary_TotalEventsAcrossRotatedFiles verifies H-3: Summary() must scan
// rotated files (.1.jsonl, .2.jsonl, ...) chronologically in addition to the
// active file, and TotalEvents reflects the union.
func TestSummary_TotalEventsAcrossRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "mn-debug")

	// Older events go into the higher-numbered rotated file.
	rotated1 := []Event{
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "old session"},
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "old session"},
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "old session"},
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "old session"},
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "old session"},
	}
	seedJSONL(t, base+".1.jsonl", rotated1)

	active := []Event{
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "new session"},
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "new session"},
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "new session"},
	}
	seedJSONL(t, base+".jsonl", active)

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if summary.TotalEvents != 8 {
		t.Errorf("TotalEvents = %d, want 8 (5 rotated + 3 active)", summary.TotalEvents)
	}
	if summary.SessionCount != 8 {
		t.Errorf("SessionCount = %d, want 8", summary.SessionCount)
	}
}

// TestSummary_TotalEventsSurvivesRestart verifies H-3 restart amnesia: a fresh
// Collector against an existing JSONL must report on-disk events, not the
// session-local stats.Total which resets to 0 on construction.
func TestSummary_TotalEventsSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	events := make([]Event, 10)
	for i := range events {
		events[i] = Event{
			Type:     TypeBroadcastReceived,
			Category: CategoryBroadcast,
			Source:   "10.0.0.1:37817",
			Summary:  "old broadcast",
			Payload:  rawPayload(t, map[string]any{"outpoint": fmt.Sprintf("aaa:%d", i)}),
		}
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	// Note: NOT calling Enable() — simulating a fresh process pointing at
	// existing JSONL. c.stats.Total is 0 by construction.
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if summary.TotalEvents != 10 {
		t.Errorf("TotalEvents = %d, want 10 (must reflect on-disk events, not session-local stats)", summary.TotalEvents)
	}
	if summary.BroadcastReceived != 10 {
		t.Errorf("BroadcastReceived = %d, want 10", summary.BroadcastReceived)
	}
}

// TestSummary_TierBreakdownIsUniqueMasternodes verifies H-4: TierBreakdown
// reflects the tier distribution of unique MN outpoints (counted once each),
// not the count of accepted-broadcast events per tier.
func TestSummary_TierBreakdownIsUniqueMasternodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	// 4 accepted events for outpoint A (platinum), 2 for B (platinum), 1 for C (gold).
	// Acceptance count per tier = {platinum: 6, gold: 1}.
	// Unique-MN count per tier  = {platinum: 2, gold: 1}.
	var events []Event
	for i := 0; i < 4; i++ {
		events = append(events, Event{
			Type: TypeBroadcastAccepted, Category: CategoryBroadcast,
			Source: "10.0.0.1:37817", Summary: "accepted",
			Payload: rawPayload(t, map[string]any{"outpoint": "A:0", "tier": "Platinum"}),
		})
	}
	for i := 0; i < 2; i++ {
		events = append(events, Event{
			Type: TypeBroadcastAccepted, Category: CategoryBroadcast,
			Source: "10.0.0.2:37817", Summary: "accepted",
			Payload: rawPayload(t, map[string]any{"outpoint": "B:0", "tier": "Platinum"}),
		})
	}
	events = append(events, Event{
		Type: TypeBroadcastAccepted, Category: CategoryBroadcast,
		Source: "10.0.0.3:37817", Summary: "accepted",
		Payload: rawPayload(t, map[string]any{"outpoint": "C:0", "tier": "Gold"}),
	})
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if got := summary.TierBreakdown["Platinum"]; got != 2 {
		t.Errorf("TierBreakdown[Platinum] = %d, want 2 (unique MNs, not acceptances)", got)
	}
	if got := summary.TierBreakdown["Gold"]; got != 1 {
		t.Errorf("TierBreakdown[Gold] = %d, want 1", got)
	}
	if summary.UniqueMasternodes != 3 {
		t.Errorf("UniqueMasternodes = %d, want 3", summary.UniqueMasternodes)
	}
}

// TestSummary_UniqueMasternodesExcludesRejected verifies H-5: outpoint indexing
// runs only inside TypeBroadcastAccepted, so rejected/dedup events do not
// inflate UniqueMasternodes or MasternodeDetails.
func TestSummary_UniqueMasternodesExcludesRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	events := []Event{
		// 1 accepted: outpoint A
		{Type: TypeBroadcastAccepted, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "ok",
			Payload: rawPayload(t, map[string]any{"outpoint": "A:0", "tier": "Gold"})},
		// 3 rejected: distinct outpoints B, C, D — must NOT count
		{Type: TypeBroadcastRejected, Category: CategoryBroadcast, Source: "10.0.0.2:37817", Summary: "bad",
			Payload: rawPayload(t, map[string]any{"outpoint": "B:0", "tier": "Platinum", "reason": "bad_sig"})},
		{Type: TypeBroadcastRejected, Category: CategoryBroadcast, Source: "10.0.0.3:37817", Summary: "bad",
			Payload: rawPayload(t, map[string]any{"outpoint": "C:0", "tier": "Platinum", "reason": "bad_sig"})},
		{Type: TypeBroadcastRejected, Category: CategoryBroadcast, Source: "10.0.0.4:37817", Summary: "bad",
			Payload: rawPayload(t, map[string]any{"outpoint": "D:0", "tier": "Platinum", "reason": "bad_sig"})},
		// 2 dedup: distinct outpoints E, F — must NOT count
		{Type: TypeBroadcastDedup, Category: CategoryBroadcast, Source: "10.0.0.5:37817", Summary: "dup",
			Payload: rawPayload(t, map[string]any{"outpoint": "E:0"})},
		{Type: TypeBroadcastDedup, Category: CategoryBroadcast, Source: "10.0.0.6:37817", Summary: "dup",
			Payload: rawPayload(t, map[string]any{"outpoint": "F:0"})},
		// 1 received-only (no outcome): outpoint G — must NOT count (no Accepted seen)
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.7:37817", Summary: "rcv",
			Payload: rawPayload(t, map[string]any{"outpoint": "G:0"})},
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if summary.UniqueMasternodes != 1 {
		t.Errorf("UniqueMasternodes = %d, want 1 (only A:0 has TypeBroadcastAccepted)", summary.UniqueMasternodes)
	}
	if len(summary.MasternodeDetails) != 1 {
		t.Errorf("len(MasternodeDetails) = %d, want 1", len(summary.MasternodeDetails))
	} else if summary.MasternodeDetails[0].Outpoint != "A:0" {
		t.Errorf("MasternodeDetails[0].Outpoint = %q, want A:0", summary.MasternodeDetails[0].Outpoint)
	}
	// TierBreakdown must NOT contain Platinum (those came from rejected events only).
	if got := summary.TierBreakdown["Platinum"]; got != 0 {
		t.Errorf("TierBreakdown[Platinum] = %d, want 0 (rejected events must not seed tier)", got)
	}
}

// TestSummary_UniquePeersIncludesBroadcasts verifies H-6: peer counting must
// include CategoryBroadcast events; the previous category exclusion was wrong.
func TestSummary_UniquePeersIncludesBroadcasts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	events := []Event{
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "r"},
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.2:37817", Summary: "r"},
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.3:37817", Summary: "r"},
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.4:37817", Summary: "r"},
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.5:37817", Summary: "r"},
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if summary.UniquePeers != 5 {
		t.Errorf("UniquePeers = %d, want 5 (all 5 broadcast peers must count)", summary.UniquePeers)
	}
	if len(summary.PeerDetails) != 5 {
		t.Errorf("len(PeerDetails) = %d, want 5", len(summary.PeerDetails))
	}
}

// TestSummary_OutpointShapedSourceNotCountedAsPeer verifies that the existing
// isNetworkAddress guard remains effective after dropping the category clause.
// Outpoint-shaped strings (txid:vout, no dot/bracket) must not be counted as peers.
func TestSummary_OutpointShapedSourceNotCountedAsPeer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	events := []Event{
		// outpoint-shaped: 64-hex txid + ":" + vout, contains no '.' or '['
		{Type: TypeStatusUpdate, Category: CategoryStatus,
			Source:  "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789:0",
			Summary: "status update"},
		// real peer
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "r"},
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if summary.UniquePeers != 1 {
		t.Errorf("UniquePeers = %d, want 1 (outpoint-shaped source must be filtered by isNetworkAddress)", summary.UniquePeers)
	}
}

// TestSummary_FileSizeAcrossRotatedFiles verifies that Summary.FileSize is
// the cross-file sum across the active and rotated JSONL files, not just the
// active file. The GUI header strip "events | LOG SIZE" binds to this field;
// reporting only the active file size would understate the on-disk footprint
// after rotation and contradict the cross-file TotalEvents semantics from H-3.
func TestSummary_FileSizeAcrossRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "mn-debug")

	// Seed two files with different content so their on-disk sizes differ.
	rotated := []Event{
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "rotated"},
	}
	active := []Event{
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "active1"},
		{Type: TypeSessionStart, Category: CategorySession, Source: "local", Summary: "active2"},
	}
	seedJSONL(t, base+".1.jsonl", rotated)
	seedJSONL(t, base+".jsonl", active)

	rotatedInfo, err := os.Stat(base + ".1.jsonl")
	if err != nil {
		t.Fatalf("stat rotated: %v", err)
	}
	activeInfo, err := os.Stat(base + ".jsonl")
	if err != nil {
		t.Fatalf("stat active: %v", err)
	}
	wantSize := rotatedInfo.Size() + activeInfo.Size()

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if summary.FileSize != wantSize {
		t.Errorf("FileSize = %d, want %d (active %d + rotated %d)",
			summary.FileSize, wantSize, activeInfo.Size(), rotatedInfo.Size())
	}
}

// TestClear_TruncatesRotatedFiles verifies H-3 dependency M-6: Clear() must
// truncate or remove rotated JSONL files in addition to the active file,
// otherwise the rotated-file scan keeps showing stale events after a Clear.
func TestClear_TruncatesRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "mn-debug")

	rotated := []Event{
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "old"},
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "old"},
	}
	seedJSONL(t, base+".1.jsonl", rotated)
	seedJSONL(t, base+".2.jsonl", rotated)

	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	c.Emit(Event{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "active"})

	// Pre-Clear: scan should see all rotated + active events.
	pre, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary (pre-clear) error: %v", err)
	}
	if pre.TotalEvents < 4 {
		t.Fatalf("pre-clear TotalEvents = %d, want >= 4 (scan must see rotated+active)", pre.TotalEvents)
	}

	if err := c.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	post, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary (post-clear) error: %v", err)
	}
	if post.TotalEvents != 0 {
		t.Errorf("post-clear TotalEvents = %d, want 0 (Clear must truncate rotated files)", post.TotalEvents)
	}
}

// TestSummary_MasternodeDetailsEventCountIncludesNonAcceptedEvents verifies that
// once an outpoint is validated by a TypeBroadcastAccepted event, subsequent
// non-accepted events for the same outpoint (received, dedup, rejected, skipped)
// also bump MasternodeDetails[*].eventCount. Row creation stays gated on
// TypeBroadcastAccepted (security — see TestSummary_UniqueMasternodesExcludesRejected),
// but the column is labeled "Events" and should reflect total observed activity
// for the validated MN, not just acceptances.
func TestSummary_MasternodeDetailsEventCountIncludesNonAcceptedEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	// Outpoint A is validated, then sees a flurry of subsequent activity.
	// Outpoint B is rejected only — must not appear in MasternodeDetails at all.
	events := []Event{
		// A: 1 accept (creates the row), then various non-accept events.
		{Type: TypeBroadcastAccepted, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "ok",
			Payload: rawPayload(t, map[string]any{"outpoint": "A:0", "tier": "Gold"})},
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "rcv",
			Payload: rawPayload(t, map[string]any{"outpoint": "A:0"})},
		{Type: TypeBroadcastDedup, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "dup",
			Payload: rawPayload(t, map[string]any{"outpoint": "A:0"})},
		{Type: TypeBroadcastDedup, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "dup",
			Payload: rawPayload(t, map[string]any{"outpoint": "A:0"})},
		{Type: TypeBroadcastRejected, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "bad",
			Payload: rawPayload(t, map[string]any{"outpoint": "A:0", "reason": "late_sigtime"})},
		// B: rejected only, never validated — must NOT appear in MasternodeDetails.
		{Type: TypeBroadcastRejected, Category: CategoryBroadcast, Source: "10.0.0.2:37817", Summary: "bad",
			Payload: rawPayload(t, map[string]any{"outpoint": "B:0", "tier": "Platinum", "reason": "bad_sig"})},
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if summary.UniqueMasternodes != 1 {
		t.Errorf("UniqueMasternodes = %d, want 1 (only A:0 validated)", summary.UniqueMasternodes)
	}
	if len(summary.MasternodeDetails) != 1 {
		t.Fatalf("len(MasternodeDetails) = %d, want 1", len(summary.MasternodeDetails))
	}
	got := summary.MasternodeDetails[0]
	if got.Outpoint != "A:0" {
		t.Errorf("MasternodeDetails[0].Outpoint = %q, want A:0", got.Outpoint)
	}
	// Expected: 1 accept + 1 received + 2 dedup + 1 rejected = 5 events for A:0.
	if got.EventCount != 5 {
		t.Errorf("MasternodeDetails[0].EventCount = %d, want 5 (accept + received + 2*dedup + rejected for validated MN)", got.EventCount)
	}
}

// TestSummary_MasternodeDetailsEventCountIncludesPreAcceptEvents verifies the
// chronological-order edge case: a broadcast_received event that arrives in the
// scan BEFORE the broadcast_accepted (the realistic production sequence — a peer
// always sends the broadcast first, validation completes after) must still
// contribute to eventCount. Prior to the two-pass fix, the row didn't exist yet
// at the time of the receive, so the round-3 increment-on-existing-row guard
// dropped pre-Accept events on the floor — eventCount = 1 + post-Accept events,
// missing the original received. With the two-pass fix using
// broadcastCountsByOutpoint, eventCount reflects ALL broadcast events for the
// validated MN regardless of scan order.
func TestSummary_MasternodeDetailsEventCountIncludesPreAcceptEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	// Realistic production order: received fires before accepted.
	events := []Event{
		{Type: TypeBroadcastReceived, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "rcv",
			Payload: rawPayload(t, map[string]any{"outpoint": "A:0"})},
		{Type: TypeBroadcastAccepted, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "ok",
			Payload: rawPayload(t, map[string]any{"outpoint": "A:0", "tier": "Gold"})},
		{Type: TypeBroadcastDedup, Category: CategoryBroadcast, Source: "10.0.0.1:37817", Summary: "dup",
			Payload: rawPayload(t, map[string]any{"outpoint": "A:0"})},
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if len(summary.MasternodeDetails) != 1 {
		t.Fatalf("len(MasternodeDetails) = %d, want 1", len(summary.MasternodeDetails))
	}
	got := summary.MasternodeDetails[0]
	// Expected: 1 received (PRE-Accept) + 1 accepted + 1 dedup = 3 events for A:0.
	// Without the two-pass fix this would be 2 (the pre-Accept received is dropped
	// because the row didn't exist yet at scan time).
	if got.EventCount != 3 {
		t.Errorf("MasternodeDetails[0].EventCount = %d, want 3 (must include pre-Accept broadcast_received)", got.EventCount)
	}
}

// ============================================================================
// l-mn-debug-payload-and-perf-cleanup regression tests
// ============================================================================

// TestSummary_DetailsCapsAtTopN verifies M-1: PeerDetails and MasternodeDetails
// are each capped at top 50 entries. Without the cap, long-running daemons
// produce thousands of entries serialized as JSON every 3s over the Wails bridge.
func TestSummary_DetailsCapsAtTopN(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	// Seed 75 unique accepted broadcasts at 75 distinct peer IPs with 75
	// distinct outpoints. This populates BOTH PeerDetails (one row per IP)
	// and MasternodeDetails (one row per outpoint) with 75 entries each
	// pre-cap, so both caps trip at the same time.
	var events []Event
	for i := 0; i < 75; i++ {
		events = append(events, Event{
			Type: TypeBroadcastAccepted, Category: CategoryBroadcast,
			Source: fmt.Sprintf("10.1.%d.1:37817", i), Summary: "ok",
			Payload: rawPayload(t, map[string]any{
				"outpoint": fmt.Sprintf("A:%d", i),
				"tier":     "Gold",
			}),
		})
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if len(summary.MasternodeDetails) != 50 {
		t.Errorf("len(MasternodeDetails) = %d, want 50 (cap)", len(summary.MasternodeDetails))
	}
	if len(summary.PeerDetails) != 50 {
		t.Errorf("len(PeerDetails) = %d, want 50 (cap)", len(summary.PeerDetails))
	}
	// UniqueMasternodes / UniquePeers report the FULL counts (not capped) —
	// only the detail rows are capped.
	if summary.UniqueMasternodes != 75 {
		t.Errorf("UniqueMasternodes = %d, want 75 (uncapped count)", summary.UniqueMasternodes)
	}
	if summary.UniquePeers != 75 {
		t.Errorf("UniquePeers = %d, want 75 (uncapped count)", summary.UniquePeers)
	}
}

// TestSummary_TransitionsCapsAtLastN verifies M-2: SyncTransitions and
// ActiveMNChanges are each capped at the last 100 entries via sliding window.
// Without the cap, multi-week JSONLs produce hundreds of transitions that
// serialize over the Wails bridge every 3s.
func TestSummary_TransitionsCapsAtLastN(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	// Seed 250 sync_state_change events with monotonically increasing timestamps.
	// The cap should keep the LAST 100 (timestamps 150..249).
	base := time.Now().Add(-250 * time.Second)
	var events []Event
	for i := 0; i < 250; i++ {
		events = append(events, Event{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Type:      TypeSyncStateChange,
			Category:  CategorySync,
			Source:    "local",
			Summary:   fmt.Sprintf("transition-%d", i),
			Payload: rawPayload(t, map[string]any{
				"prev_state": "Initial",
				"new_state":  fmt.Sprintf("State%d", i),
			}),
		})
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	if len(summary.SyncTransitions) != 100 {
		t.Errorf("len(SyncTransitions) = %d, want 100 (cap)", len(summary.SyncTransitions))
	}
	// First kept entry should be the 151st seeded (index 150) — `State150`.
	if len(summary.SyncTransitions) > 0 {
		got := summary.SyncTransitions[0].To
		want := "State150"
		if got != want {
			t.Errorf("SyncTransitions[0].To = %q, want %q (latest 100 kept)", got, want)
		}
	}
}

// TestSummary_SortTieBreakIsDeterministic verifies M-4: when two entries have
// the same count, secondary sort key is alphabetical-ascending so output order
// is deterministic across map-iteration randomness.
func TestSummary_SortTieBreakIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	// Seed 5 reject reasons all with count=1 but labels in a random-ish order.
	// Expected sorted result is alphabetical: alpha, bravo, delta, mike, zebra.
	labels := []string{"zebra", "alpha", "mike", "delta", "bravo"}
	var events []Event
	for _, lbl := range labels {
		events = append(events, Event{
			Type: TypeBroadcastRejected, Category: CategoryBroadcast,
			Source: "10.0.0.1:37817", Summary: "rej",
			Payload: rawPayload(t, map[string]any{
				"outpoint": "X:0",
				"reason":   lbl,
			}),
		})
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	summary, err := c.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}

	wantOrder := []string{"alpha", "bravo", "delta", "mike", "zebra"}
	if len(summary.RejectReasons) != len(wantOrder) {
		t.Fatalf("len(RejectReasons) = %d, want %d", len(summary.RejectReasons), len(wantOrder))
	}
	for i, want := range wantOrder {
		if summary.RejectReasons[i].Label != want {
			t.Errorf("RejectReasons[%d].Label = %q, want %q (alphabetical tie-break)",
				i, summary.RejectReasons[i].Label, want)
		}
	}
}

// TestSummary_Singleflight verifies M-5: concurrent Summary() callers share
// one scan. Launches 10 goroutines simultaneously; without singleflight each
// would invoke scanJSONLForSummary independently. With singleflight, all
// callers receive the same result from a single scan.
func TestSummary_Singleflight(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	// Seed enough events that a single scan takes a measurable amount of time
	// (otherwise all 10 calls might serialize before any concurrent overlap).
	var events []Event
	for i := 0; i < 5000; i++ {
		events = append(events, Event{
			Type:    TypeSyncStateChange,
			Category: CategorySync,
			Source:   "local",
			Summary:  "sync",
			Payload:  rawPayload(t, map[string]any{"prev_state": "A", "new_state": "B"}),
		})
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	startBefore := c.scanFileCount.Load()

	const N = 10
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := c.Summary(); err != nil {
				t.Errorf("Summary() error: %v", err)
			}
		}()
	}
	wg.Wait()

	// listJSONLFilesChronological returns 1 path here (single active file),
	// so each Summary() call invokes scanJSONLForSummary exactly once.
	// With singleflight, 10 concurrent Summary() calls collapse to 1 scan.
	// Without it, we'd see 10 scans.
	delta := c.scanFileCount.Load() - startBefore
	if delta > 1 {
		t.Errorf("scanFileCount delta = %d, want 1 (10 concurrent Summary() calls should singleflight to 1 scan)", delta)
	}
}

// TestSummary_ClearInvalidatesSingleflightGen verifies that Clear() bumps
// summaryGen, so callers arriving AFTER a Clear cannot join an in-flight
// pre-Clear singleflight scan and receive stale (pre-Clear) data.
//
// This test deliberately races a long-running Summary against a concurrent
// Clear: goroutine A starts Summary on a 5000-event log (slow scan); after
// a short head start, the main goroutine calls Clear (which bumps
// summaryGen and removes the rotated/active files); then goroutine B
// starts a second Summary. WITHOUT the gen-tied key, B would join A's
// singleflight group on the constant key "summary" and receive A's
// pre-Clear result. WITH the gen-tied key, A's group is "summary-0" and
// B's group is "summary-1", so they are independent and B sees the
// post-Clear (empty) state.
//
// The assertion compares A's TotalEvents (pre-Clear, ~5000) to B's
// TotalEvents (post-Clear, 0–1). Without the fix, B == A. With the fix,
// B << A.
func TestSummary_ClearInvalidatesSingleflightGen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mn-debug.jsonl")

	// Seed enough data that a single Summary scan is slow (~50ms) so the
	// race window is observable on any reasonable machine.
	var events []Event
	for i := 0; i < 5000; i++ {
		events = append(events, Event{
			Type: TypeBroadcastReceived, Category: CategoryBroadcast,
			Source: "10.0.0.1:37817", Summary: "rcv",
			Payload: rawPayload(t, map[string]any{"outpoint": fmt.Sprintf("X:%d", i)}),
		})
	}
	seedJSONL(t, path, events)

	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	defer c.Close()

	gen0 := c.summaryGen.Load()

	// Goroutine A: starts the slow Summary scan with the pre-Clear gen.
	type result struct {
		s   *Summary
		err error
	}
	chA := make(chan result, 1)
	startA := make(chan struct{})
	go func() {
		<-startA
		s, err := c.Summary()
		chA <- result{s, err}
	}()

	// Capture the scan counter BEFORE A starts so the wait below knows when
	// A's scan has entered scanJSONLForSummary (counter advanced by exactly
	// one). Deterministic synchronization replaces a brittle time.Sleep.
	scanCountBefore := c.scanFileCount.Load()
	close(startA)

	// Wait for A's scan to actually enter scanJSONLForSummary so we know the
	// singleflight slot is claimed for the pre-Clear gen. Poll the counter
	// every 100µs; bound the wait at 2 s so a hung goroutine fails the test
	// loudly instead of hanging the suite. This is the deterministic
	// alternative to time.Sleep on slow / overloaded CI machines (Gemini W1
	// in code-review round 3).
	deadline := time.Now().Add(2 * time.Second)
	for c.scanFileCount.Load() == scanCountBefore {
		if time.Now().After(deadline) {
			t.Fatalf("goroutine A did not enter scan within 2s; race window not established")
		}
		time.Sleep(100 * time.Microsecond)
	}

	// Clear() bumps gen and removes all log files. Any Summary started
	// AFTER this point should NOT join A's group — it must form a new
	// singleflight on the bumped gen.
	if err := c.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	gen1 := c.summaryGen.Load()
	if gen1 <= gen0 {
		t.Errorf("summaryGen did not advance after Clear: gen0=%d, gen1=%d", gen0, gen1)
	}

	// Goroutine B: starts AFTER Clear with the post-Clear gen.
	chB := make(chan result, 1)
	go func() {
		s, err := c.Summary()
		chB <- result{s, err}
	}()

	// Both goroutines complete. A may already be done (cache hit on a fast
	// machine) — that is fine, the assertion still holds because A's
	// captured Summary reflects the seeded JSONL while B reflects the
	// cleared state.
	resA := <-chA
	resB := <-chB
	if resA.err != nil {
		t.Fatalf("goroutine A Summary: %v", resA.err)
	}
	if resB.err != nil {
		t.Fatalf("goroutine B Summary: %v", resB.err)
	}

	if resA.s.BroadcastReceived == 0 {
		t.Fatalf("goroutine A Summary has no broadcast events; test fixture broken (race window may have been too short)")
	}

	if resB.s.TotalEvents >= resA.s.TotalEvents {
		t.Errorf("goroutine B (post-Clear) TotalEvents = %d, want < goroutine A (pre-Clear) TotalEvents = %d "+
			"(Clear should invalidate the in-flight singleflight group; B must NOT join A's pre-Clear scan)",
			resB.s.TotalEvents, resA.s.TotalEvents)
	}
}

func TestMatchesFilter(t *testing.T) {
	now := time.Now()
	event := Event{
		Timestamp: now,
		Type:      TypeBroadcastReceived,
		Category:  CategoryBroadcast,
		Source:    "192.168.1.1:37817",
		Summary:   "Received Gold tier MNB from peer",
	}

	tests := []struct {
		name    string
		filter  Filter
		matches bool
	}{
		{"empty filter matches all", Filter{}, true},
		{"matching category", Filter{Category: CategoryBroadcast}, true},
		{"non-matching category", Filter{Category: CategorySync}, false},
		{"matching type", Filter{Type: TypeBroadcastReceived}, true},
		{"non-matching type", Filter{Type: TypeSyncStateChange}, false},
		{"matching source", Filter{Source: "192.168.1.1:37817"}, true},
		{"non-matching source", Filter{Source: "10.0.0.1:37817"}, false},
		{"matching search", Filter{Search: "gold tier"}, true},
		{"non-matching search", Filter{Search: "platinum"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesFilter(event, tt.filter)
			if got != tt.matches {
				t.Errorf("matchesFilter() = %v, want %v", got, tt.matches)
			}
		})
	}
}

// TestQueryReadsRotatedFiles verifies that Query() scans rotated JSONL
// files in addition to the active one — closes the data-scope discrepancy
// between the Events sub-tab (was active-file-only) and the Overview
// sub-tab (Summary scans all files).
func TestQueryReadsRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 0, 3) // maxFiles=3, default size then override
	// Cap chosen so all emitted events stay within the 3-rotated + active
	// retention window — large enough to avoid purging, small enough to
	// force at least one rotation. ~250 bytes/event × 8 events/file = ~2KB.
	c.maxSizeBytes = 2000
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	// Emit 15 sync events — fits within retention but forces rotation.
	for i := 0; i < 15; i++ {
		c.EmitSync(TypeSyncStateChange, "local",
			fmt.Sprintf("event-%02d filler text padding for size", i), nil)
	}

	// Verify at least one rotated file was created.
	base := filepath.Join(dir, "mn-debug")
	if _, err := os.Stat(base + ".1.jsonl"); os.IsNotExist(err) {
		t.Fatalf("test setup failed: expected .1.jsonl to exist after 15 emits with 2KB cap")
	}

	// Cross-file query: TotalMatched should reflect ALL events across rotated
	// + active files (1 session_start + 15 sync events = 16 total).
	res, err := c.Query(Filter{Newest: true, Limit: 100})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if res.TotalMatched != 16 {
		t.Errorf("expected TotalMatched=16 across rotated+active files, got %d (FilesScanned=%d)",
			res.TotalMatched, res.FilesScanned)
	}
	if res.FilesScanned < 2 {
		t.Errorf("expected at least 2 files scanned (1 rotated + active), got %d", res.FilesScanned)
	}

	// Verify oldest event is preserved across rotation by searching for
	// "event-00" — present only in the oldest rotated file by emit order.
	res, err = c.Query(Filter{Search: "event-00"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if res.TotalMatched != 1 {
		t.Errorf("expected event-00 (rotated file) to be findable via cross-file Query, got TotalMatched=%d", res.TotalMatched)
	}
}

// TestQueryTruncatedFlag verifies QueryResult.Truncated is set correctly
// when the limit is below the matched count, and that TotalMatched still
// reflects the full match count.
func TestQueryTruncatedFlag(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	// Emit 50 sync events.
	for i := 0; i < 50; i++ {
		c.EmitSync(TypeSyncStateChange, "local", fmt.Sprintf("event-%d", i), nil)
	}

	// Limit=10 against 50 matching → Truncated=true, TotalMatched=50.
	res, err := c.Query(Filter{Limit: 10, Newest: true, Category: CategorySync})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(res.Events) != 10 {
		t.Errorf("expected 10 events returned (limit), got %d", len(res.Events))
	}
	if res.TotalMatched != 50 {
		t.Errorf("expected TotalMatched=50, got %d", res.TotalMatched)
	}
	if !res.Truncated {
		t.Errorf("expected Truncated=true when len(Events) < TotalMatched")
	}

	// Limit >= TotalMatched → Truncated=false.
	res, err = c.Query(Filter{Limit: 100, Newest: true, Category: CategorySync})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if res.TotalMatched != 50 {
		t.Errorf("expected TotalMatched=50, got %d", res.TotalMatched)
	}
	if res.Truncated {
		t.Errorf("expected Truncated=false when limit >= TotalMatched")
	}
}

// TestQueryByCategory verifies QueryResult.ByCategory reflects the
// category breakdown of matched events (filter-aware).
func TestQueryByCategory(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, 1, 3)
	if err := c.Enable(); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	defer c.Close()

	// 5 broadcast + 3 ping + 2 sync events.
	for i := 0; i < 5; i++ {
		c.EmitBroadcast(TypeBroadcastReceived, "10.0.0.1:37817", fmt.Sprintf("bcast-%d", i), nil)
	}
	for i := 0; i < 3; i++ {
		c.EmitPing(TypePingReceived, "10.0.0.2:37817", fmt.Sprintf("ping-%d", i), nil)
	}
	for i := 0; i < 2; i++ {
		c.EmitSync(TypeSyncStateChange, "local", fmt.Sprintf("sync-%d", i), nil)
	}

	// Unfiltered query — broadcast/ping/sync each present with their counts.
	// The 1 session_start event also counts under CategorySession (not Sync).
	res, err := c.Query(Filter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if res.ByCategory[CategoryBroadcast] != 5 {
		t.Errorf("expected ByCategory[broadcast]=5, got %d", res.ByCategory[CategoryBroadcast])
	}
	if res.ByCategory[CategoryPing] != 3 {
		t.Errorf("expected ByCategory[ping]=3, got %d", res.ByCategory[CategoryPing])
	}
	if res.ByCategory[CategorySync] != 2 {
		t.Errorf("expected ByCategory[sync]=2 (session_start is session, not sync), got %d", res.ByCategory[CategorySync])
	}

	// Filtered to broadcast — only broadcast key, count 5.
	res, err = c.Query(Filter{Category: CategoryBroadcast})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if res.ByCategory[CategoryBroadcast] != 5 {
		t.Errorf("expected filtered ByCategory[broadcast]=5, got %d", res.ByCategory[CategoryBroadcast])
	}
	if res.ByCategory[CategoryPing] != 0 {
		t.Errorf("expected filtered ByCategory[ping]=0, got %d", res.ByCategory[CategoryPing])
	}
	if res.ByCategory[CategorySync] != 0 {
		t.Errorf("expected filtered ByCategory[sync]=0, got %d", res.ByCategory[CategorySync])
	}
}
