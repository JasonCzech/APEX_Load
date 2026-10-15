package sample

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var markerBase = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

func at(seconds float64) time.Time {
	return markerBase.Add(time.Duration(seconds * float64(time.Second)))
}

// markerScript is one request per start time, named r0, r1, ...
func markerScript(starts ...float64) (*dsl.Script, []time.Time) {
	script := &dsl.Script{Protocol: "http"}
	times := make([]time.Time, len(starts))
	for index, start := range starts {
		script.Actions = append(script.Actions, dsl.Action{Name: fmt.Sprintf("r%d", index), Type: "request", Method: "GET", Target: "/"})
		times[index] = at(start)
	}
	return script, times
}

// layout names the script's actions in order: requests by name, markers as
// "+Name" (start) and "-Name" (end).
func layout(script *dsl.Script) string {
	parts := make([]string, 0, len(script.Actions))
	for _, action := range script.Actions {
		if action.Type != "source_transaction" {
			parts = append(parts, action.Name)
			continue
		}
		sign := "+"
		if transactionMarkerEnds(action) {
			sign = "-"
		}
		parts = append(parts, sign+dsl.TransactionMarkerName(action))
	}
	return strings.Join(parts, " ")
}

func TestInsertTransactionMarkersPlacesRecordedWindows(t *testing.T) {
	for _, test := range []struct {
		name    string
		starts  []float64
		windows []TransactionWindow
		want    string
	}{
		{
			name:   "sequential",
			starts: []float64{0, 1.5, 3.5, 5},
			windows: []TransactionWindow{
				{Name: "A", Start: at(1), End: at(2), StartOrder: 0, EndOrder: 1},
				{Name: "B", Start: at(3), End: at(4), StartOrder: 2, EndOrder: 3},
			},
			want: "r0 +A r1 -A +B r2 -B r3",
		},
		{
			name:   "nested",
			starts: []float64{1.5, 2.5, 4},
			windows: []TransactionWindow{
				{Name: "Outer", Start: at(1), End: at(5), StartOrder: 0, EndOrder: 3},
				{Name: "Inner", Start: at(2), End: at(3), StartOrder: 1, EndOrder: 2},
			},
			want: "+Outer r0 +Inner r1 -Inner r2 -Outer",
		},
		{
			name:   "overlapping",
			starts: []float64{1.5, 2.5, 3.5},
			windows: []TransactionWindow{
				{Name: "A", Start: at(1), End: at(3), StartOrder: 0, EndOrder: 2},
				{Name: "B", Start: at(2), End: at(4), StartOrder: 1, EndOrder: 3},
			},
			want: "+A r0 +B r1 -A r2 -B",
		},
		{
			name:   "same name twice",
			starts: []float64{1.5, 3.5},
			windows: []TransactionWindow{
				{Name: "Step", Start: at(1), End: at(2), StartOrder: 0, EndOrder: 1},
				{Name: "Step", Start: at(3), End: at(4), StartOrder: 2, EndOrder: 3},
			},
			want: "+Step r0 -Step +Step r1 -Step",
		},
		{
			// A window with no request still times the recorded gap, in its
			// recorded place between the requests around it.
			name:   "empty window between requests",
			starts: []float64{0, 5},
			windows: []TransactionWindow{
				{Name: "Think", Start: at(1), End: at(2), StartOrder: 0, EndOrder: 1},
			},
			want: "r0 +Think -Think r1",
		},
		{
			// A recording that ends with windows open closes them at the end,
			// the latest opened first.
			name:   "open at the end",
			starts: []float64{1.5, 2.5},
			windows: []TransactionWindow{
				{Name: "Outer", Start: at(1), StartOrder: 0},
				{Name: "Inner", Start: at(2), StartOrder: 1},
			},
			want: "+Outer r0 +Inner r1 -Inner -Outer",
		},
		{
			name:   "window boundaries are inclusive",
			starts: []float64{1, 2, 3},
			windows: []TransactionWindow{
				{Name: "A", Start: at(1), End: at(2), StartOrder: 0, EndOrder: 1},
			},
			want: "+A r0 r1 -A r2",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			script, starts := markerScript(test.starts...)
			positions, note := insertTransactionMarkers(script, starts, test.windows)
			if note != "" {
				t.Fatalf("note = %q", note)
			}
			if got := layout(script); got != test.want {
				t.Fatalf("layout = %q, want %q", got, test.want)
			}
			for original, position := range positions {
				if script.Actions[position].Name != fmt.Sprintf("r%d", original) {
					t.Fatalf("positions = %v: r%d is at %d (%s)", positions, original, position, script.Actions[position].Name)
				}
			}
		})
	}
}

