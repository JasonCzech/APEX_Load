package dsl

import "testing"

func TestUniqueRowAllocation(t *testing.T) {
	for _, tc := range []struct {
		name            string
		param           Parameter
		auto, allocates bool
	}{
		{"unique list without settings", Parameter{Type: "list", Selection: "unique"}, true, true},
		{"unique file without settings", Parameter{Type: "file", Selection: "unique"}, true, true},
		{"explicit auto_allocate", Parameter{Type: "list", Selection: "unique", AutoAllocate: true}, true, true},
		{"explicit block size", Parameter{Type: "list", Selection: "unique", BlockSize: 3}, false, true},
		{"one value per VUser", Parameter{Type: "list", Selection: "unique", ValueForEachVUser: true}, false, false},
		{"unique counter", Parameter{Type: "counter", Selection: "unique", BlockSize: 5}, false, true},
		{"sequential list", Parameter{Type: "list", Selection: "sequential"}, false, false},
		{"default selection", Parameter{Type: "file"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AutoAllocatesUniqueRows(tc.param); got != tc.auto {
				t.Errorf("AutoAllocatesUniqueRows = %v, want %v", got, tc.auto)
			}
			if got := AllocatesUniqueRows(tc.param); got != tc.allocates {
				t.Errorf("AllocatesUniqueRows = %v, want %v", got, tc.allocates)
			}
		})
	}
	if !RequiresLRFidelity(&Script{Parameters: []Parameter{{Type: "list", Selection: "unique"}}}) {
		t.Error("a unique list without settings must require generators that plan unique allocations")
	}
}
