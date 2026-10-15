package marker

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBookAddValidatesMarkers(t *testing.T) {
	var book Book
	for _, tc := range []struct {
		name   string
		marker Marker
		want   string
	}{
		{name: "start without name", marker: Marker{Kind: KindTransactionStart, Name: "  "}, want: "name is required"},
		{name: "end with none open", marker: Marker{Kind: KindTransactionEnd}, want: "no transaction is open"},
		{name: "note without text", marker: Marker{Kind: KindNote}, want: "text is required"},
		{name: "unknown kind", marker: Marker{Kind: "pause"}, want: "unsupported marker kind"},
		{name: "long name", marker: Marker{Kind: KindTransactionStart, Name: strings.Repeat("n", DefaultLimits.NameBytes+1)}, want: "name exceeds"},
	} {
		if _, err := book.Add(tc.marker, DefaultLimits); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want %q", tc.name, err, tc.want)
		}
	}
	if len(book.Markers) != 0 || book.RetainedBytes != 0 {
		t.Fatalf("rejected markers were retained: %#v", book)
	}
}

func TestBookAddTracksOpenTransactions(t *testing.T) {
	var book Book
	add := func(m Marker) Marker {
		t.Helper()
		added, err := book.Add(m, DefaultLimits)
		if err != nil {
			t.Fatalf("Add(%#v) = %v", m, err)
		}
		return added
	}
	add(Marker{Kind: KindTransactionStart, Name: " Login "})
	add(Marker{Kind: KindTransactionStart, Name: "Authorize"})
	if _, err := book.Add(Marker{Kind: KindTransactionStart, Name: "Login"}, DefaultLimits); err == nil || !strings.Contains(err.Error(), "already open") {
		t.Fatalf("reopening an open transaction: %v", err)
	}
	if _, err := book.Add(Marker{Kind: KindTransactionEnd, Name: "Search"}, DefaultLimits); err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("ending a transaction that is not open: %v", err)
	}
	// An end without a name closes the innermost transaction.
	if ended := add(Marker{Kind: KindTransactionEnd}); ended.Name != "Authorize" || ended.Timestamp.IsZero() {
		t.Fatalf("unnamed end = %#v, want Authorize with a timestamp", ended)
	}
	add(Marker{Kind: KindNote, Text: " approve on the device "})
	if !slices.Equal(book.OpenTransactions, []string{"Login"}) {
		t.Fatalf("open transactions = %v", book.OpenTransactions)
	}
	if book.Markers[0].Name != "Login" || book.Markers[3].Text != "approve on the device" {
		t.Fatalf("markers were not trimmed: %#v", book.Markers)
	}
	if want := len("transaction_start")*2 + len("Login") + len("Authorize") + len("transaction_end") + len("Authorize") + len("note") + len("approve on the device"); book.RetainedBytes != want {
		t.Fatalf("retained bytes = %d, want %d", book.RetainedBytes, want)
	}
}

func TestBookUserLimitsLeaveHeadroomForAutoMarkers(t *testing.T) {
	limits := Limits{UserEntries: 1, TotalEntries: 2, NameBytes: 64, TextBytes: 64, UserBytes: 1 << 10, TotalBytes: 2 << 10}
	var book Book
	if _, err := book.Add(Marker{Kind: KindTransactionStart, Name: "Login"}, limits); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Add(Marker{Kind: KindNote, Text: "one too many"}, limits); err == nil || !strings.Contains(err.Error(), "marker limit") {
		t.Fatalf("user marker past the user limit: %v", err)
	}
	if warnings := book.CloseOpen(time.Now(), limits); len(warnings) != 1 || !strings.Contains(warnings[0], "closed automatically") {
		t.Fatalf("auto-close past the user limit: %v", warnings)
	}
	if len(book.Markers) != 2 || !book.Markers[1].Auto {
		t.Fatalf("markers = %#v, want the auto end", book.Markers)
	}
}

func TestBookCloseOpenEndsInnermostFirstAndReportsFailures(t *testing.T) {
	var book Book
	for _, name := range []string{"Outer", "Inner"} {
		if _, err := book.Add(Marker{Kind: KindTransactionStart, Name: name}, DefaultLimits); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	warnings := book.CloseOpen(at, DefaultLimits)
	if len(warnings) != 2 || !strings.Contains(warnings[0], `"Inner"`) || !strings.Contains(warnings[1], `"Outer"`) {
		t.Fatalf("warnings = %v, want Inner then Outer closed", warnings)
	}
	if len(book.OpenTransactions) != 0 {
		t.Fatalf("open transactions after close = %v", book.OpenTransactions)
	}
	for _, m := range book.Markers[2:] {
		if m.Kind != KindTransactionEnd || !m.Auto || !m.Timestamp.Equal(at) {
			t.Fatalf("auto end = %#v", m)
		}
	}

	full := Book{OpenTransactions: []string{"Stuck"}, Markers: make([]Marker, DefaultLimits.TotalEntries)}
	if warnings := full.CloseOpen(at, DefaultLimits); len(warnings) != 1 || !strings.Contains(warnings[0], "could not be auto-closed") {
		t.Fatalf("warnings at the total limit = %v", warnings)
	}
	if len(full.OpenTransactions) != 0 {
		t.Fatalf("open transactions after a failed close = %v", full.OpenTransactions)
	}
}

func TestWindowsPairsMarkersInRecordedOrder(t *testing.T) {
	base := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return base.Add(time.Duration(seconds) * time.Second) }
	windows := Windows([]Marker{
		{Kind: KindTransactionEnd, Name: "Login", Timestamp: at(5)},
		{Kind: KindTransactionStart, Name: "Login", Timestamp: at(1)},
		{Kind: KindNote, Text: "ignored", Timestamp: at(2)},
		{Kind: KindTransactionStart, Name: "Token", Timestamp: at(3)},
		{Kind: KindTransactionStart, Name: "Login", Timestamp: at(6)},
		{Kind: KindTransactionEnd, Name: "Unknown", Timestamp: at(7)},
	})
	want := []Window{
		{Name: "Login", Start: at(1), End: at(5), StartOrder: 0, EndOrder: 3},
		{Name: "Token", Start: at(3), StartOrder: 2},
		{Name: "Login", Start: at(6), StartOrder: 4},
	}
	if !slices.Equal(windows, want) {
		t.Fatalf("windows = %#v\nwant %#v", windows, want)
	}
}
