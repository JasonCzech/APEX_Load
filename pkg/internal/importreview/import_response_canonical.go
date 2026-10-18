package importreview

import (
	"reflect"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Reuse the serialized member when the primary is the same admitted draft.
// Pointer identity avoids a second marshal and a deep comparison of its body.
func exactPrimaryImportScript(original, exact, primary any) any {
	if script, ok := primary.(*dsl.Script); ok && script != nil {
		if originals, ok := original.([]*dsl.Script); ok {
			if values, ok := exact.([]any); ok && len(values) == len(originals) {
				for index, candidate := range originals {
					if candidate == script {
						return values[index]
					}
				}
			}
		}
	}
	return ImportResponseRawScriptValue(primary)
}

func canonicalImportResponse(response map[string]any, opts ScriptImportResponseOptions) {
	scripts, ok := response["scripts"].([]any)
	if !ok {
		// Specialized converter wrappers retain their existing contract.
		if opts.Result != nil {
			response["result"] = ImportResponseReviewValue(opts.Result)
		}
		return
	}
	response["response_format"] = "canonical-v1"
	if primary := response["script"]; !IsNilImportValue(primary) {
		index := -1
		for i, script := range scripts {
			if reflect.DeepEqual(script, primary) {
				index = i
				break
			}
		}
		if index < 0 {
			index = len(scripts)
			scripts = append(scripts, primary)
			response["scripts"] = scripts
		}
		response["primary_script_index"] = index
	}
	delete(response, "script")
	canonicalSalesforcePreview(response, opts, scripts)
	if len(opts.Diagnostics) > 0 {
		grouped := opts.Diagnostics.GroupRepeated(3)
		response["diagnostics"] = grouped
		// Only remove the string alias when it exactly represents these facts.
		if reflect.DeepEqual(opts.UnsupportedElements, opts.Diagnostics.Strings()) {
			delete(response, "unsupported_elements")
			response["unsupported_from_diagnostics"] = true
		}
		sections := append([]ScriptImportReviewSection(nil), opts.ReviewSections...)
		for _, replacement := range grouped.ReviewSections() {
			for i, section := range sections {
				if section.Key == replacement.Key {
					sections[i].Items = replacement.Items
					sections[i].Summary = replacement.Summary
				}
			}
		}
		if sections != nil {
			response["review_sections"] = sections
		}
	}
	if opts.Result == nil {
		return
	}
	// Inspect top-level converter fields before serialization: serializing the
	// entire result and deleting its script afterwards retains the CPU cost.
	facts, supported := importResultFields(opts.Result)
	if !supported {
		response["result"] = ImportResponseReviewValue(opts.Result)
		return
	}
	delete(facts, "script")
	delete(facts, "scripts")
	shared := map[string]any{
		"warnings": opts.Warnings, "stats": opts.Stats,
		"correlations": opts.Correlations, "assumptions": opts.Assumptions,
		"source_review": opts.SourceReview, "summary": opts.Summary,
		"detected_protocol": opts.DetectedProtocol, "fidelity_report": opts.FidelityReport,
		"review_artifact": opts.ReviewArtifact, "scenario_draft": opts.ScenarioDraft,
		"review_summary": opts.ReviewSummary, "diagnostics": opts.Diagnostics,
		"unsupported_elements": opts.UnsupportedElements,
	}
	aliases := map[string]string{}
	for key, value := range shared {
		if field, exists := facts[key]; exists && reflect.DeepEqual(field, value) {
			delete(facts, key)
			aliases[key] = key
		}
	}
	if field, exists := facts["unsupported"]; exists && reflect.DeepEqual(field, opts.Diagnostics) {
		delete(facts, "unsupported")
		aliases["unsupported"] = "diagnostics"
	}
	response["result"] = ImportResponseReviewValue(facts)
	if len(aliases) > 0 {
		response["result_aliases"] = aliases
	}
}

// Archive summaries expose the native draft again for the preview panel. Keep
// that convenience field as a wire reference when it is already a canonical
// member. A distinct preview remains intact, and the converter is never edited.
func canonicalSalesforcePreview(response map[string]any, opts ScriptImportResponseOptions, scripts []any) {
	fields, ok := importResultFields(opts.Summary)
	if !ok {
		return
	}
	review, ok := fields["salesforce_review"].(*salesforce.Review)
	if !ok || review == nil || review.NativePreview == nil || review.NativePreview.Script == nil {
		return
	}
	preview := review.NativePreview.Script
	index := -1
	if originals, ok := opts.Scripts.([]*dsl.Script); ok {
		for i, script := range originals {
			if i < len(scripts) && reflect.DeepEqual(preview, script) {
				index = i
				break
			}
		}
	}
	if index < 0 && reflect.DeepEqual(preview, opts.Script) {
		if primary, ok := response["primary_script_index"].(int); ok {
			index = primary
		}
	}
	if index < 0 {
		return
	}
	clonedReview, clonedPreview := *review, *review.NativePreview
	clonedPreview.Script = nil
	clonedReview.NativePreview = &clonedPreview
	if summary, ok := summaryWithSalesforceReview(opts.Summary, &clonedReview); ok {
		response["summary"] = summary
		response["salesforce_preview_script_index"] = index
	}
}

func summaryWithSalesforceReview(summary any, review *salesforce.Review) (any, bool) {
	if original, ok := summary.(map[string]any); ok {
		cloned := make(map[string]any, len(original))
		for key, value := range original {
			cloned[key] = value
		}
		cloned["salesforce_review"] = review
		return cloned, true
	}
	value := reflect.ValueOf(summary)
	pointer := value.IsValid() && value.Kind() == reflect.Pointer
	if pointer && !value.IsNil() {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil, false
	}
	cloned := reflect.New(value.Type()).Elem()
	cloned.Set(value)
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if field.PkgPath == "" && strings.Split(field.Tag.Get("json"), ",")[0] == "salesforce_review" && reflect.TypeOf(review).AssignableTo(field.Type) {
			cloned.Field(i).Set(reflect.ValueOf(review))
			if pointer {
				return cloned.Addr().Interface(), true
			}
			return cloned.Interface(), true
		}
	}
	return nil, false
}

// Converter results are plain structs or string-keyed maps. Unknown shapes
// fall back to the legacy projection rather than guessing JSON embedding rules.
func importResultFields(result any) (map[string]any, bool) {
	value := reflect.ValueOf(result)
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil, false
		}
		value = value.Elem()
	}
	out := map[string]any{}
	if value.IsValid() && value.Kind() == reflect.Map && value.Type().Key().Kind() == reflect.String {
		iter := value.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = iter.Value().Interface()
		}
		return out, true
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil, false
	}
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if field.Anonymous {
			return nil, false
		}
		if name == "" {
			name = field.Name
		}
		out[name] = value.Field(i).Interface()
	}
	return out, true
}
