package dsl

import "testing"

func TestCSVExportDeclarationValidation(t *testing.T) {
	empty := ""
	valid := CSVExport{ID: "trace", FileName: "traceId-results.csv", ActionName: "sample", Columns: []CSVExportColumn{{Name: "traceId", Extraction: "trace", DefaultValue: &empty}, {Name: "latencyMs", Metric: "latency_ms"}}}
	for _, tc := range []struct {
		name    string
		mutate  func(*Script)
		invalid bool
	}{
		{"valid", func(*Script) {}, false},
		{"uppercase extension", func(s *Script) { s.Config.Results.CSVExports[0].FileName = "trace.CSV" }, false},
		{"missing action", func(s *Script) { s.Config.Results.CSVExports[0].ActionName = "unknown" }, true},
		{"duplicate action", func(s *Script) { s.Actions = append(s.Actions, s.Actions[0]) }, true},
		{"missing extraction", func(s *Script) { s.Actions[0].Extractors = nil }, true},
		{"leading space", func(s *Script) { s.Config.Results.CSVExports[0].FileName = " trace.csv" }, true},
		{"Windows drive", func(s *Script) { s.Config.Results.CSVExports[0].FileName = "C:trace.csv" }, true},
		{"control character", func(s *Script) { s.Config.Results.CSVExports[0].FileName = "trace\t.csv" }, true},
		{"missing stem", func(s *Script) { s.Config.Results.CSVExports[0].FileName = ".csv" }, true},
		{"filesystem path", func(s *Script) { s.Config.Results.CSVExports[0].FileName = "../trace.csv" }, true},
		{"metric", func(s *Script) { s.Config.Results.CSVExports[0].Columns[1].Metric = "duration" }, true},
		{"duplicate filename", func(s *Script) {
			second := s.Config.Results.CSVExports[0]
			second.ID = "second"
			s.Config.Results.CSVExports = append(s.Config.Results.CSVExports, second)
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			export := valid
			export.Columns = append([]CSVExportColumn(nil), valid.Columns...)
			script := &Script{Actions: []Action{{Name: "sample", Type: "GET", Target: "/", Extractors: []Extractor{{Name: "trace", Type: "jsonpath", Expression: "$.id"}}}}, Config: ScriptConfig{Results: ResultsConfig{CSVExports: []CSVExport{export}}}}
			tc.mutate(script)
			if issues := validateCSVExports(script); (len(issues) > 0) != tc.invalid {
				t.Fatalf("issues=%v", issues)
			}
		})
	}
}
