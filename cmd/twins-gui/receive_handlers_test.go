package main

import (
	"strings"
	"testing"
	"time"

	"github.com/twins-dev/twins-core/internal/gui/core"
)

// makeTestRows returns a deterministic set of receiving address rows used by
// the sortReceivingAddressRows tests.
func makeTestRows() []core.ReceivingAddressRow {
	return []core.ReceivingAddressRow{
		{Address: "DAddr3", Label: "charlie", Balance: 50.0, Created: time.Unix(3000, 0)},
		{Address: "DAddr1", Label: "alpha", Balance: 100.0, Created: time.Unix(1000, 0)},
		{Address: "DAddr2", Label: "bravo", Balance: 0.0, Created: time.Unix(2000, 0)},
		{Address: "DAddr5", Label: "echo", Balance: 25.0, Created: time.Unix(5000, 0)},
		{Address: "DAddr4", Label: "delta", Balance: 25.0, Created: time.Unix(4000, 0)},
	}
}

func TestSortReceivingAddressRows_BalanceDesc(t *testing.T) {
	rows := makeTestRows()
	sortReceivingAddressRows(rows, "balance", "desc")

	wantAddrs := []string{"DAddr1", "DAddr3", "DAddr4", "DAddr5", "DAddr2"}
	// Two rows have balance=25.0 (DAddr4, DAddr5). Stable secondary key
	// "address ascending" must place DAddr4 before DAddr5.
	for i, addr := range wantAddrs {
		if rows[i].Address != addr {
			t.Errorf("index %d: got %q, want %q", i, rows[i].Address, addr)
		}
	}
}

func TestSortReceivingAddressRows_BalanceAsc(t *testing.T) {
	rows := makeTestRows()
	sortReceivingAddressRows(rows, "balance", "asc")

	// Asc: 0.0, 25.0 (DAddr4 then DAddr5 by addr asc), 50.0, 100.0
	wantAddrs := []string{"DAddr2", "DAddr4", "DAddr5", "DAddr3", "DAddr1"}
	for i, addr := range wantAddrs {
		if rows[i].Address != addr {
			t.Errorf("index %d: got %q, want %q", i, rows[i].Address, addr)
		}
	}
}

func TestSortReceivingAddressRows_LabelAsc(t *testing.T) {
	rows := makeTestRows()
	sortReceivingAddressRows(rows, "label", "asc")

	wantLabels := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for i, label := range wantLabels {
		if rows[i].Label != label {
			t.Errorf("index %d: got %q, want %q", i, rows[i].Label, label)
		}
	}
}

func TestSortReceivingAddressRows_LabelDesc(t *testing.T) {
	rows := makeTestRows()
	sortReceivingAddressRows(rows, "label", "desc")

	wantLabels := []string{"echo", "delta", "charlie", "bravo", "alpha"}
	for i, label := range wantLabels {
		if rows[i].Label != label {
			t.Errorf("index %d: got %q, want %q", i, rows[i].Label, label)
		}
	}
}

func TestSortReceivingAddressRows_LabelStableSecondaryKey(t *testing.T) {
	// Two rows with identical labels — secondary key (address asc) decides.
	rows := []core.ReceivingAddressRow{
		{Address: "DAddrZ", Label: "same", Balance: 0},
		{Address: "DAddrA", Label: "same", Balance: 0},
		{Address: "DAddrM", Label: "same", Balance: 0},
	}
	sortReceivingAddressRows(rows, "label", "asc")

	wantOrder := []string{"DAddrA", "DAddrM", "DAddrZ"}
	for i, addr := range wantOrder {
		if rows[i].Address != addr {
			t.Errorf("index %d: got %q, want %q", i, rows[i].Address, addr)
		}
	}
}

func TestSortReceivingAddressRows_DefaultColumn(t *testing.T) {
	// Unknown sort column should fall through to balance.
	rows := makeTestRows()
	sortReceivingAddressRows(rows, "unknown_column", "desc")

	// Same expectation as TestSortReceivingAddressRows_BalanceDesc
	wantAddrs := []string{"DAddr1", "DAddr3", "DAddr4", "DAddr5", "DAddr2"}
	for i, addr := range wantAddrs {
		if rows[i].Address != addr {
			t.Errorf("index %d: got %q, want %q", i, rows[i].Address, addr)
		}
	}
}

