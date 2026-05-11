package debug

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	defaultMaxSizeMB = 50
	defaultMaxFiles  = 3
	defaultFilename  = "mn-debug.jsonl"
	defaultQueryLimit = 1000

	// maxDetailsRows caps the size of Summary.PeerDetails and
	// Summary.MasternodeDetails so the GUI does not have to render
	// thousands of rows in a modal every 3 s. UniquePeers / UniqueMasternodes
	// remain uncapped — only the per-row detail lists are capped.
	maxDetailsRows = 50

	// maxTransitionsRows caps the size of Summary.SyncTransitions and
	// Summary.ActiveMNChanges via a sliding window keeping the LATEST N
	// entries. Multi-week JSONLs produce hundreds of transitions; capping
	// preserves recent context without blowing up the JSON payload.
	maxTransitionsRows = 100
)

// Collector captures masternode debug events to JSONL files.
// Uses atomic.Bool for zero-cost disabled check — callers should check
// IsEnabled() before constructing event payloads.
//
// Lock ordering: statsMu is a leaf lock — never acquires mu while held.
// mu may acquire statsMu (Emit, Clear). Stats() only acquires statsMu.
type Collector struct {
	enabled atomic.Bool

	mu       sync.Mutex
	file     *os.File
	writer   *bufio.Writer
	filePath string
	dataDir  string

	maxSizeBytes int64
	maxFiles     int

	// statsMu protects stats and currentSize together.
	// Lock ordering: mu → statsMu (never reverse).
	statsMu     sync.RWMutex
	stats       Stats
	currentSize int64

	// scanFileCount counts cumulative invocations of scanJSONLForSummary.
	// Used by the singleflight regression test to verify concurrent Summary()
	// calls collapse to a single scan; production code does not branch on it.
	scanFileCount atomic.Int64

	// summarySF deduplicates concurrent Summary() calls so they share a
	// single cross-file JSONL scan. The GUI polls Summary() every 3 s; if
	// a single scan exceeds 3 s on a large log, overlapping calls would
	// otherwise each re-read the entire JSONL. singleflight collapses them.
	summarySF singleflight.Group

	// summaryGen is the singleflight key generation. Bumped by Clear() so
	// callers that arrive AFTER a Clear cannot join an in-flight pre-Clear
	// scan and receive stale results — the post-Clear key differs, forming
	// a fresh singleflight group. Read in Summary(); written in Clear().
	summaryGen atomic.Uint64
}

// NewCollector creates a new debug event collector.
// The collector starts disabled; call Enable() to begin collecting.
func NewCollector(dataDir string, maxSizeMB, maxFiles int) *Collector {
	if maxSizeMB <= 0 {
		maxSizeMB = defaultMaxSizeMB
	}
	if maxFiles <= 0 {
		maxFiles = defaultMaxFiles
	}

	return &Collector{
		dataDir:      dataDir,
		filePath:     filepath.Join(dataDir, defaultFilename),
		maxSizeBytes: int64(maxSizeMB) * 1024 * 1024,
		maxFiles:     maxFiles,
		stats: Stats{
			ByCategory: make(map[string]int64),
		},
	}
}

// IsEnabled returns true if debug collection is active.
// This is the fast-path check — use before constructing event data.
func (c *Collector) IsEnabled() bool {
	return c.enabled.Load()
}

// Enable starts debug collection, opening the JSONL file for writing.
func (c *Collector) Enable() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.file != nil {
		// Already enabled
		c.enabled.Store(true)
		return nil
	}

	f, err := os.OpenFile(c.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("debug collector: failed to open %s: %w", c.filePath, err)
	}

	info, _ := f.Stat()
	if info != nil {
		c.statsMu.Lock()
		c.currentSize = info.Size()
		c.statsMu.Unlock()
	}

	c.file = f
	c.writer = bufio.NewWriterSize(f, 64*1024) // 64KB write buffer
	c.enabled.Store(true)

	// Write session start marker directly (we hold c.mu, cannot call Emit()).
	sessionEvent := Event{
		Timestamp: time.Now(),
		Type:      TypeSessionStart,
		Category:  CategorySession,
		Source:    "local",
		Summary:   "Debug session started",
	}
	if data, err := json.Marshal(sessionEvent); err == nil {
		data = append(data, '\n')
		if n, err := c.writer.Write(data); err == nil {
			c.writer.Flush()
			c.statsMu.Lock()
			c.currentSize += int64(n)
			c.stats.Total++
			c.stats.ByCategory[sessionEvent.Category]++
			c.statsMu.Unlock()
		}
	}

	return nil
}

