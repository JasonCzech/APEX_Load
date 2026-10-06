package enterprisearchive

import (
	"fmt"
	"maps"
	"strings"
)

// VuGen scripts commonly move each business step into a C function defined in
// an action file (often the same one) and call it from vuser_init, Action or
// vuser_end. The parser records every call in a file with the C function that
// encloses it, so without inlining a step call became a no-op custom_call while
// every helper body was emitted in definition order after the main flow: init
// steps ran every iteration and transactions around step calls timed nothing.

// maxSourceHelperInlineDepth bounds nested helper inlining; deeper chains keep
// the remaining calls as custom_call actions and report it.
const maxSourceHelperInlineDepth = 16

// maxInlinedSourceCalls bounds the calls one action produces through inlining,
// so a helper that calls another many times, several levels deep, cannot
// multiply into an unbounded script. Further calls stay custom_call actions.
const maxInlinedSourceCalls = 20000

// sourceHelperDefinition is a C function the archive defines: the calls its
// body makes, in source order.
type sourceHelperDefinition struct {
	name  string
	file  string
	calls []ParsedFunction
}

// sourceHelperTable maps a lowercased C function name to its definition.
// Names defined in more than one file are ambiguous (a C link error) and are
// recorded so their calls stay unresolved.
type sourceHelperTable struct {
	definitions map[string]*sourceHelperDefinition
	ambiguous   map[string]bool
	// contents holds each source file's parsed text by lowercased file name,
	// for translating helpers called with arguments.
	contents map[string]string
	sources  map[string]helperSource // located definitions, by file and name
	// blanked memoizes each file's comment-free text for locating helpers.
	blanked map[string]string
	// zeroArgTranslations memoizes translated bodies of helpers called
	// without arguments, which do not depend on the call site.
	zeroArgTranslations map[string][]ParsedFunction
}

