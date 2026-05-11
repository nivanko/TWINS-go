package main

import (
	"fmt"

	"github.com/twins-dev/twins-core/internal/masternode/debug"
)

// ==========================================
// Masternode Debug Handlers
// ==========================================

// DebugStatusResponse contains the current debug system status.
type DebugStatusResponse struct {
	Enabled    bool             `json:"enabled"`
	Total      int64            `json:"total"`
	ByCategory map[string]int64 `json:"byCategory"`
	FileSize   int64            `json:"fileSize"`
}

// DebugEvent is a frontend-friendly representation of a debug event.
type DebugEvent struct {
	Timestamp string         `json:"timestamp"` // ISO 8601
	Type      string         `json:"type"`
	Category  string         `json:"category"`
	Source    string         `json:"source"`
	Summary   string         `json:"summary"`
	Payload   string         `json:"payload"` // raw JSON string
}

// DebugFilter specifies criteria for querying debug events from the frontend.
type DebugFilter struct {
	Category string `json:"category,omitempty"`
	Type     string `json:"type,omitempty"`
	Source   string `json:"source,omitempty"`
	Search   string `json:"search,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// DebugEventsPage bundles the paginated event list with cross-file count
// metadata so the GUI can show "X of Y matching" indicators and a truncation
// banner without re-fetching. Mirrors debug.QueryResult with timestamp
// strings instead of time.Time for JSON ergonomics.
type DebugEventsPage struct {
	Events       []DebugEvent     `json:"events"`
	TotalMatched int64            `json:"totalMatched"`
	TotalScanned int64            `json:"totalScanned"`
	ByCategory   map[string]int64 `json:"byCategory"`
	Truncated    bool             `json:"truncated"`
	FilesScanned int              `json:"filesScanned"`
	FileSize     int64            `json:"fileSize"`
}

// GetDebugStatus returns the current debug system status and event counts.
func (a *App) GetDebugStatus() (*DebugStatusResponse, error) {
	collector := a.getDebugCollector()
	if collector == nil {
		return &DebugStatusResponse{
			Enabled:    false,
			ByCategory: make(map[string]int64),
		}, nil
	}

	stats := collector.Stats()
	return &DebugStatusResponse{
		Enabled:    stats.Enabled,
		Total:      stats.Total,
		ByCategory: stats.ByCategory,
		FileSize:   stats.FileSize,
	}, nil
}

// GetDebugEvents queries the debug log across all rotated + active JSONL
// files and returns up to `limit` events with honest count metadata. The
// underlying collector.Query() is now cross-file (mirroring Summary()), so
// the Events sub-tab data scope matches the Overview sub-tab. Returns
// DebugEventsPage with TotalMatched/TotalScanned/ByCategory/Truncated for
// filter feedback, category chips, and truncation banner rendering.
func (a *App) GetDebugEvents(filter DebugFilter) (*DebugEventsPage, error) {
	collector := a.getDebugCollector()
	if collector == nil {
		return &DebugEventsPage{
			Events:     []DebugEvent{},
			ByCategory: make(map[string]int64),
		}, nil
	}

	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}

	res, err := collector.Query(debug.Filter{
		Category: filter.Category,
		Type:     filter.Type,
		Source:   filter.Source,
		Search:   filter.Search,
		Limit:    limit,
		Newest:   true,
	})
	if err != nil {
		return nil, fmt.Errorf("query debug events: %w", err)
	}

	// Query returns newest-first when Newest is set.
	converted := make([]DebugEvent, 0, len(res.Events))
	for _, e := range res.Events {
		converted = append(converted, DebugEvent{
			Timestamp: e.Timestamp.Format("2006-01-02T15:04:05.000Z07:00"),
			Type:      e.Type,
			Category:  e.Category,
			Source:    e.Source,
			Summary:   e.Summary,
			Payload:   string(e.Payload),
		})
	}

	return &DebugEventsPage{
		Events:       converted,
		TotalMatched: res.TotalMatched,
		TotalScanned: res.TotalScanned,
		ByCategory:   res.ByCategory,
		Truncated:    res.Truncated,
		FilesScanned: res.FilesScanned,
		FileSize:     res.FileSize,
	}, nil
}

// GetDebugSummary returns aggregated statistics from all debug events.
func (a *App) GetDebugSummary() (*debug.Summary, error) {
	collector := a.getDebugCollector()
	if collector == nil {
		return &debug.Summary{
			TierBreakdown:     make(map[string]int64),
			RejectReasons:     []debug.ReasonCount{},
			TopSources:        []debug.SourceCount{},
			SyncTransitions:   []debug.StatusTransition{},
			StatusChanges:     []debug.ReasonCount{},
			ActiveMNChanges:   []debug.StatusTransition{},
			PeerDetails:       []debug.PeerDetail{},
			MasternodeDetails: []debug.MasternodeDetail{},
		}, nil
	}

	summary, err := collector.Summary()
	if err != nil {
		return nil, fmt.Errorf("get debug summary: %w", err)
	}
	return summary, nil
}

// ClearDebugLog truncates the current debug log file.
func (a *App) ClearDebugLog() error {
	collector := a.getDebugCollector()
	if collector == nil {
		return fmt.Errorf("debug collector not initialized")
	}

	return collector.Clear()
}

// getDebugCollector returns the debug collector from the node, or nil.
func (a *App) getDebugCollector() *debug.Collector {
	a.componentsMu.RLock()
	node := a.node
	a.componentsMu.RUnlock()

	if node == nil {
		return nil
	}
	// Lock-free atomic load — never reads a torn pointer even when the
	// masternode.debug subscriber is concurrently swapping the field.
	return node.DebugCollector.Load()
}

// IsDebugCollectorActive reports whether the masternode debug collector is
// currently running. Use this — not GetDaemonConfigBool("masternode.debug") —
// for the GUI Debug tab gate, since collector startup can fail (e.g. on a
// read-only data directory) leaving the config value true while the collector
// is nil. The masternode:debug-changed Wails event also reports the effective
// state so live updates stay aligned.
func (a *App) IsDebugCollectorActive() bool {
	return a.getDebugCollector() != nil
}
