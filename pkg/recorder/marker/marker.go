// Package marker keeps the transaction and note markers an operator adds
// while recording. The browser recorder and the proxy recorder share it, so a
// transaction marked in either converts the same way.
package marker

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Marker kinds.
const (
	KindTransactionStart = "transaction_start"
	KindTransactionEnd   = "transaction_end"
	KindNote             = "note"
)

// Marker is a user-inserted annotation in the recording timeline: transaction
// boundaries convert to source_transaction actions, notes attach to the next
// converted action's metadata.
type Marker struct {
	Kind      string    `json:"kind"`
	Name      string    `json:"name,omitempty"` // transaction name
	Text      string    `json:"text,omitempty"` // note text
	Timestamp time.Time `json:"timestamp"`
	// Auto marks markers the recorder inserted itself (open transactions
	// closed at stop).
	Auto bool `json:"auto,omitempty"`
}

// Limits bounds what one recording retains. User markers count against the
// User limits; Auto markers (the recorder's own) may use the Total headroom.
type Limits struct {
	UserEntries  int
	TotalEntries int
	NameBytes    int
	TextBytes    int
	UserBytes    int
	TotalBytes   int
}

// DefaultLimits are the recorders' marker limits.
var DefaultLimits = Limits{
	UserEntries:  10_000,
	TotalEntries: 20_000,
	NameBytes:    4 << 10,
	TextBytes:    16 << 10,
	UserBytes:    16 << 20,
	TotalBytes:   32 << 20,
}

// Book is a recording's markers, the transactions still open, and the marker
// bytes retained. It is not goroutine-safe: recorders hold their own lock.
type Book struct {
	Markers          []Marker
	OpenTransactions []string
	RetainedBytes    int
}

// Add validates and appends a marker, maintaining open-transaction state. A
// transaction end with no name closes the innermost open transaction.
func (b *Book) Add(marker Marker, limits Limits) (Marker, error) {
	marker.Kind = strings.TrimSpace(marker.Kind)
	marker.Name = strings.TrimSpace(marker.Name)
	marker.Text = strings.TrimSpace(marker.Text)
	if marker.Kind == KindTransactionEnd && marker.Name == "" && len(b.OpenTransactions) > 0 {
		// The innermost open transaction, named before its bytes are counted.
		marker.Name = b.OpenTransactions[len(b.OpenTransactions)-1]
	}
	entryLimit := limits.UserEntries
	byteLimit := limits.UserBytes
	if marker.Auto {
		entryLimit = limits.TotalEntries
		byteLimit = limits.TotalBytes
	}
	if len(b.Markers) >= entryLimit {
		return Marker{}, fmt.Errorf("recording marker limit reached")
	}
	if len(marker.Name) > limits.NameBytes {
		return Marker{}, fmt.Errorf("marker name exceeds %d bytes", limits.NameBytes)
	}
	if len(marker.Text) > limits.TextBytes {
		return Marker{}, fmt.Errorf("marker text exceeds %d bytes", limits.TextBytes)
	}
	markerBytes := len(marker.Kind) + len(marker.Name) + len(marker.Text)
	if markerBytes > byteLimit-b.RetainedBytes {
		return Marker{}, fmt.Errorf("recording marker byte limit reached")
	}
	switch marker.Kind {
	case KindTransactionStart:
		if marker.Name == "" {
			return Marker{}, fmt.Errorf("transaction name is required")
		}
		if slices.Contains(b.OpenTransactions, marker.Name) {
			return Marker{}, fmt.Errorf("transaction %q is already open", marker.Name)
		}
		b.OpenTransactions = append(b.OpenTransactions, marker.Name)
	case KindTransactionEnd:
		if len(b.OpenTransactions) == 0 {
			return Marker{}, fmt.Errorf("no transaction is open")
		}
		index := slices.Index(b.OpenTransactions, marker.Name)
		if index < 0 {
			return Marker{}, fmt.Errorf("transaction %q is not open", marker.Name)
		}
		b.OpenTransactions = slices.Delete(b.OpenTransactions, index, index+1)
	case KindNote:
		if marker.Text == "" {
			return Marker{}, fmt.Errorf("note text is required")
		}
	default:
		return Marker{}, fmt.Errorf("unsupported marker kind %q", marker.Kind)
	}
	if marker.Timestamp.IsZero() {
		marker.Timestamp = time.Now()
	}
	b.Markers = append(b.Markers, marker)
	b.RetainedBytes += markerBytes
	return marker, nil
}

// CloseOpen ends every open transaction at the given time, innermost first,
// with Auto markers. It returns a warning for each one, so the recording says
// which transactions it closed or could not close.
func (b *Book) CloseOpen(at time.Time, limits Limits) []string {
	open := slices.Clone(b.OpenTransactions)
	var warnings []string
	for index := len(open) - 1; index >= 0; index-- {
		name := open[index]
		if _, err := b.Add(Marker{Kind: KindTransactionEnd, Name: name, Timestamp: at, Auto: true}, limits); err != nil {
			warnings = append(warnings, fmt.Sprintf("transaction %q could not be auto-closed: %v", name, err))
			continue
		}
		warnings = append(warnings, fmt.Sprintf("transaction %q was still open and was closed automatically when the recording stopped", name))
	}
	b.OpenTransactions = nil
	return warnings
}

// Window is one transaction marked while recording, from its start marker to
// its end marker. End is zero when the recording ended with it open.
// StartOrder and EndOrder place its markers in the recorded marker sequence,
// which orders markers that fall between the same two requests.
type Window struct {
	Name       string
	Start      time.Time
	End        time.Time
	StartOrder int
	EndOrder   int
}

// Windows pairs transaction markers in recorded order: an end closes the
// latest open transaction of its name.
func Windows(markers []Marker) []Window {
	sorted := slices.Clone(markers)
	slices.SortStableFunc(sorted, func(a, b Marker) int { return a.Timestamp.Compare(b.Timestamp) })
	var windows []Window
	open := map[string][]int{}
	for order, marker := range sorted {
		switch marker.Kind {
		case KindTransactionStart:
			open[marker.Name] = append(open[marker.Name], len(windows))
			windows = append(windows, Window{Name: marker.Name, Start: marker.Timestamp, StartOrder: order})
		case KindTransactionEnd:
			if stack := open[marker.Name]; len(stack) > 0 {
				window := &windows[stack[len(stack)-1]]
				window.End, window.EndOrder = marker.Timestamp, order
				open[marker.Name] = stack[:len(stack)-1]
			}
		}
	}
	return windows
}

// Note is a note written while recording, at the time it was written.
type Note struct {
	Text string
	At   time.Time
}

// Notes returns the recording's notes in the order they were written.
func Notes(markers []Marker) []Note {
	var notes []Note
	for _, marker := range markers {
		if marker.Kind == KindNote {
			notes = append(notes, Note{Text: marker.Text, At: marker.Timestamp})
		}
	}
	return notes
}
