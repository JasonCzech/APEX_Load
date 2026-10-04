package engine

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// ValidateRunCorrelationHeaders rejects conflicting authored values before a
// managed run is prepared. It includes nested and JSON-shaped sequence requests.
func ValidateRunCorrelationHeaders(script *dsl.Script, runID string) error {
	if script == nil {
		return nil
	}
	check := func(name, value, location string) error {
		if !strings.EqualFold(name, "X-Apex-Test-Run") {
			return nil
		}
		if value == runID || strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), " ", ""), "\t", "") == "{{run.id}}" {
			return nil
		}
		return fmt.Errorf("%s: authored X-Apex-Test-Run conflicts with managed run correlation", location)
	}
	for name, value := range script.Config.HTTP.DefaultHeaders {
		if err := check(name, value, "config.http.default_headers"); err != nil {
			return err
		}
	}
	for _, header := range script.Config.HTTP.DefaultHeaderList {
		if err := check(header.Name, header.Value, "config.http.default_header_list"); err != nil {
			return err
		}
	}
	return dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil {
			for name, value := range ref.Action.Headers {
				if err := check(name, value, ref.Path); err != nil {
					return err
				}
			}
			for _, header := range ref.Action.HeaderList {
				if err := check(header.Name, header.Value, ref.Path); err != nil {
					return err
				}
			}
		}
		if ref.Request != nil {
			switch headers := ref.Request["headers"].(type) {
			case map[string]string:
				for name, value := range headers {
					if err := check(name, value, ref.Path); err != nil {
						return err
					}
				}
			case map[string]any:
				for name, value := range headers {
					if err := check(name, fmt.Sprint(value), ref.Path); err != nil {
						return err
					}
				}
			}
			if headers, ok := ref.Request["header_list"].([]any); ok {
				for _, raw := range headers {
					if header, ok := raw.(map[string]any); ok {
						if err := check(fmt.Sprint(header["name"]), fmt.Sprint(header["value"]), ref.Path); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	})
}