// buildSourceHelperTable collects every C function body from the parsed source
// files. Lifecycle and run-action entry functions are included; a call to one
// of them from another function is inlined like any other helper.
func buildSourceHelperTable(sources []*ParsedAction, contents map[string]string) sourceHelperTable {
	table := sourceHelperTable{
		definitions:         map[string]*sourceHelperDefinition{},
		ambiguous:           map[string]bool{},
		contents:            map[string]string{},
		sources:             map[string]helperSource{},
		blanked:             map[string]string{},
		zeroArgTranslations: map[string][]ParsedFunction{},
	}
	for name, content := range contents {
		table.contents[strings.ToLower(name)] = content
	}
	for _, source := range sources {
		if source == nil {
			continue
		}
		for _, function := range mergeEmbeddedCalls(source.Functions, source.embeddedCalls) {
			owner := strings.TrimSpace(function.sourceFunction)
			if owner == "" {
				continue
			}
			key := strings.ToLower(owner)
			definition := table.definitions[key]
			if definition == nil {
				definition = &sourceHelperDefinition{name: owner, file: source.Filename}
				table.definitions[key] = definition
			} else if !strings.EqualFold(definition.file, source.Filename) {
				table.ambiguous[key] = true
				continue
			}
			definition.calls = append(definition.calls, function)
		}
	}
	// A function is a helper when its body makes a statement call or an
	// expression call to another helper. Expression calls to anything else
	// (strlen, atoi) were never imported and stay out of every body.
	defined := map[string]bool{}
	for key, definition := range table.definitions {
		for _, call := range definition.calls {
			if call.embeddedCall == "" {
				defined[key] = true
				break
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for key, definition := range table.definitions {
			if defined[key] {
				continue
			}
			for _, call := range definition.calls {
				if callee := strings.ToLower(strings.TrimSpace(call.Name)); defined[callee] && !table.ambiguous[callee] {
					defined[key], changed = true, true
					break
				}
			}
		}
	}
	for key := range table.definitions {
		if !defined[key] {
			delete(table.definitions, key)
		}
	}
	for _, definition := range table.definitions {
		definition.calls = table.withoutUnresolvedEmbeddedCalls(definition.calls)
	}
	return table
}

// mergeEmbeddedCalls places expression calls among the statement calls by
// source position: each goes before the first statement call that starts after
// it, so a helper called in an if condition runs before that branch.
func mergeEmbeddedCalls(statements, embedded []ParsedFunction) []ParsedFunction {
	if len(embedded) == 0 {
		return statements
	}
	merged := make([]ParsedFunction, 0, len(statements)+len(embedded))
	next := 0
	for _, statement := range statements {
		for next < len(embedded) && sourceCallStartsBefore(embedded[next], statement) {
			merged = append(merged, embedded[next])
			next++
		}
		merged = append(merged, statement)
	}
	return append(merged, embedded[next:]...)
}

func sourceCallStartsBefore(a, b ParsedFunction) bool {
	if !strings.EqualFold(a.Filename, b.Filename) {
		return false
	}
	if a.sourceEndOffset > a.sourceOffset && b.sourceEndOffset > b.sourceOffset {
		return a.sourceOffset < b.sourceOffset
	}
	if a.LineNumber != b.LineNumber {
		return a.LineNumber < b.LineNumber
	}
	return a.sourceColumn < b.sourceColumn
}

// withoutUnresolvedEmbeddedCalls drops expression calls that name no helper,
// as the importer always did, keeping statement calls and helper calls.
func (table sourceHelperTable) withoutUnresolvedEmbeddedCalls(calls []ParsedFunction) []ParsedFunction {
	kept := calls[:0:0]
	for _, call := range calls {
		if call.embeddedCall != "" {
			if _, ok := table.definitionFor(call); !ok {
				continue
			}
		}
		kept = append(kept, call)
	}
	return kept
}

// definitionFor returns the C function the archive defines unambiguously for
// a call. LoadRunner and C library functions are never helpers.
func (table sourceHelperTable) definitionFor(call ParsedFunction) (*sourceHelperDefinition, bool) {
	key := strings.ToLower(strings.TrimSpace(call.Name))
	if key == "" || table.ambiguous[key] {
		return nil, false
	}
	if _, known := lookupSourceFunction(key); known {
		return nil, false
	}
	definition, ok := table.definitions[key]
	return definition, ok
}

// helperCalls returns the calls a helper invocation performs: the recorded body
// for a call without arguments, or the translated body for one with arguments.
func (table sourceHelperTable) helperCalls(call ParsedFunction, definition *sourceHelperDefinition) ([]ParsedFunction, string) {
	if len(call.RawArgs) == 0 && len(call.CallArgs) == 0 {
		// The recorded body keeps control flow and int locals. A body that
		// builds strings in character locals needs translation, which falls
		// back to the recorded body (string operations kept for review) when
		// the body also uses something translation does not support.
		if usesCStringOperations(definition.calls) {
			key := strings.ToLower(definition.file) + "\x00" + strings.ToLower(definition.name)
			if translated, ok := table.zeroArgTranslations[key]; ok {
				return translated, ""
			}
			if translated, reason := table.translateHelperCall(call, definition); reason == "" {
				if table.zeroArgTranslations != nil {
					table.zeroArgTranslations[key] = translated
				}
				return translated, ""
			}
		}
		return definition.calls, ""
	}
	return table.translateHelperCall(call, definition)
}

func usesCStringOperations(calls []ParsedFunction) bool {
	for _, call := range calls {
		switch strings.ToLower(strings.TrimSpace(call.Name)) {
		case "strncpy", "strcpy", "strcat", "sprintf", "memset":
			return true
		}
	}
	return false
}

// excludingFile returns the table without the helpers file defines.
func (table sourceHelperTable) excludingFile(file string) sourceHelperTable {
	out := table
	out.definitions = make(map[string]*sourceHelperDefinition, len(table.definitions))
	for key, definition := range table.definitions {
		if !strings.EqualFold(definition.file, file) {
			out.definitions[key] = definition
		}
	}
	return out
}

// definesHelper reports whether name is a C function some parsed code calls,
// so its body runs only through those calls.
func (table sourceHelperTable) definesHelper(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	_, ok := table.definitions[key]
	return ok && !table.ambiguous[key]
}

// entryFunctions keeps the calls an action's entry function makes. A file that
// also defines helpers emitted every helper body at top level; those bodies
// now run only where they are called. Calls without an enclosing function
// (legacy fallback parsing) are kept. When no call belongs to the entry
// function, matched is false and the whole file is returned: its helper bodies
// still run at top level, so callers must not also inline them.
func entryFunctions(functions []ParsedFunction, entry string) (kept []ParsedFunction, matched bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return functions, false
	}
	for _, function := range functions {
		if strings.EqualFold(strings.TrimSpace(function.sourceFunction), entry) {
			matched = true
			break
		}
	}
	if !matched {
		return functions, false
	}
	kept = make([]ParsedFunction, 0, len(functions))
	for _, function := range functions {
		owner := strings.TrimSpace(function.sourceFunction)
		if owner == "" || strings.EqualFold(owner, entry) {
			kept = append(kept, function)
		}
	}
	return kept, true
}

// withoutEmbeddedCalls drops every expression call; they run only through
// helper inlining.
func withoutEmbeddedCalls(functions []ParsedFunction) []ParsedFunction {
	kept := functions[:0:0]
	for _, function := range functions {
		if function.embeddedCall == "" {
			kept = append(kept, function)
		}
	}
	return kept
}

// inlineSourceHelpers replaces each call to a defined helper with the helper's
// calls, recursively. An inlined call carries the caller's enclosing
// conditions and loops, and its own condition and loop identities are
// qualified by the call site so two calls of one helper stay distinct blocks.
// files lists the source files whose helper bodies were inlined.
func inlineSourceHelpers(functions []ParsedFunction, table sourceHelperTable) (out []ParsedFunction, warnings []string, files []string) {
	if len(table.definitions) == 0 {
		return withoutEmbeddedCalls(functions), nil, nil
	}
	produced := 0
	budgetWarned := false
	usedFiles := map[string]string{}
	var expand func(calls []ParsedFunction, stack []string) []ParsedFunction
	expand = func(calls []ParsedFunction, stack []string) []ParsedFunction {
		out := make([]ParsedFunction, 0, len(calls))
		for _, call := range calls {
			definition, ok := table.definitionFor(call)
			if !ok {
				if call.embeddedCall == "" {
					out = append(out, call)
				}
				continue
			}
			at := fmt.Sprintf("%s:%d", call.Filename, call.LineNumber)
			if call.embeddedCall == embeddedCallReview {
				warnings = append(warnings, fmt.Sprintf("%s: %s(...) is called in a loop condition or inside another call's arguments; its body was not inlined and the call needs review", at, definition.name))
				out = append(out, call)
				continue
			}
			key := strings.ToLower(definition.name)
			if stackContains(stack, key) {
				warnings = append(warnings, fmt.Sprintf("%s: %s() calls itself through %s; the recursive call was not inlined and needs review", at, definition.name, strings.Join(append(stack, key), " -> ")))
				out = append(out, call)
				continue
			}
			if len(stack) >= maxSourceHelperInlineDepth {
				warnings = append(warnings, fmt.Sprintf("%s: %s() is nested more than %d calls deep; it was not inlined and needs review", at, definition.name, maxSourceHelperInlineDepth))
				out = append(out, call)
				continue
			}
			body, reason := table.helperCalls(call, definition)
			if reason != "" {
				warnings = append(warnings, fmt.Sprintf("%s: %s(...) was not inlined because %s; the call needs review", at, definition.name, reason))
				out = append(out, call)
				continue
			}
			if produced+len(body) > maxInlinedSourceCalls {
				if !budgetWarned {
					budgetWarned = true
					warnings = append(warnings, fmt.Sprintf("%s: inlining %s() would exceed %d calls for one action; helper calls past the limit were not inlined and need review", at, definition.name, maxInlinedSourceCalls))
				}
				out = append(out, call)
				continue
			}
			produced += len(body)
			usedFiles[strings.ToLower(definition.file)] = definition.file
			site := fmt.Sprintf("inline@%s:%d:%d/", call.Filename, call.LineNumber, call.sourceColumn)
			for _, inner := range expand(body, append(append([]string(nil), stack...), key)) {
				inner.Phase = call.Phase
				inner.Conditions = append(append([]string(nil), call.Conditions...), inner.Conditions...)
				inner.ConditionIDs = append(append([]string(nil), call.ConditionIDs...), prefixedIDs(site, inner.ConditionIDs)...)
				inner.Loops = append(append([]string(nil), call.Loops...), inner.Loops...)
				inner.LoopIDs = append(append([]string(nil), call.LoopIDs...), prefixedIDs(site, inner.LoopIDs)...)
				inner.dependsOnUnsupportedTransactionFlow = inner.dependsOnUnsupportedTransactionFlow || call.dependsOnUnsupportedTransactionFlow
				// Conversion may rewrite a call's argument maps (web_find aliases
				// What to Text); each inlined copy owns its own.
				inner.Args = maps.Clone(inner.Args)
				inner.Headers = maps.Clone(inner.Headers)
				out = append(out, inner)
			}
		}
		return out
	}
	out = expand(functions, nil)
	for _, file := range usedFiles {
		files = append(files, file)
	}
	return out, uniqueSortedStrings(warnings), uniqueSortedStrings(files)
}

func prefixedIDs(prefix string, ids []string) []string {
	out := make([]string, len(ids))
	for index, id := range ids {
		out[index] = prefix + id
	}
	return out
}

func stackContains(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return true
		}
	}
	return false
}