// Markers carry the browser script's shape: named by their first argument,
// with the end taking LR_AUTO.
func TestInsertTransactionMarkersUseTheBrowserScriptShape(t *testing.T) {
	script, starts := markerScript(1.5)
	if _, note := insertTransactionMarkers(script, starts, []TransactionWindow{{Name: "Checkout", Start: at(1), End: at(2), EndOrder: 1}}); note != "" {
		t.Fatal(note)
	}
	start, end := script.Actions[0], script.Actions[2]
	if start.Type != "source_transaction" || start.Params["function"] != "lr_start_transaction" || dsl.TransactionMarkerName(start) != "Checkout" {
		t.Fatalf("start marker = %#v", start)
	}
	args, _ := end.Params["args"].([]any)
	if end.Params["function"] != "lr_end_transaction" || len(args) != 2 || args[0] != "Checkout" || args[1] != "LR_AUTO" {
		t.Fatalf("end marker = %#v", end)
	}
}

// A background conversation keeps running after the transaction its first
// request started in, so it neither opens nor closes one.
func TestInsertTransactionMarkersIgnoreBackgroundConversations(t *testing.T) {
	script, starts := markerScript(1.5, 0, 2.5, 0, 6)
	script.Actions[1] = dsl.Action{Name: "poll", Type: "http_async_start"}
	script.Actions[3] = dsl.Action{Name: "stop", Type: "http_async_stop"}
	starts[1] = at(1.6)
	starts[3] = time.Time{}
	if _, note := insertTransactionMarkers(script, starts, []TransactionWindow{
		{Name: "A", Start: at(1), End: at(3), EndOrder: 1},
		{Name: "B", Start: at(1.55), End: at(1.7), StartOrder: 2, EndOrder: 3},
	}); note != "" {
		t.Fatal(note)
	}
	// B held only the conversation's start, so it places as an empty window
	// before the next request; starting a conversation returns at once, so
	// either side of it times the same.
	if got := layout(script); got != "+A r0 poll +B -B r2 -A stop r4" {
		t.Fatalf("layout = %q", got)
	}
}

// The recorded pause after the last request of a transaction is not part of
// it: it moves past the ends placed there, before the next start.
func TestInsertTransactionMarkersMovePausePastEnds(t *testing.T) {
	script, starts := markerScript(1.5, 3.5, 6)
	pause := dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1800}
	script.Actions[0].ThinkTimeAfter, script.Actions[0].ThinkTime = true, pause
	script.Actions[1].ThinkTimeAfter, script.Actions[1].ThinkTime = true, pause
	if _, note := insertTransactionMarkers(script, starts, []TransactionWindow{
		{Name: "A", Start: at(1), End: at(2), StartOrder: 0, EndOrder: 1},
		{Name: "B", Start: at(2.5), End: at(5), StartOrder: 2, EndOrder: 3},
	}); note != "" {
		t.Fatal(note)
	}
	if got := layout(script); got != "+A r0 -A +B r1 -B r2" {
		t.Fatalf("layout = %q", got)
	}
	for index, want := range []bool{false, false, true, false, false, true, false} {
		if script.Actions[index].ThinkTimeAfter != want {
			t.Fatalf("action %d (%s) think time after = %t, want %t", index, script.Actions[index].Name, !want, want)
		}
	}
	if script.Actions[2].ThinkTime != pause {
		t.Fatalf("moved pause = %#v, want %#v", script.Actions[2].ThinkTime, pause)
	}

	// With only starts after it, the pause stays before them.
	script, starts = markerScript(0, 3.5)
	script.Actions[0].ThinkTimeAfter, script.Actions[0].ThinkTime = true, pause
	if _, note := insertTransactionMarkers(script, starts, []TransactionWindow{{Name: "B", Start: at(2), End: at(4), EndOrder: 1}}); note != "" {
		t.Fatal(note)
	}
	if !script.Actions[0].ThinkTimeAfter || script.Actions[1].ThinkTimeAfter {
		t.Fatalf("pause moved into the starting transaction: %#v", script.Actions[:2])
	}
}

func TestInsertTransactionMarkersNeedEveryRequestTime(t *testing.T) {
	script, starts := markerScript(1, 2)
	starts[1] = time.Time{}
	positions, note := insertTransactionMarkers(script, starts, []TransactionWindow{{Name: "A", Start: at(0)}})
	if positions != nil || !strings.Contains(note, "no recorded start time") || len(script.Actions) != 2 {
		t.Fatalf("positions = %v note = %q actions = %d, want the script untouched", positions, note, len(script.Actions))
	}
	positions, note = insertTransactionMarkers(script, starts[:1], []TransactionWindow{{Name: "A", Start: at(0)}})
	if positions != nil || !strings.Contains(note, "do not line up") {
		t.Fatalf("positions = %v note = %q, want a mismatch note", positions, note)
	}
	if positions, note := insertTransactionMarkers(script, starts, nil); positions != nil || note != "" {
		t.Fatalf("no windows: positions = %v note = %q", positions, note)
	}
}

