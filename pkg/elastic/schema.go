package elastic

import (
	"errors"
	"fmt"
)

// ValidateSelectionFields uses the actual field-capability intersection, not a
// preset's name, to determine whether read-only mappings are executable.
func ValidateSelectionFields(selection Selection, fields []Field) error {
	s := selection.Defaults()
	if err := s.Validate(); err != nil {
		return err
	}
	if !HasIndexedSignals(s) {
		return nil
	}
	catalog := map[string]Field{}
	signals := map[string]bool{}
	for _, task := range selectionTasks(s) {
		signals[task.kind] = true
	}
	for _, field := range fields {
		catalog[field.Name] = field
	}
	check := func(name string, numeric, aggregate bool) error {
		if name == "" {
			return nil
		}
		f, ok := catalog[name]
		if !ok || f.Conflict || len(f.Types) != 1 || !f.Searchable || aggregate && !f.Aggregatable {
			return fmt.Errorf("selected field %q is missing, conflicting or unavailable", name)
		}
		if numeric {
			switch f.Types[0] {
			case "byte", "short", "integer", "long", "unsigned_long", "half_float", "float", "double", "scaled_float":
			case "histogram", "aggregate_metric_double":
				if !aggregate {
					return fmt.Errorf("selected record field %q must be a numeric scalar", name)
				}
			default:
				return fmt.Errorf("selected field %q is not numeric", name)
			}
		}
		return nil
	}
	if err := check(s.TimestampField, false, false); err != nil {
		return err
	}
	f := catalog[s.TimestampField]
	if f.PartiallyMapped {
		return errors.New("event timestamp must be mapped in every selected index")
	}
	if f.Types[0] != "date" && f.Types[0] != "date_nanos" && s.Preset != "jaeger" {
		return errors.New("event timestamp must have a date mapping")
	}
	for _, name := range []string{s.RunIDField, s.UniqueSortField} {
		if err := check(name, false, name == s.UniqueSortField); err != nil {
			return err
		}
		if name != "" && catalog[name].PartiallyMapped {
			return fmt.Errorf("scope and pagination field %q must be mapped in every selected index", name)
		}
	}
	for _, name := range append(append([]string{}, s.AttributeFields...), s.ProjectedFields...) {
		if err := check(name, false, false); err != nil {
			return err
		}
		switch catalog[name].Types[0] {
		case "object", "nested", "flattened", "histogram", "aggregate_metric_double":
			return fmt.Errorf("projected field %q must be scalar", name)
		}
	}
	if s.Preset == "elastic_apm" {
		for _, task := range selectionTasks(s) {
			if task.kind == "span" || task.kind == "transaction" || task.kind == "error" {
				if err := check("processor.event", false, false); err != nil {
					return err
				}
				break
			}
		}
	}
	exactIdentityField := func(name string) error {
		if name == "" {
			return nil
		}
		switch catalog[name].Types[0] {
		case "keyword", "constant_keyword", "wildcard":
			return nil
		}
		return fmt.Errorf("exact scope field %q requires an unanalyzed string mapping", name)
	}
	if err := exactIdentityField(s.RunIDField); err != nil {
		return err
	}
	if len(s.ServiceValues) > 0 {
		if err := check(s.ServiceField, false, false); err != nil {
			return err
		}
		if err := exactIdentityField(s.ServiceField); err != nil {
			return err
		}
		if catalog[s.ServiceField].PartiallyMapped {
			return errors.New("service restriction must be mapped in every selected index")
		}
	}
	for _, m := range s.Metrics {
		for _, name := range []string{m.Field, m.SumField, m.CountField} {
			numeric := name != m.Field || m.Statistic != "count" && m.Statistic != "cardinality"
			if err := check(name, numeric, true); err != nil {
				return err
			}
		}
		for _, name := range m.GroupBy {
			if err := check(name, false, true); err != nil {
				return err
			}
		}
	}
	for _, q := range s.Queries {
		if q.Language == "dsl" && q.Kind == "metric" {
			for _, name := range []string{q.ValueColumn, q.SumColumn, q.CountColumn} {
				numeric := name != q.ValueColumn || q.Statistic != "count" && q.Statistic != "cardinality"
				if err := check(name, numeric, true); err != nil {
					return err
				}
			}
			for _, name := range q.GroupBy {
				if err := check(name, false, true); err != nil {
					return err
				}
			}
		}
	}
	if signals["span"] {
		for _, name := range []string{s.TraceIDField, s.SpanIDField} {
			if err := check(name, false, false); err != nil {
				return err
			}
		}
	}
	if signals["span"] || signals["transaction"] {
		if s.Preset == "elastic_apm" && s.DurationField == "transaction.duration.us" {
			if signals["span"] {
				if err := check("span.duration.us", true, false); err != nil {
					return err
				}
			}
			if signals["transaction"] {
				if err := check("transaction.duration.us", true, false); err != nil {
					return err
				}
			}
		} else if err := check(s.DurationField, true, false); err != nil {
			return err
		}
	}
	if signals["log"] {
		if err := check(s.MessageField, false, false); err != nil {
			return err
		}
	}
	return nil
}

func ValidateSelectionAgainstFields(selection Selection, fields []Field) error {
	return ValidateSelectionFields(selection, fields)
}

type Preset struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Selection   Selection `json:"selection"`
}

// Presets are explicit mapping drafts. Discovery still validates their selected
// fields; a preset never installs templates, agents, pipelines or index settings.
func Presets() []Preset {
	return []Preset{
		{Name: "ecs", Description: "ECS application logs and errors; event.duration is nanoseconds", Selection: Selection{Preset: "ecs", Logs: true, Errors: true}.Defaults()},
		{Name: "elastic_apm", Description: "Elastic APM transactions, spans and errors; duration.us is microseconds", Selection: Selection{Preset: "elastic_apm", Traces: true, Transactions: true, Errors: true}.Defaults()},
		{Name: "opensearch_otel", Description: "OpenSearch Data Prepper OpenTelemetry trace documents", Selection: Selection{Preset: "opensearch_otel", Traces: true}.Defaults()},
		{Name: "custom", Description: "Explicit custom mappings and projected scalar records", Selection: Selection{Preset: "custom"}.Defaults()},
		{Name: "component_health", Description: "Optional independently authorized cluster, Kibana and Logstash API snapshots", Selection: Selection{Preset: "custom", ClusterHealth: true}.Defaults()},
	}
}
