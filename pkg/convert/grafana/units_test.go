package grafana

import "testing"

func TestMapUnitFamilies(t *testing.T) {
	cases := []struct {
		id         string
		label      string
		recognized bool
		hasNote    bool
	}{
		{"none", "", true, false},
		{"short", "", true, false},
		{"", "", true, false},
		{"percent", "%", true, false},
		{"percentunit", "%", true, true},
		{"ms", "ms", true, false},
		{"s", "s", true, false},
		{"bytes", "bytes", true, false},
		{"decbytes", "B", true, false},
		{"Bps", "B/s", true, false},
		{"reqps", "req/s", true, false},
		{"celsius", "°C", true, false},
		{"currencyUSD", "USD", true, false},
		{"bool", "", true, true},
		{"totallyunknown", "", false, false},
	}
	for _, c := range cases {
		got := mapUnit(c.id)
		if got.Label != c.label {
			t.Errorf("mapUnit(%q).Label = %q, want %q", c.id, got.Label, c.label)
		}
		if got.Recognized != c.recognized {
			t.Errorf("mapUnit(%q).Recognized = %v, want %v", c.id, got.Recognized, c.recognized)
		}
		if (got.Note != "") != c.hasNote {
			t.Errorf("mapUnit(%q).Note presence = %v, want %v (note=%q)", c.id, got.Note != "", c.hasNote, got.Note)
		}
	}
}