// Disable stops debug collection and closes the file.
func (c *Collector) Disable() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.enabled.Store(false)
	c.closeFileLocked()
}

// Emit writes a debug event to the JSONL file.
// Returns immediately if collection is disabled (zero-cost atomic check).
func (c *Collector) Emit(event Event) {
	if !c.enabled.Load() {
		return
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	data = append(data, '\n')

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.writer == nil {
		return
	}

	n, err := c.writer.Write(data)
	if err != nil {
		return
	}

	// Flush periodically (every ~4KB of buffered data)
	if c.writer.Buffered() > 4096 {
		c.writer.Flush()
	}

	// Update stats and currentSize together under statsMu
	c.statsMu.Lock()
	c.currentSize += int64(n)
	c.stats.Total++
	c.stats.ByCategory[event.Category]++
	needsRotation := c.currentSize >= c.maxSizeBytes
	c.statsMu.Unlock()

	// Check if rotation is needed
	if needsRotation {
		c.rotateLocked()
	}
}

// EmitSync emits a sync-category event.
func (c *Collector) EmitSync(eventType, source, summary string, payload map[string]any) {
	if !c.enabled.Load() {
		return
	}
	c.emitWithPayload(CategorySync, eventType, source, summary, payload)
}

// EmitBroadcast emits a broadcast-category event.
func (c *Collector) EmitBroadcast(eventType, source, summary string, payload map[string]any) {
	if !c.enabled.Load() {
		return
	}
	c.emitWithPayload(CategoryBroadcast, eventType, source, summary, payload)
}

// EmitPing emits a ping-category event.
func (c *Collector) EmitPing(eventType, source, summary string, payload map[string]any) {
	if !c.enabled.Load() {
		return
	}
	c.emitWithPayload(CategoryPing, eventType, source, summary, payload)
}

// EmitStatus emits a status-category event.
func (c *Collector) EmitStatus(eventType, source, summary string, payload map[string]any) {
	if !c.enabled.Load() {
		return
	}
	c.emitWithPayload(CategoryStatus, eventType, source, summary, payload)
}

// EmitWinner emits a winner-category event.
func (c *Collector) EmitWinner(eventType, source, summary string, payload map[string]any) {
	if !c.enabled.Load() {
		return
	}
	c.emitWithPayload(CategoryWinner, eventType, source, summary, payload)
}

// EmitActive emits an active-masternode-category event.
func (c *Collector) EmitActive(eventType, source, summary string, payload map[string]any) {
	if !c.enabled.Load() {
		return
	}
	c.emitWithPayload(CategoryActive, eventType, source, summary, payload)
}

// EmitNetwork emits a network/P2P-category event.
func (c *Collector) EmitNetwork(eventType, source, summary string, payload map[string]any) {
	if !c.enabled.Load() {
		return
	}
	c.emitWithPayload(CategoryNetwork, eventType, source, summary, payload)
}

func (c *Collector) emitWithPayload(category, eventType, source, summary string, payload map[string]any) {
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err == nil {
			raw = data
		}
	}
	c.Emit(Event{
		Timestamp: time.Now(),
		Type:      eventType,
		Category:  category,
		Source:    source,
		Summary:   summary,
		Payload:   raw,
	})
}

// Stats returns current event statistics.
func (c *Collector) Stats() Stats {
	c.statsMu.RLock()
	stats := Stats{
		Total:      c.stats.Total,
		ByCategory: make(map[string]int64, len(c.stats.ByCategory)),
		Enabled:    c.enabled.Load(),
		FileSize:   c.currentSize,
	}
	maps.Copy(stats.ByCategory, c.stats.ByCategory)
	c.statsMu.RUnlock()

	return stats
}

// mnAccumHelper tracks per-masternode event counts during summary computation.
type mnAccumHelper struct {
	addr  string
	tier  string
	count int64
}

