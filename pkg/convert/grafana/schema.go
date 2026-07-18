package grafana

import (
	"encoding/json"
	"strings"
)

// The raw* structs mirror the subset of the Grafana dashboard JSON schema that the
// importer understands. Polymorphic fields (datasource, variable query/value) are
// kept as json.RawMessage and decoded with dedicated helpers, because their shape
// changes across schema versions.

type rawEnvelope struct {
	// Dashboard is populated when the JSON is the /api/dashboards/uid/:uid response
	// envelope ({"dashboard": {...}, "meta": {...}}) that users commonly paste. When
	// it is absent the top-level object *is* the dashboard.
	Dashboard json.RawMessage `json:"dashboard"`
}

type rawDashboard struct {
	Title         string        `json:"title"`
	UID           string        `json:"uid"`
	SchemaVersion int           `json:"schemaVersion"`
	Panels        []rawPanel    `json:"panels"`
	Templating    rawTemplating `json:"templating"`
}

type rawPanel struct {
	ID              int               `json:"id"`
	Type            string            `json:"type"`
	Title           string            `json:"title"`
	Datasource      json.RawMessage   `json:"datasource"`
	GridPos         rawGridPos        `json:"gridPos"`
	Targets         []rawTarget       `json:"targets"`
	Panels          []rawPanel        `json:"panels"` // children of a collapsed row
	FieldConfig     rawFieldConfig    `json:"fieldConfig"`
	Repeat          json.RawMessage   `json:"repeat"`
	Links           []json.RawMessage `json:"links"`
	Transformations []json.RawMessage `json:"transformations"`
}

type rawGridPos struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type rawTarget struct {
	RefID        string          `json:"refId"`
	Expr         string          `json:"expr"`
	LegendFormat string          `json:"legendFormat"`
	Hide         bool            `json:"hide"`
	Datasource   json.RawMessage `json:"datasource"`
	// RawSQL/Query flag non-Prometheus target shapes (Loki/SQL datasources) so the
	// converter can reject them with an honest diagnostic instead of a query failure.
	RawSQL string `json:"rawSql"`
}

type rawFieldConfig struct {
	Defaults struct {
		Unit       string            `json:"unit"`
		Thresholds rawThresholds     `json:"thresholds"`
		Mappings   []json.RawMessage `json:"mappings"`
		Links      []json.RawMessage `json:"links"`
	} `json:"defaults"`
	Overrides []rawOverride `json:"overrides"`
}

type rawThresholds struct {
	Mode  string             `json:"mode"`
	Steps []rawThresholdStep `json:"steps"`
}

type rawThresholdStep struct {
	Color string   `json:"color"`
	Value *float64 `json:"value"`
}

type rawOverride struct {
	Properties []rawOverrideProperty `json:"properties"`
}

type rawOverrideProperty struct {
	ID    string          `json:"id"`
	Value json.RawMessage `json:"value"`
}

type rawTemplating struct {
	List []rawVariable `json:"list"`
}

type rawVariable struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Label      string            `json:"label"`
	Query      json.RawMessage   `json:"query"`
	Datasource json.RawMessage   `json:"datasource"`
	Current    rawVariablePick   `json:"current"`
	Options    []rawVariablePick `json:"options"`
	Multi      bool              `json:"multi"`
	IncludeAll bool              `json:"includeAll"`
}

type rawVariablePick struct {
	Value json.RawMessage `json:"value"`
	Text  json.RawMessage `json:"text"`
}

// datasourceRef is the normalized view of a panel/target datasource reference.
type datasourceRef struct {
	present bool
	Type    string
	UID     string
	// variable is true when the reference points at a dashboard variable, e.g.
	// {"uid": "$datasource"} or a bare "$datasource" string.
	variable bool
}

// parseDatasource normalizes Grafana's polymorphic datasource field, which may be:
//   - absent/null
//   - a bare string name (pre-v33): "Prometheus" or "$datasource" or "-- Grafana --"
//   - an object {type, uid} (v33+), where uid may itself be "$var"
func parseDatasource(raw json.RawMessage) datasourceRef {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return datasourceRef{present: false}
	}
	// String form.
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		asString = strings.TrimSpace(asString)
		return datasourceRef{
			present:  true,
			UID:      asString,
			variable: strings.HasPrefix(asString, "$"),
			// A bare string name carries no type; treat well-known Grafana defaults
			// as the built-in datasource and everything else as unknown-but-present.
		}
	}
	// Object form.
	var obj struct {
		Type string `json:"type"`
		UID  string `json:"uid"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		uid := strings.TrimSpace(obj.UID)
		return datasourceRef{
			present:  true,
			Type:     strings.TrimSpace(obj.Type),
			UID:      uid,
			variable: strings.HasPrefix(uid, "$"),
		}
	}
	return datasourceRef{present: true}
}

// decodeStringValue extracts a scalar string from a variable pick value/text, which
// may be a string, a single-element array, or absent.
func decodeStringValue(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var asArray []string
	if err := json.Unmarshal(raw, &asArray); err == nil && len(asArray) > 0 {
		return strings.TrimSpace(asArray[0])
	}
	return ""
}

// decodeStringValues extracts every string from a variable pick value, which may be
// a string or an array of strings.
func decodeStringValues(raw json.RawMessage) []string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var asArray []string
	if err := json.Unmarshal(raw, &asArray); err == nil {
		return asArray
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil && strings.TrimSpace(asString) != "" {
		return []string{asString}
	}
	return nil
}
