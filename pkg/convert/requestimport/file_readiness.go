package requestimport

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

const MissingFilePlaceholder = "[APEXLOAD_IMPORT_FILE_PLACEHOLDER]"

var importFileReferencePattern = regexp.MustCompile(`\{\{\s*file\.([^{}\s]+)\s*\}\}`)

func prepareMissingFileBindings(script *dsl.Script) ([]importreview.Finding, error) {
	if script == nil {
		return nil, nil
	}
	sourceIndex := make(map[string]int, len(script.DataSources))
	unbound := make(map[string]struct{})
	for index := range script.DataSources {
		source := &script.DataSources[index]
		if !strings.EqualFold(strings.TrimSpace(source.Type), "file") {
			continue
		}
		name := strings.TrimSpace(source.Name)
		if name == "" {
			continue
		}
		sourceIndex[name] = index
		if strings.TrimSpace(source.Path) == "" &&
			strings.TrimSpace(source.StorageRef) == "" &&
			(strings.TrimSpace(source.InlineData) == "" || isImportFilePlaceholder(source.InlineData)) {
			if strings.TrimSpace(source.InlineData) == "" {
				source.InlineData = MissingFilePlaceholder
			}
			source.Encoding = "text"
			unbound[name] = struct{}{}
		}
	}

	refsByPath := make(map[string][]string)
	allRefs := make(map[string]struct{})
	if err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		refs := surfaceFileReferences(ref)
		if len(refs) == 0 {
			return nil
		}
		// http_sequence requests are untyped children of one executable action.
		// Keep the existing durable-gate placement on that containing action
		// while scanning each child as its own request surface.
		ownerPath := ref.OwnerPath
		refsByPath[ownerPath] = uniqueSorted(append(refsByPath[ownerPath], refs...))
		for _, name := range refs {
			allRefs[name] = struct{}{}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var missingSources []string
	for name := range allRefs {
		if _, exists := sourceIndex[name]; exists {
			continue
		}
		missingSources = append(missingSources, name)
	}
	sort.Strings(missingSources)
	for _, name := range missingSources {
		sourceIndex[name] = len(script.DataSources)
		script.DataSources = append(script.DataSources, dsl.DataSource{
			Name:       name,
			Type:       "file",
			InlineData: MissingFilePlaceholder,
			Encoding:   "text",
			Strategy:   "sequential",
		})
		unbound[name] = struct{}{}
	}

	var findings []importreview.Finding
	if err := dslwalk.Walk(script, func(surface dslwalk.SurfaceRef) error {
		if surface.Kind != dslwalk.ActionSurface {
			return nil
		}
		ref := dslwalk.ActionRef{
			Phase:  surface.Phase,
			Path:   surface.Path,
			Depth:  surface.Depth,
			Action: surface.Action,
		}
		var missing []string
		for _, name := range refsByPath[ref.Path] {
			if _, needsBinding := unbound[name]; needsBinding {
				missing = append(missing, name)
			}
		}
		missing = uniqueSorted(missing)
		if len(missing) == 0 {
			return nil
		}
		if ref.Action.Metadata == nil {
			ref.Action.Metadata = make(map[string]any)
		}
		ref.Action.Metadata["missing_file_bindings"] = missing
		if _, alreadyDefined := ref.Action.Metadata["import_review"]; !alreadyDefined {
			ref.Action.Metadata["import_review"] = map[string]any{
				"version":      1,
				"source":       importSource(script),
				"reason_codes": []string{"missing_file_binding"},
				"source_refs":  []string{ref.Path},
				"resolution":   "bind_file_assets_and_remove_gate",
			}
		}
		finding := importreview.Finding{
			Code:       "import_missing_file_bindings",
			Severity:   diag.Blocker,
			Element:    firstNonEmpty(ref.Action.Name, "request"),
			Detail:     fmt.Sprintf("%d file asset binding(s) must be supplied before execution", len(missing)),
			Location:   ref.Path,
			ActionPath: ref.Path,
			Gate:       importreview.GateExecution,
		}
		importreview.ApplyToAction(ref.Action, []importreview.Finding{finding})
		findings = append(findings, finding)
		return nil
	}); err != nil {
		return nil, err
	}
	return importreview.NormalizeFindings(findings), nil
}

func surfaceFileReferences(ref dslwalk.SurfaceRef) []string {
	if ref.Action != nil {
		action := *ref.Action
		if ref.Kind == dslwalk.ActionSurface &&
			strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
			action.Params = paramsWithoutSequenceRequests(action.Params)
		}
		return actionFileReferences(&action)
	}
	if ref.Request == nil {
		return nil
	}
	payload, err := json.Marshal(ref.Request)
	if err != nil {
		return nil
	}
	var action dsl.Action
	if err := json.Unmarshal(payload, &action); err != nil {
		return nil
	}
	return actionFileReferences(&action)
}

func paramsWithoutSequenceRequests(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}
	out := make(map[string]any, len(params))
	for name, value := range params {
		if name != "requests" {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isImportFilePlaceholder(value string) bool {
	value = strings.TrimSpace(value)
	if value == MissingFilePlaceholder {
		return true
	}
	// Staged adopters historically emitted source-specific sentinels. Treat
	// those internal APEXLOAD file placeholders as unbound while normalizing
	// the finalized draft to the shared sentinel.
	return strings.HasPrefix(value, "[APEXLOAD_") &&
		strings.HasSuffix(value, "_FILE_PLACEHOLDER]")
}

func importSource(script *dsl.Script) string {
	if script == nil {
		return "import"
	}
	if source := strings.TrimSpace(script.GeneratedFrom); source != "" {
		return source
	}
	if script.Metadata != nil && script.Metadata.Import != nil {
		if source := strings.TrimSpace(script.Metadata.Import.Source); source != "" {
			return source
		}
	}
	return "import"
}

func actionFileReferences(action *dsl.Action) []string {
	if action == nil {
		return nil
	}
	values := []string{
		action.Target,
		action.Selector,
		action.Value,
		action.Body,
		action.BodyFile,
	}
	for name, value := range action.Headers {
		values = append(values, name, value)
	}
	for _, header := range action.HeaderList {
		values = append(values, header.Name, header.Value)
	}
	if action.Auth != nil {
		values = append(values, action.Auth.Username, action.Auth.Password, action.Auth.Token, action.Auth.Domain)
	}
	if len(action.Params) != 0 {
		if encoded, err := json.Marshal(action.Params); err == nil {
			values = append(values, string(encoded))
		}
	}
	set := make(map[string]struct{})
	for _, value := range values {
		for _, match := range importFileReferencePattern.FindAllStringSubmatch(value, -1) {
			if len(match) == 2 {
				if name := strings.TrimSpace(match[1]); name != "" {
					set[name] = struct{}{}
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
