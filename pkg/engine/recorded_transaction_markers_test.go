package engine

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/uniquify"
	"github.com/jasonczech/apexload/pkg/dsl"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/selector"
)

// A browser recording's transactions must still open and close what they name
// after import de-duplication. Their end markers once shared the start's step
// name, were renamed "<name>_2" and closed nothing, so debugging a recorded
// Tableau Public walk failed every transaction. A legacy pair without
// arguments is pinned to its name when renamed.
func TestRecordedTransactionMarkersPairAfterImportDeduplication(t *testing.T) {
	base := time.UnixMilli(1_790_000_000_000)
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	button := &selector.ElementMetadata{Tag: "button", ID: "go", CSSPath: "#go"}
	session := &recbrowser.Session{ID: "rec_pair", StartURL: "https://shop.example/", StartedAt: base}
	session.Markers = []recbrowser.Marker{
		{Kind: recbrowser.MarkerTransactionStart, Name: "Discover", Timestamp: at(100)},
		{Kind: recbrowser.MarkerTransactionEnd, Name: "Discover", Timestamp: at(200)},
		{Kind: recbrowser.MarkerTransactionStart, Name: "Search", Timestamp: at(300)},
		{Kind: recbrowser.MarkerTransactionEnd, Name: "Search", Timestamp: at(500)},
	}
	session.Steps = []recbrowser.Step{{Seq: 1, Kind: recbrowser.StepClick, Timestamp: at(400), Element: button, Candidates: selector.Candidates(*button)}}
	script := recbrowser.Convert(session, recbrowser.ConvertOptions{}).Script
	script.Actions = append(script.Actions,
		dsl.Action{Name: "Legacy", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction"}},
		dsl.Action{Name: "Legacy", Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction"}},
	)
	uniquify.DeduplicateActionNames(script)

	sink := &sequenceCaptureSink{}
	vu := &VUser{Variables: map[string]string{}, ResultSink: sink}
	for _, action := range script.Actions {
		if action.Type != "source_transaction" {
			continue
		}
		if err := vu.handleSourceTransactionAction(action); err != nil {
			t.Fatalf("%s: %v", action.Name, err)
		}
	}
	var closed []string
	for _, transaction := range sink.transactions {
		closed = append(closed, transaction.ActionName)
	}
	if want := []string{"Discover", "Search", "Legacy"}; !reflect.DeepEqual(closed, want) || len(vu.sourceTransactions) != 0 {
		t.Fatalf("closed transactions = %v (still open %v), want %v", closed, vu.sourceTransactions, want)
	}
}

// A recording saved before its markers named their transaction has an end
// renamed "<name>_2"; the failure says how to repair it.
func TestRenamedTransactionEndExplainsTheRepair(t *testing.T) {
	vu := &VUser{Variables: map[string]string{}}
	if err := vu.handleSourceTransactionAction(dsl.Action{Name: "Checkout", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction"}}); err != nil {
		t.Fatal(err)
	}
	err := vu.handleSourceTransactionAction(dsl.Action{Name: "Checkout_2", Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction"}})
	if err == nil || !strings.Contains(err.Error(), `an import renamed this step from "Checkout": set its first argument to "Checkout"`) {
		t.Fatalf("error = %v, want the repair hint", err)
	}
	for _, name := range []string{"Checkout_", "Payment_2", "Checkout_x2"} {
		err := vu.handleSourceTransactionAction(dsl.Action{Name: name, Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction"}})
		if err == nil || strings.Contains(err.Error(), "renamed") {
			t.Fatalf("%s: error = %v, want no repair hint", name, err)
		}
	}
}
