package bruno

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	bruEnvBlockHeaderPattern    = regexp.MustCompile(`^[ \t]*vars(?:\s*:\s*(secret))?[ \t]*\{(.*)$`)
	bruEnvFlagListHeaderPattern = regexp.MustCompile(`^[ \t]*vars\s*:\s*secret[ \t]*\[(.*)$`)
	bruEnvFieldLinePattern      = regexp.MustCompile(`^[ \t]*([A-Za-z_][A-Za-z0-9_.-]*)\s*:\s*(.*?)\s*$`)
)

type bruEnvField struct {
	name  string
	value string
}

type bruEnvBlock struct {
	flagged bool
	fields  []bruEnvField
}

func isBruClosingBraceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "}" {
		return true
	}
	if strings.HasPrefix(trimmed, "}") {
		after := strings.TrimSpace(trimmed[1:])
		return strings.HasPrefix(after, "#")
	}
	return false
}

func stripBruLeadingComment(s string) string {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "#") {
		return ""
	}
	return trimmed
}

func parseBruEnvBlocks(data []byte) []bruEnvBlock {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var blocks []bruEnvBlock
	for i := 0; i < len(lines); i++ {
		header := bruEnvBlockHeaderPattern.FindStringSubmatch(lines[i])
		if header == nil {
			continue
		}
		flagged := strings.EqualFold(strings.TrimSpace(header[1]), "secret")
		rest := stripBruLeadingComment(header[2])
		var bodyLines []string
		if rest == "" {
			closeIdx := -1
			for j := i + 1; j < len(lines); j++ {
				if isBruClosingBraceLine(lines[j]) {
					closeIdx = j
					break
				}
				if bruEnvBlockHeaderPattern.MatchString(lines[j]) || bruEnvFlagListHeaderPattern.MatchString(lines[j]) {
					break
				}
			}
			if closeIdx < 0 {
				continue
			}
			bodyLines = lines[i+1 : closeIdx]
			i = closeIdx
		} else {
			inner, endIdx, ok := scanBruBracedBody(header[2], lines, i)
			if !ok {
				continue
			}
			bodyLines = strings.Split(inner, "\n")
			i = endIdx
		}
		var fields []bruEnvField
		for _, rawLine := range bodyLines {
			match := bruEnvFieldLinePattern.FindStringSubmatch(rawLine)
			if len(match) < 3 {
				continue
			}
			name := strings.TrimSpace(match[1])
			if name == "" {
				continue
			}
			fields = append(fields, bruEnvField{
				name:  name,
				value: unwrapDotEnvValue(strings.TrimSpace(match[2])),
			})
		}
		blocks = append(blocks, bruEnvBlock{
			flagged: flagged,
			fields:  fields,
		})
	}
	return blocks
}

func scanBruBracedBody(firstRemainder string, lines []string, startIdx int) (string, int, bool) {
	const (
		scanExpectKey = iota
		scanInKey
		scanExpectValueStart
		scanInUnquotedValue
	)
	depth := 1
	inSingle := false
	inDouble := false
	escaped := false
	scanState := scanExpectKey
	var buf strings.Builder
	for idx := startIdx; idx < len(lines); idx++ {
		segment := firstRemainder
		if idx > startIdx {
			buf.WriteByte('\n')
			segment = lines[idx]
			inSingle = false
			inDouble = false
			escaped = false
			scanState = scanExpectKey
		}
		for pos := 0; pos < len(segment); pos++ {
			ch := segment[pos]
			if escaped {
				escaped = false
				buf.WriteByte(ch)
				continue
			}
			if (inSingle || inDouble) && ch == '\\' {
				escaped = true
				buf.WriteByte(ch)
				continue
			}
			if inSingle {
				if ch == '\'' {
					inSingle = false
					scanState = scanInUnquotedValue
				}
				buf.WriteByte(ch)
				continue
			}
			if inDouble {
				if ch == '"' {
					inDouble = false
					scanState = scanInUnquotedValue
				}
				buf.WriteByte(ch)
				continue
			}
			if ch == '{' {
				depth++
				if scanState == scanExpectValueStart {
					scanState = scanInUnquotedValue
				}
				buf.WriteByte(ch)
				continue
			}
			if ch == '}' {
				depth--
				if depth == 0 {
					return buf.String(), idx, true
				}
				buf.WriteByte(ch)
				continue
			}
			switch scanState {
			case scanExpectKey:
				if ch == ' ' || ch == '\t' {
					buf.WriteByte(ch)
					continue
				}
				if ch == '#' {
					pos = len(segment)
					continue
				}
				if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_' || ch == '~' {
					scanState = scanInKey
				} else {
					scanState = scanInUnquotedValue
				}
			case scanInKey:
				if ch == ':' {
					scanState = scanExpectValueStart
				} else if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' || ch == '-' || ch == ' ' || ch == '\t' {
					// Remain in scanInKey until ':' or non-key token.
				} else {
					scanState = scanInUnquotedValue
				}
			case scanExpectValueStart:
				if ch == ' ' || ch == '\t' {
					buf.WriteByte(ch)
					continue
				}
				if ch == '\'' {
					inSingle = true
					buf.WriteByte(ch)
					continue
				}
				if ch == '"' {
					inDouble = true
					buf.WriteByte(ch)
					continue
				}
				scanState = scanInUnquotedValue
			}
			buf.WriteByte(ch)
		}
	}
	return "", startIdx, false
}

