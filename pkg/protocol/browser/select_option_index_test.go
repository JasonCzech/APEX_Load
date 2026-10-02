package browser

import (
	"math"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestSelectActionOptionsUsesZeroBasedIndexAndRejectsAmbiguity(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   any
		values  []string
		want    int
		invalid bool
	}{
		{name: "first", value: 0, want: 0}, {name: "third decoded", value: float64(2), want: 2}, {name: "maximum", value: 999999, want: 999999},
		{name: "negative", value: -1, invalid: true}, {name: "limit", value: 1000000, invalid: true}, {name: "fraction", value: 1.5, invalid: true}, {name: "infinite", value: math.Inf(1), invalid: true}, {name: "NaN", value: math.NaN(), invalid: true}, {name: "combined", value: 0, values: []string{"Label"}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			selection, err := selectActionOptions(&protocol.Action{Params: map[string]any{"option_index": test.value}}, test.values)
			if (err != nil) != test.invalid {
				t.Fatalf("err=%v invalid=%v", err, test.invalid)
			}
			if !test.invalid {
				if selection.Indexes == nil || len(*selection.Indexes) != 1 || (*selection.Indexes)[0] != test.want || selection.ValuesOrLabels != nil {
					t.Fatalf("selection=%+v", selection)
				}
			}
		})
	}
	selection, err := selectActionOptions(&protocol.Action{}, []string{"Label"})
	if err != nil || selection.Indexes != nil || selection.ValuesOrLabels == nil || (*selection.ValuesOrLabels)[0] != "Label" {
		t.Fatalf("existing label selection=%+v err=%v", selection, err)
	}
}