// listJSONLFilesChronological returns existing JSONL paths in chronological
// order: oldest rotated first (mn-debug.{maxFiles}.jsonl), down to .1.jsonl,
// then the active file. Non-existent files are skipped. Caller need not hold mu.
func (c *Collector) listJSONLFilesChronological() []string {
	base := strings.TrimSuffix(c.filePath, ".jsonl")
	paths := make([]string, 0, c.maxFiles+1)
	for i := c.maxFiles; i >= 1; i-- {
		p := fmt.Sprintf("%s.%d.jsonl", base, i)
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	if _, err := os.Stat(c.filePath); err == nil {
		paths = append(paths, c.filePath)
	}
	return paths
}

// Summary reads all events across the active and rotated JSONL files and
// computes aggregated statistics. TotalEvents is computed from the scan
// itself — it is the single source of truth for "all-time" event count and
// survives daemon restarts and log rotation.
//
// Concurrent callers share a single scan via singleflight; this matters when
// the GUI polls every 3 s and a single scan on a large rotated set takes
// longer than the poll interval, which would otherwise queue up redundant
// scans (the "thundering herd" the M-5 cleanup explicitly targets).
//
// Trade-off: any caller that arrives while a scan is in flight joins that
// scan's group and receives the snapshot taken at scan-START — not at
// scan-end. So if A starts a 4 s scan at t=0 and B calls at t=2, B sees
// the t=0 view of the JSONL even though new events landed between t=0 and
// t=2. This is the EXPLICIT design of singleflight. The acceptable
// staleness is bounded by scan duration; the alternative (every call
// independent) was rejected because it makes the cost-per-poll grow
// linearly with log size (re-reading hundreds of MB every 3 s on a
// long-running daemon).
//
// Mutation invalidation: Clear() bumps summaryGen so post-Clear callers
// form a NEW singleflight group on the bumped key — they cannot join an
// in-flight pre-Clear scan and receive its stale (now-deleted) data.
// Callers that arrive only between two append-only Emit() calls are NOT
// considered stale — they see a recent (≤ scan-duration old) snapshot,
// which is the documented contract.
func (c *Collector) Summary() (*Summary, error) {
	// Tie the singleflight key to summaryGen so Clear() can invalidate any
	// in-flight scan: a Clear()-bumped generation means subsequent callers
	// form a NEW singleflight group instead of joining the stale pre-Clear
	// scan and receiving the cleared-log's prior contents.
	key := fmt.Sprintf("summary-%d", c.summaryGen.Load())
	v, err, _ := c.summarySF.Do(key, func() (interface{}, error) {
		return c.summaryUncached()
	})
	if err != nil {
		return nil, err
	}
	return v.(*Summary), nil
}

// summaryUncached is the non-deduplicated body of Summary. It is invoked
// only via the singleflight wrapper above — direct calls would defeat the
// dedup invariant. Unexported so external packages cannot bypass the wrap.
func (c *Collector) summaryUncached() (*Summary, error) {
	c.mu.Lock()
	if c.writer != nil {
		c.writer.Flush()
	}
	c.mu.Unlock()

	s := &Summary{
		TierBreakdown: make(map[string]int64),
	}

	paths := c.listJSONLFilesChronological()

	// FileSize is the cross-file sum of all JSONL files in the rotation set
	// (active + rotated). The GUI binds the header LOG SIZE card to this
	// field; reporting only the active file size would understate the on-disk
	// footprint and contradict the cross-file TotalEvents semantics from H-3.
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			s.FileSize += info.Size()
		}
	}

	if len(paths) == 0 {
		s.RejectReasons = []ReasonCount{}
		s.TopSources = []SourceCount{}
		s.SyncTransitions = []StatusTransition{}
		s.StatusChanges = []ReasonCount{}
		s.ActiveMNChanges = []StatusTransition{}
		s.PeerDetails = []PeerDetail{}
		s.MasternodeDetails = []MasternodeDetail{}
		return s, nil
	}

	// Tracking maps shared across all files.
	uniqueOutpoints := make(map[string]struct{})
	sourceCounts := make(map[string]int64)
	peerEventCounts := make(map[string]int64)

	mnEventCounts := make(map[string]*mnAccumHelper)
	rejectReasons := make(map[string]int64)
	statusChangeCounts := make(map[string]int64)
	// broadcastCountsByOutpoint counts every broadcast event with a non-empty
	// outpoint payload, regardless of whether the MN has been validated yet.
	// Folded into mnEventCounts[*].count post-loop, so MasternodeDetails
	// "Events" includes the pre-Accept activity (e.g. the original
	// broadcast_received that triggered validation) — which would otherwise
	// be dropped because the row doesn't exist yet during the pre-Accept scan.
	broadcastCountsByOutpoint := make(map[string]int64)
	var dsegTotalServed int64
	var totalEvents int64

	for _, path := range paths {
		if err := c.scanJSONLForSummary(path, s,
			uniqueOutpoints, sourceCounts, peerEventCounts,
			mnEventCounts, rejectReasons, statusChangeCounts,
			broadcastCountsByOutpoint,
			&dsegTotalServed, &totalEvents); err != nil {
			return nil, err
		}
	}

	// Fold per-outpoint broadcast event counts into the validated MN rows.
	// Row creation is still gated on TypeBroadcastAccepted (security per H-5);
	// only validated MNs end up in mnEventCounts. For each such row, we
	// override the locally-incremented count with the total observed activity
	// for its outpoint — capturing pre-Accept events as well as post-Accept.
	for outpoint, acc := range mnEventCounts {
		if total, ok := broadcastCountsByOutpoint[outpoint]; ok {
			acc.count = total
		}
	}

	// TotalEvents now reflects the cross-file scan — replaces the prior
	// session-local c.stats.Total which silently reset to 0 on restart and
	// ignored rotated files.
	s.TotalEvents = totalEvents

	// Compute derived values
	s.UniqueMasternodes = int64(len(uniqueOutpoints))
	s.UniquePeers = int64(len(peerEventCounts))

	// TierBreakdown reflects tier distribution of UNIQUE masternodes the node
	// has seen and validated (post-loop, after H-5 gates outpoint indexing on
	// TypeBroadcastAccepted). One increment per outpoint, not per accepted event.
	for _, acc := range mnEventCounts {
		if acc.tier != "" {
			s.TierBreakdown[acc.tier]++
		}
	}

	// Accept rate uses the validation-outcome denominator (Accepted + Rejected).
	// Each broadcast emits a *Received event followed by exactly one outcome
	// (*Accepted, *Rejected, *Dedup, or *Skipped) — summing Received with the
	// outcomes would double-count. Dedup/Skipped are upstream filtering, not
	// validation results, and have their own dedicated counters.
	if validated := s.BroadcastAccepted + s.BroadcastRejected; validated > 0 {
		s.AcceptRate = float64(s.BroadcastAccepted) / float64(validated) * 100
	}

	if pingValidated := s.PingAccepted + s.PingFailed; pingValidated > 0 {
		s.PingAcceptRate = float64(s.PingAccepted) / float64(pingValidated) * 100
	}

	if s.DSEGResponses > 0 {
		s.AvgMNsServed = float64(dsegTotalServed) / float64(s.DSEGResponses)
	}

	// Build sorted reject reasons (top 10)
	s.RejectReasons = buildTopN(rejectReasons, 10)

	// Build sorted top sources (top 5)
	s.TopSources = buildTopSources(sourceCounts, 5)

	// Build status changes
	s.StatusChanges = buildTopN(statusChangeCounts, 20)

	// Cap timeline slices at the LATEST maxTransitionsRows entries (sliding
	// window). Append-only growth across multi-week JSONLs would otherwise
	// produce hundreds of entries; capping preserves recent context without
	// blowing up the JSON payload over the Wails bridge every 3 s.
	if len(s.SyncTransitions) > maxTransitionsRows {
		s.SyncTransitions = s.SyncTransitions[len(s.SyncTransitions)-maxTransitionsRows:]
	}
	if len(s.ActiveMNChanges) > maxTransitionsRows {
		s.ActiveMNChanges = s.ActiveMNChanges[len(s.ActiveMNChanges)-maxTransitionsRows:]
	}

	// Ensure slices are non-nil for JSON
	if s.RejectReasons == nil {
		s.RejectReasons = []ReasonCount{}
	}
	if s.TopSources == nil {
		s.TopSources = []SourceCount{}
	}
	if s.SyncTransitions == nil {
		s.SyncTransitions = []StatusTransition{}
	}
	if s.StatusChanges == nil {
		s.StatusChanges = []ReasonCount{}
	}
	if s.ActiveMNChanges == nil {
		s.ActiveMNChanges = []StatusTransition{}
	}

	// Build peer detail list sorted by event count descending
	s.PeerDetails = buildPeerDetails(peerEventCounts)
	if s.PeerDetails == nil {
		s.PeerDetails = []PeerDetail{}
	}

	// Build masternode detail list sorted by event count descending
	s.MasternodeDetails = buildMasternodeDetails(mnEventCounts)
	if s.MasternodeDetails == nil {
		s.MasternodeDetails = []MasternodeDetail{}
	}

	return s, nil
}

