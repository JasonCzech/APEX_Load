package dsl

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
)

var csvExportID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func validateCSVExports(script *Script) []string {
	var issues []string
	exports := script.Config.Results.CSVExports
	if len(exports) > 32 {
		return []string{"config.results.csv_exports must not exceed 32 exports"}
	}
	ids := map[string]bool{}
	files := map[string]bool{}
	actions := map[string]Action{}
	names := map[string]int{}
	visitScriptActions(script, func(_ string, a Action) { names[a.Name]++; actions[a.Name] = a })
	for i, e := range exports {
		label := fmt.Sprintf("config.results.csv_exports[%d]", i)
		if !csvExportID.MatchString(e.ID) || ids[e.ID] {
			issues = append(issues, label+": id must be unique and use 1-64 letters, digits, underscores or hyphens")
		}
		ids[e.ID] = true
		if files[e.FileName] {
			issues = append(issues, label+": file_name must be unique")
		}
		files[e.FileName] = true
		if err := ValidateCSVExportFileName(e.FileName); err != nil {
			issues = append(issues, label+": file_name must be a CSV basename")
		}
		if names[e.ActionName] != 1 || len(e.ActionName) > 512 {
			issues = append(issues, label+": action_name must identify exactly one action")
		}
		if len(e.Columns) == 0 || len(e.Columns) > 32 {
			issues = append(issues, label+": requires 1-32 columns")
		}
		cols := map[string]bool{}
		for _, c := range e.Columns {
			if c.Name == "" || len(c.Name) > 128 || cols[c.Name] || strings.ContainsAny(c.Name, "\r\n\x00") {
				issues = append(issues, label+": column names must be nonempty and unique")
			}
			cols[c.Name] = true
			if (c.Extraction == "") == (c.Metric == "") {
				issues = append(issues, label+": each column requires exactly one extraction or metric")
			}
			if c.Extraction != "" {
				found := false
				for _, ext := range actions[e.ActionName].Extractors {
					if ext.Name == c.Extraction {
						found = true
					}
				}
				if !found {
					issues = append(issues, label+": extraction must name an extractor on the source action")
				}
			}
			if c.Metric != "" && c.Metric != "latency_ms" {
				issues = append(issues, label+": unsupported metric")
			}
			if c.DefaultValue != nil && (c.Extraction == "" || len(*c.DefaultValue) > 4096) {
				issues = append(issues, label+": default_value requires an extraction and at most 4096 bytes")
			}
		}
	}
	return issues
}

// ValidateCSVExportFileName shares the portable artifact contract with importers
// and materialization, so readiness never promises an unrepresentable filename.
func ValidateCSVExportFileName(name string) error {
	drive := len(name) > 1 && name[1] == ':' && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z'))
	if strings.TrimSpace(name) != name || path.Base(name) != name || !strings.HasSuffix(strings.ToLower(name), ".csv") || len(name) <= 4 || len(name) > 128 || strings.Contains(name, "\\") || strings.ContainsFunc(name, unicode.IsControl) || drive {
		return fmt.Errorf("file_name must be a canonical CSV basename of at most 128 bytes")
	}
	return nil
}
