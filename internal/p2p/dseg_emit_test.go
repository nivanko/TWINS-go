package p2p

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/twins-dev/twins-core/internal/masternode/debug"
)

// TestEmitDSEGRequest_OutboundAndInbound verifies N-1 from the
// masternode-debug-tab research audit: dseg_request events are emitted with
// a "direction" field that distinguishes inbound (peer asking us) from
// outbound (we asking a peer). Pre-fix, only inbound emitted, so the GUI's
// DSEGRequests count understated by 100% of the outbound dseg sends.
//
// Server.emitDSEGRequest is the single emit site shared by all three call
// paths (AskForMN outbound, RequestMasternodeList outbound full list,
// handleDSEG inbound). This test exercises the helper directly with a real
// debug.Collector backed by a temp dir, then asserts the JSONL contains
// the expected events with the correct direction field.
func TestEmitDSEGRequest_OutboundAndInbound(t *testing.T) {
	dir := t.TempDir()
	col := debug.NewCollector(dir, 1, 3)
	if err := col.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	defer col.Close()

	srv := &Server{}
	srv.debugCollector.Store(col)

	srv.emitDSEGRequest("10.0.0.1:37817", "abc:0", "out", 0)
	srv.emitDSEGRequest("10.0.0.2:37817", "", "out", 0)
	srv.emitDSEGRequest("10.0.0.3:37817", "", "in", 36)

	// Wait briefly for the buffered writer to flush via Summary().
	time.Sleep(50 * time.Millisecond)

	summary, err := col.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if summary.DSEGRequests != 3 {
		t.Errorf("DSEGRequests = %d, want 3 (1 specific outbound + 1 full-list outbound + 1 inbound)", summary.DSEGRequests)
	}

	jsonl, err := os.ReadFile(filepath.Join(dir, "mn-debug.jsonl"))
	if err != nil {
		t.Fatalf("read jsonl: %v", err)
	}
	got := string(jsonl)
	for _, want := range []string{
		`"direction":"out"`,
		`"direction":"in"`,
		`"outpoint":"abc:0"`,
		`"peer":"10.0.0.1:37817"`,
		`"peer":"10.0.0.2:37817"`,
		`"peer":"10.0.0.3:37817"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("JSONL missing %q", want)
		}
	}
}