// A converted recording keeps every action-indexed artifact aligned after its
// markers go in: the correlation baseline is keyed by each request's action
// index (through a background conversation's reshaping too) and source
// mappings still name their requests.
func TestConvertTimesRecordedTransactionsAndKeepsIndexesAligned(t *testing.T) {
	entry := func(seconds float64, method, path, mimeType, body string) string {
		started := at(seconds).Format(time.RFC3339Nano)
		post := ""
		if body != "" {
			post = `,"postData":{"mimeType":"application/json","text":` + jsonQuote(body) + `}`
		}
		return `{"startedDateTime":"` + started + `","time":20,"request":{"method":"` + method + `","url":"https://app.test` + path + `","httpVersion":"HTTP/1.1","headers":[{"name":"Content-Type","value":"application/json"}],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0` + post + `},` +
			`"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[{"name":"Content-Type","value":"` + mimeType + `"}],"cookies":[],"content":{"size":2,"mimeType":"` + mimeType + `","text":"{}"},"redirectURL":"","headersSize":-1,"bodySize":2},"cache":{},"timings":{"send":1,"wait":18,"receive":1}}`
	}
	har := `{"log":{"version":"1.2","entries":[` + strings.Join([]string{
		entry(0, "GET", "/home", "text/html", ""),
		entry(1, "GET", "/api/status?_=1", "application/json", ""),
		entry(2, "GET", "/api/status?_=2", "application/json", ""),
		entry(2.5, "POST", "/api/cart", "application/json", `{"sku":"synthetic-1"}`),
		entry(3, "GET", "/api/status?_=3", "application/json", ""),
		entry(5, "GET", "/next", "text/html", ""),
	}, ",") + `]}}`
	result, err := Convert(ImportRequest{Name: "Recorded", Format: "har", Content: har, Options: ConvertOptions{
		Transactions: []TransactionWindow{
			{Name: "Browse", Start: at(0), End: at(5.1), StartOrder: 0, EndOrder: 3},
			{Name: "Cart", Start: at(2.4), End: at(2.6), StartOrder: 1, EndOrder: 2},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Script
	if got := layoutByTarget(script); got != "+Browse /home async +Cart /api/cart -Cart stop /next -Browse" {
		t.Fatalf("layout = %q", got)
	}
	if cart := script.Actions[4]; cart.Target != "/api/cart" || cart.Metadata[harSourceTransactionMeta] != "Cart" {
		t.Fatalf("cart request = %q tagged %v, want /api/cart in Cart", cart.Target, cart.Metadata[harSourceTransactionMeta])
	}
	if len(result.Baseline) == 0 {
		t.Fatal("no recorded baseline")
	}
	for _, observation := range result.Baseline {
		if observation.ID < 0 || observation.ID >= len(script.Actions) {
			t.Fatalf("baseline ID %d is outside the script", observation.ID)
		}
		action := script.Actions[observation.ID]
		target := action.Target
		if action.Type == "http_async_start" {
			request, _ := action.Params["request"].(map[string]any)
			target, _ = request["target"].(string)
		}
		if action.Type == "source_transaction" || !strings.HasSuffix(observation.RequestURL, target) {
			t.Fatalf("baseline observation for %s keyed to action %d %q (%s)", observation.RequestURL, observation.ID, action.Name, action.Type)
		}
	}
	if result.SourceReview == nil {
		t.Fatal("no source review")
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.TargetKind != "action" {
			continue
		}
		index := -1
		if _, err := fmt.Sscanf(mapping.TargetPath, "actions[%d]", &index); err != nil || index < 0 || index >= len(script.Actions) {
			t.Fatalf("mapping path %q", mapping.TargetPath)
		}
		if action := script.Actions[index]; action.Type == "source_transaction" {
			t.Fatalf("mapping %q for %q points at marker %q", mapping.TargetPath, mapping.TargetName, action.Name)
		}
	}
}

func layoutByTarget(script *dsl.Script) string {
	parts := make([]string, 0, len(script.Actions))
	for _, action := range script.Actions {
		switch action.Type {
		case "source_transaction":
			sign := "+"
			if transactionMarkerEnds(action) {
				sign = "-"
			}
			parts = append(parts, sign+dsl.TransactionMarkerName(action))
		case "http_async_start":
			parts = append(parts, "async")
		case "http_async_stop":
			parts = append(parts, "stop")
		default:
			parts = append(parts, action.Target)
		}
	}
	return strings.Join(parts, " ")
}

func jsonQuote(value string) string {
	return fmt.Sprintf("%q", value)
}

func TestPlaceNotesAttachesEachNoteToTheNextRequest(t *testing.T) {
	exchanges := []exchange{
		{StartedAt: at(1), HasStartedAt: true},
		{StartedAt: at(3), HasStartedAt: true},
		{}, // no start time: never chosen
		{StartedAt: at(2), HasStartedAt: true},
	}
	placed, unplaced := placeNotes([]TimedNote{
		{Text: "after everything", At: at(9)},
		{Text: "before the first", At: at(0)},
		{Text: "between", At: at(1.5)},
		{Text: "same instant", At: at(2)},
	}, exchanges)
	want := map[int]string{0: "before the first", 3: "between; same instant", 1: "after everything"}
	if unplaced != 0 || fmt.Sprint(placed) != fmt.Sprint(want) {
		t.Fatalf("placed = %v (unplaced %d), want %v", placed, unplaced, want)
	}
	if placed, unplaced := placeNotes([]TimedNote{{Text: "x", At: at(0)}}, []exchange{{}}); placed != nil || unplaced != 1 {
		t.Fatalf("untimed exchanges: placed %v, unplaced %d", placed, unplaced)
	}
}