func stripBruUnquotedComment(line string) string {
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if (inSingle || inDouble) && ch == '\\' {
			escaped = true
			continue
		}
		if !inDouble && ch == '\'' {
			inSingle = !inSingle
			continue
		}
		if !inSingle && ch == '"' {
			inDouble = !inDouble
			continue
		}
		if !inSingle && !inDouble && ch == '#' {
			return line[:i]
		}
	}
	return line
}

func parseBruFlaggedVarNames(data []byte) map[string]bool {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	flagged := map[string]bool{}
	addTokens := func(raw string) {
		for _, part := range strings.Split(raw, ",") {
			token := strings.Trim(strings.TrimSpace(part), `"'`)
			if strings.HasPrefix(token, "~") || token == "" {
				continue
			}
			flagged[token] = true
		}
	}
	for i := 0; i < len(lines); i++ {
		header := bruEnvFlagListHeaderPattern.FindStringSubmatch(lines[i])
		if header == nil {
			continue
		}
		rest := strings.TrimSpace(stripBruUnquotedComment(header[1]))
		if closeIdx := strings.IndexByte(rest, ']'); closeIdx >= 0 {
			addTokens(rest[:closeIdx])
			continue
		}
		addTokens(rest)
		for j := i + 1; j < len(lines); j++ {
			if bruEnvBlockHeaderPattern.MatchString(lines[j]) || bruEnvFlagListHeaderPattern.MatchString(lines[j]) {
				break
			}
			trimmed := strings.TrimSpace(stripBruUnquotedComment(lines[j]))
			if closeIdx := strings.IndexByte(trimmed, ']'); closeIdx >= 0 {
				addTokens(trimmed[:closeIdx])
				i = j
				break
			}
			addTokens(trimmed)
		}
	}
	return flagged
}

