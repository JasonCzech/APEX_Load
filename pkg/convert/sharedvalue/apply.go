package sharedvalue

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// ErrCredentialShaped is returned when a caller tries to bind a
// credential-shaped value to a script parameter. Such values must go through
// the guided secret-binding flow; see CredentialShaped for why a parameter
// would silently destroy the value.
var ErrCredentialShaped = errors.New("credential-shaped shared value must use a secret binding")

// Apply writes the operator's values into every affected script, in place.
// values keys are candidate IDs; a candidate with no entry, or an empty entry,
// is left untouched.
//
// Callers must pass candidates they derived themselves from these same scripts
// — never candidates supplied by a client. Only the ID and the operator's value
// cross the trust boundary.
func Apply(scripts []*dsl.Script, candidates []Candidate, values map[string]string) ([]Diff, error) {
	diffs := make([]Diff, 0, len(values))
	applied := make([]string, 0, len(values))
	for _, candidate := range candidates {
		raw, ok := values[candidate.ID]
		if !ok {
			continue
		}
		// Trim once, here, and use the trimmed value everywhere below. Testing
		// TrimSpace for emptiness and then writing the raw string would let a
		// pasted "https://prod.example.com " reach config.base_url verbatim and
		// make every request in the collection target a malformed URL.
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if candidate.CredentialShaped {
			return nil, fmt.Errorf("%w: %s", ErrCredentialShaped, candidate.Name)
		}
		if !ValidName(candidate.Name) {
			return nil, fmt.Errorf("shared value %q is not a valid parameter name", candidate.Name)
		}
		if len(value) > maxSharedValueBytes {
			return nil, fmt.Errorf("shared value %q exceeds %d bytes", candidate.Name, maxSharedValueBytes)
		}
		candidateDiffs, err := applyCandidate(scripts, candidate, value)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, candidateDiffs...)
		applied = append(applied, candidate.Name)
	}
	if err := assertBaseURLsAreLiteral(scripts, applied); err != nil {
		return nil, err
	}
	// Missing-variable gates are stamped at conversion time and do not heal
	// themselves. Without this the script would still refuse to run after the
	// operator supplied the very value the gate was asking for.
	for _, script := range scripts {
		cleared, err := requestimport.ClearResolvedMissingVariableGates(script)
		if err != nil {
			return nil, err
		}
		for _, path := range cleared {
			diffs = append(diffs, Diff{
				ScriptName: scriptName(script),
				Path:       path + ".metadata.execution_blocker",
			})
		}
		// Same reasoning for the base-URL gate: its documented resolution is the
		// substitution performed above, so leaving it would block a script the
		// operator has already fixed.
		clearedBaseURL, err := requestimport.ClearResolvedBaseURLTemplateGate(script)
		if err != nil {
			return nil, err
		}
		for _, path := range clearedBaseURL {
			diffs = append(diffs, Diff{
				ScriptName: scriptName(script),
				Path:       path + ".metadata.import_review",
			})
		}
	}
	return diffs, nil
}

func applyCandidate(scripts []*dsl.Script, candidate Candidate, value string) ([]Diff, error) {
	diffs := make([]Diff, 0, candidate.OccurrenceCount+len(candidate.ScriptIndexes))

	// Target scripts by the positions recorded at detection time. Matching on
	// script name instead would apply a candidate found in one script to an
	// unrelated namesake, which two collection folders holding a request of the
	// same name produce routinely.
	for _, scriptIndex := range candidate.ScriptIndexes {
		script := scriptAt(scripts, scriptIndex)
		if script == nil {
			continue
		}
		switch candidate.Kind {
		case KindExistingParameter:
			diff, ok := updateParameterValue(script, candidate, value)
			if ok {
				diffs = append(diffs, diff)
			}
		case KindSourceVariable:
			// {{var.NAME}} resolves through the parameter runtime, so for action
			// targets defining the parameter is the whole fix.
			diffs = append(diffs, upsertParameter(script, candidate, value))
		case KindRepeatedLiteral:
			rewritten, err := rewriteLiteral(script, scriptIndex, candidate, value)
			if err != nil {
				return nil, err
			}
			diffs = append(diffs, rewritten...)
		}
		// config.base_url is the exception: it is flattened once at protocol
		// init, before any VUser exists, so a template there is sent literally.
		// Importers that copy a source variable into base_url ship a script
		// gated with base_url_runtime_template_unavailable, whose documented
		// resolution is exactly this substitution.
		if diff, ok := literalizeBaseURL(script, candidate, value); ok {
			diffs = append(diffs, diff)
		}
	}
	return diffs, nil
}

// literalizeBaseURL replaces this candidate's template in config.base_url with
// the operator's literal. Templates naming any other variable are left alone:
// they are a pre-existing condition this pass did not create and cannot fix.
func literalizeBaseURL(script *dsl.Script, candidate Candidate, value string) (Diff, bool) {
	before := script.Config.BaseURL
	if before == "" || !strings.Contains(before, "{{") {
		return Diff{}, false
	}
	after := replaceBaseURLTemplates(before, candidate.Name, value)
	if after == before {
		return Diff{}, false
	}
	script.Config.BaseURL = after
	return Diff{
		CandidateID: candidate.ID,
		ScriptName:  scriptName(script),
		Path:        "config.base_url",
		Before:      before,
		After:       after,
	}, true
}

