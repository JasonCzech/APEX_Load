package postman

// chaining.go — turns Postman test-script response captures
// (pm.<scope>.set("var", pm.response...)) into response extractors so later
// {{var.X}} references resolve at replay. This is Postman's deterministic
// response→request chaining idiom (collections carry no response schemas, so
// the chain signal lives in the test scripts, not the contract).

import (
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type postmanChainItem struct {
	ActionIndex int
	Item        requestItem
	// Extractors is precomputed while walking the collection's scoped variable
	// stores. A non-nil empty slice deliberately suppresses the syntax-only
	// fallback: the response writes were recognized, but none survives Postman's
	// scope precedence and request-local lifetime into the next request.
	Extractors []detectedExtractor
}

var (
	// const/let/var jsonData = pm.response.json()
	postmanJSONAliasPattern = regexp.MustCompile(`(?:var|let|const)\s+([A-Za-z_$][\w$]*)\s*=\s*pm\.response\.json\s*\(\s*\)`)
	// pm.<scope>.set("name", pm.response.json()<.a.b>)
	postmanSetJSONPattern = regexp.MustCompile(`pm\.(environment|collectionVariables|globals|variables)\.set\s*\(\s*["']([^"']+)["']\s*,\s*pm\.response\.json\s*\(\s*\)((?:\.[A-Za-z_$][\w$]*)*)\s*\)`)
	// pm.<scope>.set("name", <ident><.a.b>) where <ident> aliases the parsed body
	postmanSetAliasPattern = regexp.MustCompile(`pm\.(environment|collectionVariables|globals|variables)\.set\s*\(\s*["']([^"']+)["']\s*,\s*([A-Za-z_$][\w$]*)((?:\.[A-Za-z_$][\w$]*)+)\s*\)`)
	// pm.<scope>.set("name", pm.response.headers.get("Header"))
	postmanSetHeaderPattern = regexp.MustCompile(`pm\.(environment|collectionVariables|globals|variables)\.set\s*\(\s*["']([^"']+)["']\s*,\s*pm\.response\.headers\.get\s*\(\s*["']([^"']+)["']\s*\)\s*\)`)
)

type detectedExtractor struct {
	varName     string
	source      string // "body" | "header"
	extractType string // "jsonpath" | "header"
	expression  string
}

func applyPostmanResponseChaining(script *dsl.Script, items []postmanChainItem, diagnostics *diag.Collector) []Correlation {
	if script == nil {
		return nil
	}
	var correlations []Correlation
	for _, ci := range items {
		if ci.ActionIndex < 0 || ci.ActionIndex >= len(script.Actions) {
			continue
		}
		detected := ci.Extractors
		if detected == nil {
			detected = detectPostmanExtractors(ci.Item.Events)
		}
		if len(detected) == 0 {
			continue
		}
		action := &script.Actions[ci.ActionIndex]
		existing := extractorNameSet(action.Extractors)
		location := postmanItemLocation(ci.Item)
		for _, d := range detected {
			if existing[d.varName] {
				continue
			}
			existing[d.varName] = true
			action.Extractors = append(action.Extractors, dsl.Extractor{
				Name:       d.varName,
				Source:     d.source,
				Type:       d.extractType,
				Expression: d.expression,
				OnMiss:     "empty",
			})
			correlations = append(correlations, Correlation{
				Name:        d.varName,
				Source:      d.source,
				Expression:  d.expression,
				ExtractType: d.extractType,
				FoundInStep: ci.ActionIndex + 1,
			})
			if diagnostics != nil {
				diagnostics.Add(diag.Info, "Postman correlation", location, "captured "+d.expression+" from the response into {{var."+d.varName+"}} for later requests")
			}
		}
	}
	if len(correlations) == 0 {
		return nil
	}
	// Resolve which later steps consume each captured variable.
	for i := range correlations {
		token := "{{var." + correlations[i].Name + "}}"
		var used []int
		for idx := range script.Actions {
			step := idx + 1
			if step == correlations[i].FoundInStep {
				continue
			}
			if actionReferencesToken(script.Actions[idx], token) {
				used = append(used, step)
			}
		}
		sort.Ints(used)
		correlations[i].UsedInSteps = used
	}
	return correlations
}

func detectPostmanExtractors(events []event) []detectedExtractor {
	state := newPostmanScopedVariables(nil, nil)
	request := requestItem{Events: events}
	writes := applyPostmanScopedEventAssignmentsForListen(state, request, "test", nil)
	// This syntax-only helper describes the value visible at the end of the
	// post-response script. Convert's collection walk additionally clears the
	// local store and supplies its persistent, next-request extractor set.
	return state.effectiveResponseExtractors(writes)
}

// postmanCorrelationScriptFullyTranslated proves that every executable token
// in one test event belongs to the deliberately narrow response-capture
// grammar above. A recognized substring is insufficient: any residual comment,
// call, operator, or control-flow token keeps the durable review gate.
func postmanCorrelationScriptFullyTranslated(ev event) bool {
	if !strings.EqualFold(strings.TrimSpace(ev.Listen), "test") ||
		len(detectPostmanExtractors([]event{ev})) == 0 {
		return false
	}
	remaining := strings.Join(ev.Script.Exec, "\n")
	for _, pattern := range []*regexp.Regexp{
		postmanJSONAliasPattern,
		postmanSetJSONPattern,
		postmanSetAliasPattern,
		postmanSetHeaderPattern,
	} {
		remaining = pattern.ReplaceAllString(remaining, "")
	}
	remaining = strings.Map(func(value rune) rune {
		switch value {
		case ' ', '\t', '\r', '\n', ';':
			return -1
		default:
			return value
		}
	}, remaining)
	return remaining == ""
}

func jsonPathFromDotPath(dotPath string) string {
	dotPath = strings.TrimSpace(dotPath)
	if dotPath == "" {
		return "$"
	}
	return "$" + dotPath
}

func extractorNameSet(extractors []dsl.Extractor) map[string]bool {
	out := map[string]bool{}
	for _, e := range extractors {
		if n := strings.TrimSpace(e.Name); n != "" {
			out[n] = true
		}
	}
	return out
}

func actionReferencesToken(action dsl.Action, token string) bool {
	if strings.Contains(action.Target, token) || strings.Contains(action.Body, token) || strings.Contains(action.Value, token) {
		return true
	}
	for _, v := range action.Headers {
		if strings.Contains(v, token) {
			return true
		}
	}
	if action.Auth != nil && (strings.Contains(action.Auth.Token, token) || strings.Contains(action.Auth.Username, token) || strings.Contains(action.Auth.Password, token)) {
		return true
	}
	return false
}