// scanJSONLForSummary scans one JSONL file and folds its events into the
// shared aggregation state. Called once per file by Summary().
func (c *Collector) scanJSONLForSummary(
	path string,
	s *Summary,
	uniqueOutpoints map[string]struct{},
	sourceCounts map[string]int64,
	peerEventCounts map[string]int64,
	mnEventCounts map[string]*mnAccumHelper,
	rejectReasons map[string]int64,
	statusChangeCounts map[string]int64,
	broadcastCountsByOutpoint map[string]int64,
	dsegTotalServed *int64,
	totalEvents *int64,
) error {
	c.scanFileCount.Add(1)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("debug collector: failed to open %s for summary: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}

		*totalEvents++

		ts := event.Timestamp.Format("2006-01-02T15:04:05.000Z07:00")

		// Track time range
		if s.FirstEvent == "" {
			s.FirstEvent = ts
		}
		s.LastEvent = ts

		// Track unique peers across ALL categories. The previous
		// `event.Category != CategoryBroadcast` exclusion under-counted by a
		// large factor because most masternode network traffic is broadcasts.
		// `isNetworkAddress` already filters outpoint-shaped sources (txid:vout).
		if event.Source != "" && event.Source != "local" && isNetworkAddress(event.Source) {
			peerEventCounts[event.Source]++
		}

		// Parse payload once
		var payload map[string]any
		if len(event.Payload) > 0 {
			json.Unmarshal(event.Payload, &payload)
		}

		switch event.Category {
		case CategorySession:
			if event.Type == TypeSessionStart {
				s.SessionCount++
			}

		case CategoryBroadcast:
			if event.Source != "" && event.Source != "local" {
				sourceCounts[event.Source]++
			}
			// Always record this broadcast event against its outpoint, regardless
			// of validation status. Used post-loop to fold total event activity
			// into MasternodeDetails for validated MNs (so the "Events" column
			// includes pre-Accept events like the original broadcast_received).
			if outpoint, ok := payload["outpoint"].(string); ok && outpoint != "" {
				broadcastCountsByOutpoint[outpoint]++
			}
			switch event.Type {
			case TypeBroadcastReceived:
				s.BroadcastReceived++
			case TypeBroadcastAccepted:
				s.BroadcastAccepted++
				// Outpoint indexing is gated on TypeBroadcastAccepted (H-5):
				// only validated broadcasts contribute to UniqueMasternodes,
				// MasternodeDetails, and TierBreakdown. Rejected/dedup events
				// carry attacker-claimed payloads and must not seed these maps.
				if outpoint, ok := payload["outpoint"].(string); ok && outpoint != "" {
					uniqueOutpoints[outpoint] = struct{}{}
					tier, _ := payload["tier"].(string)
					addr, _ := payload["payee"].(string)
					if acc, exists := mnEventCounts[outpoint]; exists {
						acc.count++
						if tier != "" && acc.tier == "" {
							acc.tier = tier
						}
						if addr != "" && acc.addr == "" {
							acc.addr = addr
						}
					} else {
						mnEventCounts[outpoint] = &mnAccumHelper{addr: addr, tier: tier, count: 1}
					}
				}
			case TypeBroadcastRejected:
				s.BroadcastRejected++
				if reason, ok := payload["reason"].(string); ok && reason != "" {
					rejectReasons[reason]++
				} else if errStr, ok := payload["error"].(string); ok && errStr != "" {
					rejectReasons[errStr]++
				}
			case TypeBroadcastDedup:
				s.BroadcastDedup++
			case TypeBroadcastSkipped:
				s.BroadcastSkipped++
				// Skipped events do NOT feed rejectReasons — those are for validation failures only.
				// Skip reasons remain in the per-event payload for future Query() filtering.
			}

			// (Per-outpoint event count is tracked in broadcastCountsByOutpoint
			// at the top of this case and folded into mnEventCounts post-scan.
			// This avoids the chronological-order bug where a broadcast_received
			// that arrives BEFORE the broadcast_accepted in the same scan would
			// be dropped — the row didn't exist yet at the time of the receive.)

		case CategoryPing:
			switch event.Type {
			case TypePingReceived:
				s.PingReceived++
			case TypePingAccepted:
				s.PingAccepted++
			case TypePingRejected:
				s.PingFailed++
				if reason, ok := payload["reason"].(string); ok && reason != "" {
					rejectReasons[reason]++
				} else if errStr, ok := payload["error"].(string); ok && errStr != "" {
					rejectReasons[errStr]++
				}
			case TypePingSkipped:
				s.PingSkipped++
				// Skipped events do NOT feed rejectReasons.
			}
			// Unknown ping event types are silently ignored (e.g., test-only TypePingStageResult).

		case CategoryStatus:
			if event.Type == TypeStatusUpdate {
				label := event.Summary
				if prevStatus, ok := payload["prev_status"].(string); ok {
					if newStatus, ok2 := payload["new_status"].(string); ok2 {
						label = prevStatus + " → " + newStatus
					}
				}
				statusChangeCounts[label]++
			}

		case CategoryWinner:
			// winner events tracked by count only (already in Stats)

		case CategoryActive:
			switch event.Type {
			case "active_ping_sent":
				s.ActivePingsSent++
				if success, ok := payload["success"].(bool); ok {
					if success {
						s.ActivePingsSuccess++
					} else {
						s.ActivePingsFailed++
					}
				}
			case "active_state_change":
				from, _ := payload["prev_status"].(string)
				to, _ := payload["new_status"].(string)
				s.ActiveMNChanges = append(s.ActiveMNChanges, StatusTransition{
					Timestamp: ts,
					From:      from,
					To:        to,
				})
			}

		case CategoryNetwork:
			switch event.Type {
			case "network_mnb_received":
				s.NetworkMNBCount++
			case "network_mnp_received":
				s.NetworkMNPCount++
			case "dseg_request":
				s.DSEGRequests++
			case "dseg_response":
				s.DSEGResponses++
				if sentCount, ok := payload["sent_count"].(float64); ok {
					*dsegTotalServed += int64(sentCount)
				}
			}

		case CategorySync:
			if event.Type == TypeSyncStateChange {
				from, _ := payload["prev_state"].(string)
				to, _ := payload["new_state"].(string)
				s.SyncTransitions = append(s.SyncTransitions, StatusTransition{
					Timestamp: ts,
					From:      from,
					To:        to,
				})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("debug collector: summary scan error on %s: %w", path, err)
	}
	return nil
}

// buildTopN returns the top N entries from a count map, sorted by count descending.
func buildTopN(counts map[string]int64, n int) []ReasonCount {
	if len(counts) == 0 {
		return nil
	}
	result := make([]ReasonCount, 0, len(counts))
	for label, count := range counts {
		result = append(result, ReasonCount{Label: label, Count: count})
	}
	// Sort descending by count, alphabetical-ascending on label as tie-break
	// so output ordering is deterministic across map-iteration randomness.
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Label < result[j].Label
	})
	if len(result) > n {
		result = result[:n]
	}
	return result
}