// updateParameterValue writes through whichever constant shape the converter
// used — inline values, or the single row of an inline CSV data source. A
// parameter that turned out not to be a constant is left alone rather than
// being flattened into one.
func updateParameterValue(script *dsl.Script, candidate Candidate, value string) (Diff, bool) {
	index := parameterIndex(script, candidate.Name)
	if index < 0 {
		return upsertParameter(script, candidate, value), true
	}
	binding, ok := constantParameterBinding(script, script.Parameters[index])
	if !ok {
		return Diff{}, false
	}
	before, changed := writeConstantBinding(script, &script.Parameters[index], binding, value)
	if !changed {
		return Diff{}, false
	}
	path := fmt.Sprintf("parameters[%d].values[0]", index)
	if !binding.inline {
		path = fmt.Sprintf("data_sources[%s].%s", binding.dataSourceName, binding.column)
	}
	return Diff{
		CandidateID: candidate.ID,
		ScriptName:  scriptName(script),
		Path:        path,
		Before:      before,
		After:       value,
	}, true
}

// upsertParameter adds the constant form every request-collection converter
// already emits: a list with one inline value, resolved once per VUser.
func upsertParameter(script *dsl.Script, candidate Candidate, value string) Diff {
	if index := parameterIndex(script, candidate.Name); index >= 0 {
		diff, _ := updateParameterValue(script, candidate, value)
		return diff
	}
	script.Parameters = append(script.Parameters, dsl.Parameter{
		Name:   candidate.Name,
		Type:   "list",
		Update: "once",
		Values: []string{value},
	})
	return Diff{
		CandidateID: candidate.ID,
		ScriptName:  scriptName(script),
		Path:        fmt.Sprintf("parameters[%d]", len(script.Parameters)-1),
		After:       value,
	}
}

func rewriteLiteral(script *dsl.Script, scriptIndex int, candidate Candidate, value string) ([]Diff, error) {
	name := scriptName(script)
	wanted := make(map[string]bool, len(candidate.Locations))
	baseURLTargeted := false
	for _, location := range candidate.Locations {
		if location.ScriptIndex != scriptIndex {
			continue
		}
		if location.Field == FieldBaseURL {
			baseURLTargeted = true
			continue
		}
		wanted[location.Path+"\x00"+location.Field] = true
	}
	if !baseURLTargeted && len(wanted) == 0 {
		return nil, nil
	}

	diffs := make([]Diff, 0, len(wanted)+1)
	// config.base_url is flattened once at protocol init, before any VUser
	// exists, so it can hold only a literal (or a {{secret.*}} reference).
	// Writing the operator's value directly is the only correct outcome here.
	if baseURLTargeted {
		before := script.Config.BaseURL
		if before != value {
			script.Config.BaseURL = value
			diffs = append(diffs, Diff{
				CandidateID: candidate.ID,
				ScriptName:  name,
				Path:        "config.base_url",
				Before:      before,
				After:       value,
			})
		}
	}

	if len(wanted) > 0 {
		replacements := map[string]string{candidate.LiteralValue: "{{param." + candidate.Name + "}}"}
		// Count the field rewrites separately from diffs: diffs may already hold
		// the config.base_url change, and minting a parameter on the strength of
		// that would leave one nothing in the script references.
		rewritten := 0
		err := walkFields(script, func(entry field) bool {
			if !wanted[entry.path+"\x00"+entry.name] {
				return true
			}
			before := entry.value
			after := paramize.ReplaceOutsideTemplates(before, replacements)
			if after == before {
				return true
			}
			entry.set(after)
			rewritten++
			diffs = append(diffs, Diff{
				CandidateID: candidate.ID,
				ScriptName:  name,
				Path:        entry.path + "." + entry.name,
				Before:      before,
				After:       after,
			})
			return true
		})
		if err != nil {
			return nil, err
		}
		if rewritten > 0 {
			upsertParameter(script, candidate, value)
		}
	}
	return diffs, nil
}

// assertBaseURLsAreLiteral is a hard postcondition, not a nicety. A template in
// config.base_url is sent verbatim by replay, shakeout, and the CLI, because
// buildProtoConfigWithSecretResolver flattens it before any VUser exists.
// pkg/convert/requestimport raises base_url_runtime_template_unavailable for
// exactly this; nothing here may reintroduce it.
// It is deliberately scoped to the names this call applied. A template naming
// some other variable is a pre-existing importer condition, already reported as
// base_url_runtime_template_unavailable, and failing the whole apply over it
// would block the operator from fixing the parts they can.
func assertBaseURLsAreLiteral(scripts []*dsl.Script, appliedNames []string) error {
	for _, script := range scripts {
		if script == nil {
			continue
		}
		for _, name := range appliedNames {
			if match := findBaseURLTemplate(script.Config.BaseURL, name); match != "" {
				return fmt.Errorf("script %q: config.base_url must hold a literal, not %s", scriptName(script), match)
			}
		}
	}
	return nil
}

// baseURLTemplatePattern mirrors the engine's own template grammar, which trims
// whitespace inside the braces. Matching the exact strings "{{var.X}}" and
// "{{param.X}}" would miss "{{ var.X }}" — a reference the runtime honours —
// leaving the substitution a no-op and the postcondition blind to it.
var baseURLTemplatePattern = regexp.MustCompile(`\{\{\s*(var|param|data)\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

func replaceBaseURLTemplates(value, name, replacement string) string {
	return baseURLTemplatePattern.ReplaceAllStringFunc(value, func(match string) string {
		groups := baseURLTemplatePattern.FindStringSubmatch(match)
		if len(groups) == 3 && groups[2] == name && groups[1] != "data" {
			return replacement
		}
		return match
	})
}

// findBaseURLTemplate returns the offending template when config.base_url still
// references name through a per-VUser namespace, or "" when it does not.
func findBaseURLTemplate(value, name string) string {
	for _, match := range baseURLTemplatePattern.FindAllStringSubmatch(value, -1) {
		if len(match) == 3 && match[2] == name {
			return match[0]
		}
	}
	return ""
}