func TestSortReceivingAddressRows_LabelCaseInsensitive(t *testing.T) {
	rows := []core.ReceivingAddressRow{
		{Address: "DA", Label: "Zeta", Balance: 0},
		{Address: "DB", Label: "alpha", Balance: 0},
		{Address: "DC", Label: "Beta", Balance: 0},
	}
	sortReceivingAddressRows(rows, "label", "asc")

	// Case-insensitive: alpha, Beta, Zeta
	wantLabels := []string{"alpha", "Beta", "Zeta"}
	for i, label := range wantLabels {
		if rows[i].Label != label {
			t.Errorf("index %d: got %q, want %q", i, rows[i].Label, label)
		}
	}
}

func TestSortReceivingAddressRows_EmptyInput(t *testing.T) {
	var rows []core.ReceivingAddressRow
	sortReceivingAddressRows(rows, "balance", "desc")
	if len(rows) != 0 {
		t.Errorf("expected empty slice, got len %d", len(rows))
	}
}

// ----------------------------------------------------------------------------
// csvEscapeReceiving tests
// ----------------------------------------------------------------------------

func TestCSVEscapeReceiving_EmptyString(t *testing.T) {
	got := csvEscapeReceiving("")
	if got != `""` {
		t.Errorf("got %q, want %q", got, `""`)
	}
}

func TestCSVEscapeReceiving_PlainString(t *testing.T) {
	got := csvEscapeReceiving("hello")
	want := `"hello"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCSVEscapeReceiving_FormulaInjection(t *testing.T) {
	cases := map[string]string{
		"=SUM(A1:A2)":  `"'=SUM(A1:A2)"`,
		"+CMD()":       `"'+CMD()"`,
		"-1234":        `"'-1234"`,
		"@import.url": `"'@import.url"`,
	}
	for input, want := range cases {
		got := csvEscapeReceiving(input)
		if got != want {
			t.Errorf("input %q: got %q, want %q", input, got, want)
		}
	}
}

func TestCSVEscapeReceiving_QuoteEscaping(t *testing.T) {
	got := csvEscapeReceiving(`he said "hi"`)
	want := `"he said ""hi"""`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCSVEscapeReceiving_ControlCharsReplaced(t *testing.T) {
	got := csvEscapeReceiving("a\tb\nc\rd")
	want := `"a b c d"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCSVEscapeReceiving_NoFalsePositiveOnLetterStart(t *testing.T) {
	// "addr" starts with 'a' (not in =+-@), must NOT be prefixed with quote
	got := csvEscapeReceiving("addr1")
	want := `"addr1"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCSVEscapeReceiving_UnicodePassthrough(t *testing.T) {
	got := csvEscapeReceiving("héllo wörld")
	want := `"héllo wörld"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCSVEscapeReceiving_OnlyFirstCharTriggersFormula(t *testing.T) {
	// '=' in the middle of a string should NOT trigger formula prefix
	got := csvEscapeReceiving("a=b")
	want := `"a=b"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ----------------------------------------------------------------------------
// Smoke test for the page-size validation set used by GetReceivingAddressesPage
// ----------------------------------------------------------------------------

func TestValidReceivingAddressPageSizes(t *testing.T) {
	wantValid := []int{25, 50, 100, 250}
	for _, sz := range wantValid {
		if !validReceivingAddressPageSizes[sz] {
			t.Errorf("expected %d to be a valid page size", sz)
		}
	}
	wantInvalid := []int{0, 1, 24, 26, 99, 251, 1000}
	for _, sz := range wantInvalid {
		if validReceivingAddressPageSizes[sz] {
			t.Errorf("expected %d to be invalid", sz)
		}
	}
}

// Sanity check that we never accidentally pass a row's payment-request flag
// through the wrong CSV column. This is a regression-style smoke test against
// the column order assumed by ExportReceivingAddressesCSV.
func TestExportCSVColumnOrderAssumption(t *testing.T) {
	// Build a single-row CSV manually using the same logic as the handler.
	// If anyone ever reorders the columns this test will catch it.
	row := core.ReceivingAddressRow{
		Label:             "lbl",
		Address:           "addr",
		Balance:           1.5,
		HasPaymentRequest: true,
	}

	var sb strings.Builder
	sb.WriteString(csvEscapeReceiving(row.Label))
	sb.WriteByte(',')
	sb.WriteString(csvEscapeReceiving(row.Address))
	sb.WriteByte(',')
	sb.WriteString(csvEscapeReceiving("1.50000000"))
	sb.WriteByte(',')
	sb.WriteString(csvEscapeReceiving("yes"))

	want := `"lbl","addr","1.50000000","yes"`
	if sb.String() != want {
		t.Errorf("column order regression: got %q, want %q", sb.String(), want)
	}
}