// buildTopSources returns the top N source addresses by event count.
func buildTopSources(counts map[string]int64, n int) []SourceCount {
	if len(counts) == 0 {
		return nil
	}
	result := make([]SourceCount, 0, len(counts))
	for source, count := range counts {
		result = append(result, SourceCount{Source: source, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Source < result[j].Source
	})
	if len(result) > n {
		result = result[:n]
	}
	return result
}

// isNetworkAddress returns true if s looks like an IP:port address rather than an outpoint (txid:vout).
func isNetworkAddress(s string) bool {
	// IPv4 addresses contain dots, IPv6 addresses contain brackets
	return strings.Contains(s, ".") || strings.Contains(s, "[")
}

// buildPeerDetails returns all peer addresses with their event counts, sorted by count descending.
func buildPeerDetails(counts map[string]int64) []PeerDetail {
	if len(counts) == 0 {
		return nil
	}
	result := make([]PeerDetail, 0, len(counts))
	for addr, count := range counts {
		result = append(result, PeerDetail{Address: addr, EventCount: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].EventCount != result[j].EventCount {
			return result[i].EventCount > result[j].EventCount
		}
		return result[i].Address < result[j].Address
	})
	if len(result) > maxDetailsRows {
		result = result[:maxDetailsRows]
	}
	return result
}

// buildMasternodeDetails returns the top maxDetailsRows masternode outpoints with tier and event counts, sorted by count descending.
func buildMasternodeDetails(counts map[string]*mnAccumHelper) []MasternodeDetail {
	if len(counts) == 0 {
		return nil
	}
	result := make([]MasternodeDetail, 0, len(counts))
	for outpoint, acc := range counts {
		result = append(result, MasternodeDetail{Outpoint: outpoint, Address: acc.addr, Tier: acc.tier, EventCount: acc.count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].EventCount != result[j].EventCount {
			return result[i].EventCount > result[j].EventCount
		}
		return result[i].Outpoint < result[j].Outpoint
	})
	if len(result) > maxDetailsRows {
		result = result[:maxDetailsRows]
	}
	return result
}

// Query reads the JSONL file and returns filtered events.
// When Newest is true, results are returned in reverse chronological order (newest first).
// Otherwise, results are returned in chronological order (oldest first).
func (c *Collector) Query(filter Filter) (*QueryResult, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}

	c.mu.Lock()
	// Flush any buffered data before reading so newly-emitted events are
	// visible in the active file scan.
	if c.writer != nil {
		c.writer.Flush()
	}
	c.mu.Unlock()

	// Walk all rotated + active JSONL files in chronological order
	// (oldest rotated → ... → active). Mirrors Summary()'s file traversal
	// so the Events sub-tab data scope matches the Overview sub-tab.
	paths := c.listJSONLFilesChronological()

	result := &QueryResult{
		ByCategory:   make(map[string]int64),
		FilesScanned: len(paths),
	}

	// Cross-file FileSize via os.Stat sum (mirrors Summary.FileSize so the
	// GUI Events tab has a fresh value while fetchSummary is gated off).
	// Stat errors per-file are silently skipped — same tolerance as the
	// Summary path; missing files mean rotated entries that vanished
	// between listJSONLFilesChronological() and the os.Stat call.
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			result.FileSize += info.Size()
		}
	}

	// For Newest=true we need ALL matching events to find the most recent
	// `limit`. We use a ring buffer of capacity `limit` to bound memory:
	// append new matches; when full, drop the oldest match. After the scan
	// we hold the most-recent `limit` matches in chronological order.
	//
	// For Newest=false we simply append until we hit `limit`. We still
	// continue scanning to count TotalMatched / ByCategory accurately.
	for _, path := range paths {
		if err := c.scanFileForQuery(path, filter, limit, result); err != nil {
			return nil, err
		}
	}

	// Reverse for Newest=true so the caller receives newest-first.
	if filter.Newest {
		for i, j := 0, len(result.Events)-1; i < j; i, j = i+1, j-1 {
			result.Events[i], result.Events[j] = result.Events[j], result.Events[i]
		}
	}

	result.Truncated = result.TotalMatched > int64(len(result.Events))
	return result, nil
}

