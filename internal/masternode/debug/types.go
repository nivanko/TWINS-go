package debug

import (
	"encoding/json"
	"time"
)

// Event categories for masternode debug events.
const (
	CategorySync      = "sync"
	CategoryBroadcast = "broadcast"
	CategoryPing      = "ping"
	CategoryStatus    = "status"
	CategoryWinner    = "winner"
	CategoryActive    = "active"
	CategoryNetwork   = "network"
	CategorySession   = "session"
)

// Event types per category.
// Only types actually emitted in production code are defined here.
const (
	// Sync events
	TypeSyncStateChange = "sync_state_change"

	// Broadcast events
	TypeBroadcastReceived = "broadcast_received"
	TypeBroadcastAccepted = "broadcast_accepted"
	TypeBroadcastRejected = "broadcast_rejected"
	TypeBroadcastDedup    = "broadcast_dedup"
	TypeBroadcastSkipped  = "broadcast_skipped"

	// Ping events
	TypePingStageResult = "ping_stage_result"
	TypePingReceived    = "ping_received"
	TypePingAccepted    = "ping_accepted"
	TypePingRejected    = "ping_rejected"
	TypePingSkipped     = "ping_skipped"

	// Status events
	TypeStatusUpdate = "status_update"

	// Winner events
	TypeWinnerVote = "winner_vote"

	// Session events
	TypeSessionStart = "session_start"
)

// Event represents a single debug event written to the JSONL file.
type Event struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Category  string          `json:"category"`
	Source    string          `json:"source"`  // peer address or "local"
	Summary   string          `json:"summary"` // human-readable one-liner
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Filter specifies criteria for querying debug events.
type Filter struct {
	Category string `json:"category,omitempty"`
	Type     string `json:"type,omitempty"`
	Source   string `json:"source,omitempty"`
	Search   string `json:"search,omitempty"` // text search in summary
	Limit    int    `json:"limit,omitempty"`  // max results (0 = default 1000)
	Newest   bool   `json:"newest,omitempty"` // return newest N events instead of oldest
}

// Stats contains event counts by category.
type Stats struct {
	Total      int64            `json:"total"`
	ByCategory map[string]int64 `json:"byCategory"`
	FileSize   int64            `json:"fileSize"` // bytes
	Enabled    bool             `json:"enabled"`
}

// QueryResult bundles paginated events from a cross-file Query() scan with
// honest count metadata so callers can render "X of Y matching" and
// truncation indicators without re-counting.
//
// TotalMatched: number of events matching the filter across ALL scanned
// files (not capped by Filter.Limit).
// TotalScanned: number of events scanned across ALL files (filter-independent).
// ByCategory: category breakdown of matched events.
// Truncated: true when TotalMatched > len(Events). Direction of dropped
// matches depends on Filter.Newest:
//   - Newest=true (caller wants most-recent N): the OLDER matches were
//     dropped via ring-buffer trim during scan.
//   - Newest=false (caller wants oldest N): the NEWER matches were not
//     appended once the limit was reached, but still counted into
//     TotalMatched / ByCategory.
// The GUI Banner copy ("Showing most recent N of M matching events") is
// Newest=true-specific; downstream callers passing Newest=false should
// render their own copy.
// FilesScanned: number of JSONL files actually opened (rotated + active).
// FileSize: cross-file `os.Stat` sum across every JSONL file the scan walked
// (mirrors `Summary.FileSize`). Exposed on QueryResult so the GUI Events
// sub-tab has a fresh cross-file file-size source — `Summary.FileSize`
// goes stale while the Events sub-tab is mounted (`fetchSummary` is gated
// to Overview to avoid expensive cross-file rescans on every 3s tick).
type QueryResult struct {
	Events       []Event          `json:"events"`
	TotalMatched int64            `json:"totalMatched"`
	TotalScanned int64            `json:"totalScanned"`
	ByCategory   map[string]int64 `json:"byCategory"`
	Truncated    bool             `json:"truncated"`
	FilesScanned int              `json:"filesScanned"`
	FileSize     int64            `json:"fileSize"`
}

// Summary contains aggregated insights computed from all debug events.
type Summary struct {
	// Overview
	FirstEvent   string `json:"firstEvent"`   // ISO 8601 timestamp of earliest event
	LastEvent    string `json:"lastEvent"`     // ISO 8601 timestamp of latest event
	TotalEvents  int64  `json:"totalEvents"`
	FileSize     int64  `json:"fileSize"`
	SessionCount int64  `json:"sessionCount"`

	// Broadcast health
	BroadcastReceived int64              `json:"broadcastReceived"`
	BroadcastAccepted int64              `json:"broadcastAccepted"`
	BroadcastRejected int64              `json:"broadcastRejected"`
	BroadcastDedup    int64              `json:"broadcastDedup"`
	BroadcastSkipped  int64              `json:"broadcastSkipped"`
	AcceptRate        float64            `json:"acceptRate"` // percentage
	RejectReasons     []ReasonCount      `json:"rejectReasons"`
	UniqueMasternodes int64              `json:"uniqueMasternodes"`
	TierBreakdown     map[string]int64   `json:"tierBreakdown"`
	TopSources        []SourceCount      `json:"topSources"`

	// Ping health
	PingReceived       int64   `json:"pingReceived"`
	PingAccepted       int64   `json:"pingAccepted"`
	PingFailed         int64   `json:"pingFailed"`
	PingSkipped        int64   `json:"pingSkipped"`
	PingAcceptRate     float64 `json:"pingAcceptRate"`
	ActivePingsSent    int64   `json:"activePingsSent"`
	ActivePingsSuccess int64   `json:"activePingsSuccess"`
	ActivePingsFailed  int64   `json:"activePingsFailed"`

	// Network activity
	DSEGRequests    int64   `json:"dsegRequests"`
	DSEGResponses   int64   `json:"dsegResponses"`
	AvgMNsServed    float64 `json:"avgMNsServed"`
	NetworkMNBCount int64   `json:"networkMNBCount"`
	NetworkMNPCount int64   `json:"networkMNPCount"`
	UniquePeers     int64   `json:"uniquePeers"`

	// Status & sync timeline
	SyncTransitions  []StatusTransition `json:"syncTransitions"`
	StatusChanges    []ReasonCount      `json:"statusChanges"`
	ActiveMNChanges  []StatusTransition `json:"activeMNChanges"`

	// Detail lists for clickable stats
	PeerDetails       []PeerDetail       `json:"peerDetails"`
	MasternodeDetails []MasternodeDetail `json:"masternodeDetails"`
}

// ReasonCount pairs a reason/label with its occurrence count.
type ReasonCount struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// SourceCount pairs a peer source address with its event count.
type SourceCount struct {
	Source string `json:"source"`
	Count  int64  `json:"count"`
}

// StatusTransition records a state/status change event.
type StatusTransition struct {
	Timestamp string `json:"timestamp"` // ISO 8601
	From      string `json:"from"`
	To        string `json:"to"`
}

// PeerDetail contains per-peer event statistics.
type PeerDetail struct {
	Address    string `json:"address"`
	EventCount int64  `json:"eventCount"`
}

// MasternodeDetail contains per-masternode event statistics.
type MasternodeDetail struct {
	Outpoint   string `json:"outpoint"`
	Address    string `json:"address"` // collateral payee address (TWINS base58)
	Tier       string `json:"tier"`
	EventCount int64  `json:"eventCount"`
}
