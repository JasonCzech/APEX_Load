package har

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/sample"
)

// Requests carry the recorded transaction open when they started; nested
// transactions tag the innermost, and a request outside every window stays
// untagged. Start and end markers time each window around its requests.
func TestConvertHARTagsRecordedTransactions(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entry := func(seconds int, path string) string {
		return fmt.Sprintf(`{"startedDateTime": %q, "time": 10,
      "request": {"method": "GET", "url": "https://shop.example.test%s", "headers": []},
      "response": {"status": 200, "headers": [{"name": "Content-Type", "value": "application/json"}], "content": {"text": "{}"}}}`,
			base.Add(time.Duration(seconds)*time.Second).Format(time.RFC3339Nano), path)
	}
	content := `{"log": {"entries": [` + strings.Join([]string{
		entry(1, "/home"), entry(3, "/cart"), entry(5, "/pay"), entry(7, "/receipt"), entry(9, "/after"),
	}, ",") + `]}}`
	at := func(seconds int) time.Time { return base.Add(time.Duration(seconds) * time.Second) }
	result, err := Convert([]byte(content), ConvertOptions{
		AsyncScan: new(bool),
		Transactions: []sample.TransactionWindow{
			{Name: "Checkout", Start: at(2), End: at(8)},
			{Name: "Pay", Start: at(4), End: at(6)},
			{Name: "Open", Start: at(10)},
		},
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	want := map[string]any{"/home": nil, "/cart": "Checkout", "/pay": "Pay", "/receipt": "Checkout", "/after": nil}
	var layout []string
	for _, action := range result.Script.Actions {
		if action.Type == "source_transaction" {
			layout = append(layout, action.Name)
			continue
		}
		layout = append(layout, action.Target)
		if got := action.Metadata["source_transaction"]; got != want[action.Target] {
			t.Errorf("%s transaction = %v, want %v", action.Target, got, want[action.Target])
		}
	}
	wantLayout := "/home|Start Checkout transaction|/cart|Start Pay transaction|/pay|End Pay transaction|/receipt|End Checkout transaction|/after|Start Open transaction|End Open transaction"
	if got := strings.Join(layout, "|"); got != wantLayout {
		t.Fatalf("actions = %s\nwant %s", got, wantLayout)
	}
}