// scanFileForQuery reads a single JSONL file, applies `filter`, and folds
// matches into `result`. For Newest=true it maintains a bounded ring buffer
// (capacity `limit`) inside result.Events so peak memory stays constant
// regardless of how many files / events the cross-file scan walks. The
// result.Events buffer is shared across files in a single Query call.
func (c *Collector) scanFileForQuery(path string, filter Filter, limit int, result *QueryResult) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("debug collector: failed to open %s for query: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024) // 1MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			continue // skip malformed lines
		}

		result.TotalScanned++

		if !matchesFilter(event, filter) {
			continue
		}

		result.TotalMatched++
		result.ByCategory[event.Category]++

		if filter.Newest {
			// Ring buffer: when full, drop the oldest entry to make room.
			// Slice header reuse — `result.Events = append(result.Events[1:], event)`
			// would re-slice but reuse the underlying array.
			if len(result.Events) >= limit {
				result.Events = append(result.Events[1:], event)
			} else {
				result.Events = append(result.Events, event)
			}
		} else {
			// Oldest-first: stop appending once limit is reached but keep
			// scanning so TotalMatched / ByCategory reflect the full set.
			if len(result.Events) < limit {
				result.Events = append(result.Events, event)
			}
		}
	}

	return scanner.Err()
}