// environmentVariableValues reads the plain values an environment or .env file
// defines, including secret-flagged and literal redaction-sentinel entries,
// skipping only disabled entries and template-bearing expressions.
func environmentVariableValues(data []byte, extension string) map[string]string {
	values := map[string]string{}
	add := func(name, value string) {
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if name == "" || value == "" || strings.Contains(value, "{{") {
			return
		}
		values[name] = value
	}
	switch strings.ToLower(extension) {
	case ".env":
		for _, entry := range parseDotEnvEntries(data) {
			add(entry.Name, entry.Value)
		}
	case ".bru":
		for _, block := range parseBruEnvBlocks(data) {
			for _, field := range block.fields {
				add(field.name, field.value)
			}
		}
	default:
		var document any
		if yaml.Unmarshal(data, &document) != nil {
			return nil
		}
		var walk func(any)
		walk = func(current any) {
			switch typed := current.(type) {
			case map[string]any:
				name := anyString(typed["name"])
				raw, hasValue := typed["value"]
				enabled, hasEnabled := typed["enabled"].(bool)
				disabled := nativeBool(typed["disabled"])
				if name != "" && hasValue && (!hasEnabled || enabled) && !disabled && nativeScalarValue(raw) {
					add(name, anyString(raw))
				}
				for key, child := range typed {
					if strings.EqualFold(key, "variables") || strings.EqualFold(key, "vars") {
						if varMap, ok := child.(map[string]any); ok && !(anyString(varMap["name"]) != "" && varMap["value"] != nil) {
							for varName, varRaw := range varMap {
								if nativeScalarValue(varRaw) {
									add(varName, anyString(varRaw))
									continue
								}
								if entryMap, ok := varRaw.(map[string]any); ok && anyString(entryMap["name"]) == "" {
									rawVal, hasVal := entryMap["value"]
									entryEnabled, entryHasEnabled := entryMap["enabled"].(bool)
									entryDisabled := nativeBool(entryMap["disabled"])
									if hasVal && (!entryHasEnabled || entryEnabled) && !entryDisabled && nativeScalarValue(rawVal) {
										add(varName, anyString(rawVal))
									}
								}
							}
						}
					}
					walk(child)
				}
			case []any:
				for _, child := range typed {
					walk(child)
				}
			}
		}
		walk(document)
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

type dotEnvEntry struct {
	Name  string
	Value string
	Line  int
}

func parseDotEnvEntries(data []byte) []dotEnvEntry {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	entries := make([]dotEnvEntry, 0, len(lines))
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if rest, ok := strings.CutPrefix(key, "export "); ok {
			key = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(key, "export\t"); ok {
			key = strings.TrimSpace(rest)
		}
		if key == "" {
			continue
		}
		value := unwrapDotEnvValue(strings.TrimSpace(line[eq+1:]))
		entries = append(entries, dotEnvEntry{
			Name:  key,
			Value: value,
			Line:  index + 1,
		})
	}
	return entries
}

func unwrapDotEnvValue(raw string) string {
	if len(raw) >= 2 {
		if (raw[0] == '"' && raw[len(raw)-1] == '"') ||
			(raw[0] == '\'' && raw[len(raw)-1] == '\'') {
			return raw[1 : len(raw)-1]
		}
	}
	return raw
}

// nativeBaseEnvironmentVariables collects unambiguous .env and secret-flagged
// structured environment definitions so native request projection resolves them
// upfront while keeping ordinary non-secret environment variables selectable in
// SharedValues review.
func nativeBaseEnvironmentVariables(files []sourceFile) map[string]nativeVariable {
	return collectEnvironmentVariableDefinitions(files, true)
}

// nativeFallbackEnvironmentVariables collects all unambiguous scalar variables
// across .env, .bru, and YAML/JSON environment files so credential-position
// templates (Authorization headers, auth blocks, API-key query params, and
// credential body fields) resolve literal environment values instead of
// tokenizing or promotion-blocking them when secret: true is omitted.
func nativeFallbackEnvironmentVariables(files []sourceFile) map[string]nativeVariable {
	return collectEnvironmentVariableDefinitions(files, false)
}

type flaggedEnvVariable struct {
	variable nativeVariable
	flagged  bool
}

func collectEnvironmentVariableDefinitions(files []sourceFile, onlyFlagged bool) map[string]nativeVariable {
	type envCandidate struct {
		variable   nativeVariable
		anyFlagged bool
		conflicted bool
	}
	dotEnvVars := make(map[string]envCandidate)
	namedEnvVars := make(map[string]envCandidate)

	recordDotEnv := func(v nativeVariable) {
		existing, seen := dotEnvVars[v.Name]
		if !seen {
			dotEnvVars[v.Name] = envCandidate{variable: v, anyFlagged: true}
			return
		}
		if existing.conflicted || existing.variable.Value != v.Value {
			existing.conflicted = true
			dotEnvVars[v.Name] = existing
		}
	}

	for _, file := range files {
		if classifyFileRole(file.Path, file.Data) != "environment" {
			continue
		}
		lower := strings.ToLower(file.Path)
		ext := path.Ext(lower)
		if ext == ".env" || path.Base(lower) == ".env" {
			perFile := make(map[string]nativeVariable)
			var order []string
			for _, entry := range parseDotEnvEntries(file.Data) {
				if _, exists := perFile[entry.Name]; !exists {
					order = append(order, entry.Name)
				}
				perFile[entry.Name] = nativeVariable{
					Name:              entry.Name,
					Value:             entry.Value,
					ValueIsString:     true,
					SourcePath:        file.Path,
					DefinitionPointer: "/line/" + strconv.Itoa(entry.Line),
				}
			}
			for _, key := range order {
				v := perFile[key]
				recordDotEnv(v)
				procVar := v
				procVar.Name = "process.env." + v.Name
				recordDotEnv(procVar)
			}
			continue
		}
		fileVars := parseStructuredEnvironmentVariables(file, ext)
		for _, item := range fileVars {
			v := item.variable
			existing, seen := namedEnvVars[v.Name]
			if !seen {
				namedEnvVars[v.Name] = envCandidate{
					variable:   v,
					anyFlagged: item.flagged,
				}
				continue
			}
			if item.flagged && !existing.anyFlagged {
				existing.anyFlagged = true
			}
			if existing.conflicted || existing.variable.Value != v.Value {
				existing.conflicted = true
			}
			namedEnvVars[v.Name] = existing
		}
	}

	resolved := make(map[string]nativeVariable, len(dotEnvVars)+len(namedEnvVars))
	for name, candidate := range dotEnvVars {
		if candidate.conflicted {
			continue
		}
		resolved[name] = candidate.variable
	}
	names := make([]string, 0, len(namedEnvVars))
	for name := range namedEnvVars {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		candidate := namedEnvVars[name]
		if candidate.conflicted {
			delete(resolved, name)
			continue
		}
		if onlyFlagged && !candidate.anyFlagged {
			if dotCandidate, hasDot := dotEnvVars[name]; hasDot && !dotCandidate.conflicted && dotCandidate.variable.Value != candidate.variable.Value {
				delete(resolved, name)
			}
			continue
		}
		resolved[name] = candidate.variable
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func parseStructuredEnvironmentVariables(file sourceFile, ext string) []flaggedEnvVariable {
	perFile := make(map[string]flaggedEnvVariable)
	var order []string
	put := func(v nativeVariable, flagged bool) {
		if v.Name == "" {
			return
		}
		if _, exists := perFile[v.Name]; !exists {
			order = append(order, v.Name)
		}
		perFile[v.Name] = flaggedEnvVariable{variable: v, flagged: flagged}
	}

	if ext == ".bru" {
		flaggedNames := parseBruFlaggedVarNames(file.Data)
		for _, block := range parseBruEnvBlocks(file.Data) {
			pointerPrefix := "/vars/"
			if block.flagged {
				pointerPrefix = "/vars:secret/"
			}
			for _, field := range block.fields {
				put(nativeVariable{
					Name:              field.name,
					Value:             field.value,
					ValueIsString:     true,
					SourcePath:        file.Path,
					DefinitionPointer: pointerPrefix + nativeJSONPointerEscape(field.name),
				}, block.flagged || flaggedNames[field.name])
			}
		}
		out := make([]flaggedEnvVariable, 0, len(order))
		for _, name := range order {
			out = append(out, perFile[name])
		}
		return out
	}

	var document any
	if yaml.Unmarshal(file.Data, &document) != nil {
		return nil
	}
	var walk func(any, string)
	walk = func(current any, pointer string) {
		switch typed := current.(type) {
		case map[string]any:
			name := strings.TrimSpace(anyString(typed["name"]))
			raw, hasValue := typed["value"]
			secret := nativeBool(typed["secret"])
			enabled, hasEnabled := typed["enabled"].(bool)
			disabled := nativeBool(typed["disabled"])
			if name != "" && hasValue && (!hasEnabled || enabled) && !disabled && nativeScalarValue(raw) {
				put(nativeVariable{
					Name:              name,
					Value:             anyString(raw),
					ValueIsString:     nativeStringScalar(raw),
					SourcePath:        file.Path,
					DefinitionPointer: pointer + "/value",
				}, secret)
			}
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				keyPtr := pointer + "/" + nativeJSONPointerEscape(key)
				if strings.EqualFold(key, "variables") || strings.EqualFold(key, "vars") {
					if varMap, ok := typed[key].(map[string]any); ok && !(strings.TrimSpace(anyString(varMap["name"])) != "" && varMap["value"] != nil) {
						varKeys := make([]string, 0, len(varMap))
						for varKey := range varMap {
							varKeys = append(varKeys, varKey)
						}
						sort.Strings(varKeys)
						for _, varKey := range varKeys {
							varName := strings.TrimSpace(varKey)
							if varName == "" {
								continue
							}
							varRaw := varMap[varKey]
							varPtr := keyPtr + "/" + nativeJSONPointerEscape(varKey)
							if nativeScalarValue(varRaw) {
								put(nativeVariable{
									Name:              varName,
									Value:             anyString(varRaw),
									ValueIsString:     nativeStringScalar(varRaw),
									SourcePath:        file.Path,
									DefinitionPointer: varPtr,
								}, false)
								continue
							}
							if entryMap, ok := varRaw.(map[string]any); ok && strings.TrimSpace(anyString(entryMap["name"])) == "" {
								rawVal, hasVal := entryMap["value"]
								entrySecret := nativeBool(entryMap["secret"])
								entryEnabled, entryHasEnabled := entryMap["enabled"].(bool)
								entryDisabled := nativeBool(entryMap["disabled"])
								if hasVal && (!entryHasEnabled || entryEnabled) && !entryDisabled && nativeScalarValue(rawVal) {
									put(nativeVariable{
										Name:              varName,
										Value:             anyString(rawVal),
										ValueIsString:     nativeStringScalar(rawVal),
										SourcePath:        file.Path,
										DefinitionPointer: varPtr + "/value",
									}, entrySecret)
								}
							}
						}
					}
				}
				walk(typed[key], keyPtr)
			}
		case []any:
			for index, child := range typed {
				walk(child, pointer+"/"+strconv.Itoa(index))
			}
		}
	}
	walk(document, "")
	out := make([]flaggedEnvVariable, 0, len(order))
	for _, name := range order {
		out = append(out, perFile[name])
	}
	return out
}