// Clear truncates the active debug log file AND removes any rotated files.
// Without removing rotated files, the all-time Summary() scan keeps showing
// stale events after a user-initiated Clear (M-6, dependency of H-3).
func (c *Collector) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Invalidate any in-flight Summary() singleflight group so callers that
	// arrive AFTER this Clear receive the post-Clear log state, not stale
	// pre-Clear results from a scan that started before the truncation.
	c.summaryGen.Add(1)

	if c.writer != nil {
		c.writer.Flush()
	}

	c.closeFileLocked()

	// Remove rotated files. We continue iterating even if a single Remove
	// fails (e.g. file locked on Windows by a concurrent Summary scan or an
	// external process), so as many files as possible are deleted. The first
	// error is captured and reported after the active-file truncate so the
	// caller still observes the failure, but the cleanup is not aborted on
	// the first hiccup — leaving N-1 rotated files on disk would defeat the
	// purpose of Clear() under the cross-file Summary scan from H-3.
	base := strings.TrimSuffix(c.filePath, ".jsonl")
	var firstRotateErr error
	for i := 1; i <= c.maxFiles; i++ {
		p := fmt.Sprintf("%s.%d.jsonl", base, i)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			if firstRotateErr == nil {
				firstRotateErr = fmt.Errorf("debug collector: failed to remove rotated %s: %w", p, err)
			}
		}
	}

	// Truncate the active file (re-create as zero-length).
	f, err := os.Create(c.filePath)
	if err != nil {
		return fmt.Errorf("debug collector: failed to clear: %w", err)
	}
	f.Close()

	// Reset stats and currentSize together under statsMu.
	c.statsMu.Lock()
	c.currentSize = 0
	c.stats.Total = 0
	c.stats.ByCategory = make(map[string]int64)
	c.statsMu.Unlock()

	// Re-open if enabled. Do this BEFORE returning firstRotateErr so the
	// collector is left in a usable state even when a rotated-file removal
	// failed — partial cleanup is still better than an aborted Clear that
	// leaves the writer detached.
	if c.enabled.Load() {
		if err := c.reopenFileLocked(); err != nil {
			return err
		}
	}

	// Surface the first rotated-file failure (if any) AFTER local state is
	// fully cleaned up and the file is reopened, so the caller observes the
	// error without losing the success of the active-file truncate.
	return firstRotateErr
}

// Close stops collection and releases resources.
func (c *Collector) Close() {
	c.Disable()
}

// rotateLocked rotates log files: mn-debug.jsonl → .1.jsonl → .2.jsonl → ...
// Caller must hold c.mu.
func (c *Collector) rotateLocked() {
	c.closeFileLocked()

	base := strings.TrimSuffix(c.filePath, ".jsonl")

	// Remove oldest file
	oldest := fmt.Sprintf("%s.%d.jsonl", base, c.maxFiles)
	os.Remove(oldest)

	// Shift existing rotated files
	for i := c.maxFiles - 1; i >= 1; i-- {
		oldName := fmt.Sprintf("%s.%d.jsonl", base, i)
		newName := fmt.Sprintf("%s.%d.jsonl", base, i+1)
		os.Rename(oldName, newName)
	}

	// Rotate current file to .1
	os.Rename(c.filePath, fmt.Sprintf("%s.1.jsonl", base))

	c.statsMu.Lock()
	c.currentSize = 0
	c.statsMu.Unlock()
	if err := c.reopenFileLocked(); err != nil {
		// Disable collection — subsequent Emit() calls will see writer==nil
		// and return early, preventing silent data loss.
		c.enabled.Store(false)
	}
}

// closeFileLocked closes the current file. Caller must hold c.mu.
func (c *Collector) closeFileLocked() {
	if c.writer != nil {
		c.writer.Flush()
		c.writer = nil
	}
	if c.file != nil {
		c.file.Close()
		c.file = nil
	}
}

// reopenFileLocked opens the log file for appending. Caller must hold c.mu.
func (c *Collector) reopenFileLocked() error {
	f, err := os.OpenFile(c.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	c.file = f
	c.writer = bufio.NewWriterSize(f, 64*1024)
	return nil
}

// matchesFilter checks if an event matches all filter criteria.
func matchesFilter(event Event, filter Filter) bool {
	if filter.Category != "" && event.Category != filter.Category {
		return false
	}
	if filter.Type != "" && event.Type != filter.Type {
		return false
	}
	if filter.Source != "" && event.Source != filter.Source {
		return false
	}
	if filter.Search != "" {
		searchLower := strings.ToLower(filter.Search)
		if !strings.Contains(strings.ToLower(event.Summary), searchLower) {
			return false
		}
	}
	return true
}
