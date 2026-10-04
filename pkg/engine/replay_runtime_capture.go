package engine

import (
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

type replayRuntimeProjector struct {
	configs                []*RedactionConfig
	policy                 *RedactionConfig
	work                   int
	input                  int
	omittedValues          int
	workUnavailable        bool
	publicationUnavailable bool
}

func (p *replayRuntimeProjector) charge(n int) bool {
	if n < 0 || n > maxReplayRuntimeProjectionWork-p.work {
		p.workUnavailable = true
		return false
	}
	p.work += n
	return true
}
func (p *replayRuntimeProjector) inspect(name, value string) bool {
	if len(name) > maxReplayRuntimeInputBytes-p.input {
		p.workUnavailable = true
		return false
	}
	p.input += len(name)
	if len(value) > maxReplayRuntimeInputBytes-p.input {
		p.workUnavailable = true
		return false
	}
	p.input += len(value)
	return p.charge(len(name) + len(value) + 1)
}

func (vu *VUser) runtimeSnapshotProjector() *replayRuntimeProjector {
	p := &replayRuntimeProjector{}
	if vu == nil || vu.resultPublicationUnavailable() {
		p.publicationUnavailable = true
		return p
	}
	config := vu.resultPublicationRedactionConfig()
	p.policy = config
	if config != nil && (len(config.compiled) > MaxRedactionRules || len(config.Rules) != len(config.compiled)) {
		p.publicationUnavailable = true
		return p
	}
	if config != nil {
		for _, pattern := range config.compiled {
			if pattern.re == nil || len(pattern.re.String()) > MaxRedactionPatternBytes || len(pattern.repl) > MaxRedactionReplacementBytes {
				p.publicationUnavailable = true
				return p
			}
		}
	}
	for depth := 0; config != nil; depth++ {
		if depth >= maxRuntimeSecretRedactionLayers || config.runtimePublicationUnavailable || len(config.secretReplacers) > maxRuntimeSecretRedactionReplacers || (len(config.runtimeSecretValues) > 0 && len(config.secretReplacers) == 0) {
			p.publicationUnavailable = true
			return p
		}
		for _, replacer := range config.secretReplacers {
			if replacer == nil {
				p.publicationUnavailable = true
				return p
			}
		}
		p.configs = append(p.configs, config)
		config = config.runtimeParent
	}
	if vu.replayRuntime == nil || vu.replayRuntime.workUnavailable {
		p.workUnavailable = true
		return p
	}
	count := len(vu.Variables) + len(vu.replayRuntime.rows) + len(vu.replayRuntime.frames)
	if vu.Parameters != nil {
		count += len(vu.Parameters.order)
	}
	if count > maxReplayRuntimeInputEntries {
		p.workUnavailable = true
		return p
	}
	known := map[string]struct{}{}
	add := func(name, value string) bool {
		if !p.inspect(name, value) {
			return false
		}
		if scrub.SensitiveName(name) && value != "" && value != redactedPayloadValue {
			known[value] = struct{}{}
		}
		return true
	}
	// Complete privacy closure precedes display selection. A secret in a binding
	// omitted by the 256-entry display limit still masks every visible replica.
	for name, value := range vu.Variables {
		if !add(name, value) {
			return p
		}
	}
	if pr := vu.Parameters; pr != nil {
		for _, name := range pr.order {
			observation, found := pr.runtimeObservations[name]
			if found {
				if !add(name, observation.value) {
					return p
				}
				if observation.row != nil {
					count++
					if count > maxReplayRuntimeInputEntries {
						p.workUnavailable = true
						return p
					}
					if !p.inspect(observation.row.Name, observation.row.Source) {
						return p
					}
				}
			} else if !add(name, "") {
				return p
			}
		}
	}
	for _, row := range vu.replayRuntime.rows {
		if !p.inspect(row.Name, row.Source) {
			return p
		}
	}
	for _, frame := range vu.replayRuntime.frames {
		if len(frame.StepPath) > MaxReplayRuntimeSnapshotPathDepth || len(frame.IterationPath) > MaxReplayRuntimeSnapshotPathDepth {
			p.workUnavailable = true
			return p
		}
		for _, segment := range frame.StepPath {
			if !p.inspect("", segment) {
				return p
			}
		}
	}
	if len(known) > 0 {
		values := make([]string, 0, len(known))
		for value := range known {
			// Reversible spelling construction is bounded before entering the shared
			// matcher constructor, rather than rebuilding the large inherited graph.
			if len(value) > maxReplayRuntimeProjectionWork/512 || !p.charge(512*(len(value)+1)) {
				p.workUnavailable = true
				return p
			}
			values = append(values, value)
		}
		sort.Strings(values)
		local, err := withSecretValueRedaction(nil, values)
		if err != nil {
			p.publicationUnavailable = true
			return p
		}
		if local != nil {
			p.configs = append(p.configs, local)
		}
	}
	return p
}

func (p *replayRuntimeProjector) omit() (string, bool) { p.omittedValues++; return "", false }
func (p *replayRuntimeProjector) exact(value string) (string, bool) {
	if !p.charge(len(value) + 1) {
		return "", false
	}
	matched := make([]bool, len(value))
	any := false
	for _, config := range p.configs {
		for _, replacer := range config.secretReplacers {
			if !p.charge(3 * (len(value) + 1)) {
				return "", false
			}
			candidate := replacer.Replace(value)
			if len(candidate) != len(value) {
				p.publicationUnavailable = true
				return "", false
			}
			for i := range candidate {
				if candidate[i] == 0 {
					matched[i] = true
					any = true
				}
			}
		}
	}
	if any {
		return expandRuntimeSecretMatches(value, matched), true
	}
	return value, true
}
func (p *replayRuntimeProjector) applyPattern(value string, pattern payloadReplacement) (string, bool) {
	if pattern.re == nil || len(pattern.re.String()) > MaxRedactionPatternBytes || len(pattern.repl) > MaxRedactionReplacementBytes {
		p.publicationUnavailable = true
		return "", false
	}
	if !p.charge((len(value) + 1) * (len(pattern.re.String()) + 1)) {
		return "", false
	}
	const matchesLimit = 256
	matches := pattern.re.FindAllStringSubmatchIndex(value, matchesLimit+1)
	if len(matches) > matchesLimit {
		return p.omit()
	}
	if len(matches) == 0 {
		return value, true
	}
	var out strings.Builder
	last := 0
	for _, match := range matches {
		// ExpandString can repeat an entire capture many times. Bound that work
		// before allocation, and omit the complete value if it cannot be scanned.
		bound := (len(pattern.repl) + 1) * (len(value) + 1)
		if bound > MaxReplayRuntimeSnapshotJSONBytes {
			return p.omit()
		}
		if !p.charge(bound) {
			return "", false
		}
		replacement := pattern.re.ExpandString(nil, pattern.repl, value, match)
		if out.Len()+match[0]-last+len(replacement) > MaxReplayRuntimeSnapshotValueBytes {
			return p.omit()
		}
		out.WriteString(value[last:match[0]])
		out.Write(replacement)
		last = match[1]
	}
	if out.Len()+len(value)-last > MaxReplayRuntimeSnapshotValueBytes {
		return p.omit()
	}
	out.WriteString(value[last:])
	return out.String(), true
}
func (p *replayRuntimeProjector) text(value string) (string, bool) {
	// Never publish a raw prefix of an oversized, malformed or NUL-bearing value.
	if len(value) > MaxReplayRuntimeSnapshotValueBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return p.omit()
	}
	value, ok := p.exact(value)
	if !ok {
		return "", false
	}
	if p.policy != nil {
		for _, pattern := range p.policy.compiled {
			value, ok = p.applyPattern(value, pattern)
			if !ok {
				return "", false
			}
		}
	}
	value, ok = p.exact(value)
	if !ok {
		return "", false
	}
	if !p.charge(128 * (len(value) + 1)) {
		return "", false
	}
	value = scrub.ValueLabel(value, scrub.Policy{})
	// Structured scrub policy can introduce a string that itself matches a known
	// value. The final exact pass closes those neutral-key replicas as well.
	value, ok = p.exact(value)
	if !ok {
		return "", false
	}
	if len(value) > MaxReplayRuntimeSnapshotValueBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return p.omit()
	}
	return strings.Clone(value), true
}
func (p *replayRuntimeProjector) name(value string) string {
	out, ok := p.text(value)
	if !ok {
		return "[OMITTED]"
	}
	return out
}
func (p *replayRuntimeProjector) value(name, value, state string) (string, *string) {
	if scrub.SensitiveName(name) {
		masked := redactedPayloadValue
		return "redacted", &masked
	}
	out, ok := p.text(value)
	if !ok {
		return "omitted", nil
	}
	if out != value {
		state = "redacted"
	}
	return state, &out
}
func (p *replayRuntimeProjector) location(location ReplayDebugAction) ReplayDebugAction {
	if len(location.StepPath) > MaxReplayRuntimeSnapshotPathDepth {
		location.StepPath = nil
		p.omittedValues++
	}
	if len(location.IterationPath) > MaxReplayRuntimeSnapshotPathDepth {
		location.IterationPath = nil
		location.Iteration = nil
		p.omittedValues++
	}
	out := *cloneReplayDebugAction(location)
	// Phase, ordinals, generated event identities and structural paths are typed
	// execution receipts. Authored labels and target fields receive strict policy.
	out.ActionName = p.name(out.ActionName)
	out.Target = p.name(out.Target)
	out.Selector = p.name(out.Selector)
	out.SourceStepID = p.name(out.SourceStepID)
	out.ParentActionName = p.name(out.ParentActionName)
	if _, known := dsl.SpecForActionType(func() string {
		if len(out.ActionType) > MaxReplayRuntimeSnapshotValueBytes {
			return ""
		}
		return out.ActionType
	}()); !known {
		out.ActionType = p.name(out.ActionType)
	}
	if !runtimeEnum(out.Method, "", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CONNECT", "TRACE") {
		out.Method = p.name(out.Method)
	}
	for i, segment := range out.StepPath {
		if !runtimeStructuralPath(segment) || len(segment) > MaxReplayRuntimeSnapshotPathSegmentBytes {
			out.StepPath = nil
			p.omittedValues++
			break
		}
		out.StepPath[i] = strings.Clone(segment)
	}
	return out
}
func replayRuntimeJSONSize(value any) (int, bool) {
	remaining := MaxReplayRuntimeSnapshotJSONBytes
	ok := countReplayRuntimeJSON(reflect.ValueOf(value), &remaining)
	return MaxReplayRuntimeSnapshotJSONBytes - remaining, ok
}

func (vu *VUser) captureReplayRuntimeSnapshot(boundary string, location ReplayDebugAction) ReplayRuntimeSnapshot {
	p := vu.runtimeSnapshotProjector()
	unavailable := func() ReplayRuntimeSnapshot {
		if p.publicationUnavailable {
			return emptyReplayRuntimeSnapshot(boundary, "publication_unavailable", true)
		}
		return emptyReplayRuntimeSnapshot(boundary, "work_limit", false)
	}
	if p.publicationUnavailable || p.workUnavailable {
		return unavailable()
	}
	if len(location.StepPath)+len(location.IterationPath) > maxReplayRuntimeInputEntries {
		p.workUnavailable = true
		return unavailable()
	}
	for _, value := range []string{location.ActionName, location.ActionType, location.Method, location.Target, location.Selector, location.SourceStepID, location.ParentActionName} {
		if !p.inspect("", value) {
			return unavailable()
		}
	}
	for _, segment := range location.StepPath {
		if !p.inspect("", segment) {
			return unavailable()
		}
	}
	if !runtimeTextBounded(location.EventID) {
		p.workUnavailable = true
		return unavailable()
	}
	s := ReplayRuntimeSnapshot{State: "complete", Boundary: boundary, Location: p.location(location), Variables: []ReplayRuntimeBinding{}, Parameters: []ReplayRuntimeParameter{}, Rows: []ReplayRuntimeRow{}, Frames: []ReplayRuntimeFrame{}}
	size, ok := replayRuntimeJSONSize(s)
	if !ok { // Authored location fields are whole-field omissions, never prefixes.
		for _, field := range []*string{&s.Location.Target, &s.Location.Selector, &s.Location.ActionName, &s.Location.ParentActionName, &s.Location.SourceStepID} {
			if *field != "" {
				*field = ""
				p.omittedValues++
			}
			size, ok = replayRuntimeJSONSize(s)
			if ok {
				break
			}
		}
	}
	if !ok {
		p.workUnavailable = true
		return unavailable()
	}
	remaining := MaxReplayRuntimeSnapshotJSONBytes - size - 64 // growing omission counters
	entries := 0
	admit := func(value any, count int) bool {
		if entries >= MaxReplayRuntimeSnapshotEntries {
			return false
		}
		size, ok := replayRuntimeJSONSize(value)
		if !p.charge(size + 1) {
			return false
		}
		if count > 0 {
			size++
		}
		if !ok || size > remaining {
			return false
		}
		remaining -= size
		entries++
		return true
	}
	// Keep the current execution context and actual row receipts ahead of large
	// binding lists. Every discarded item is counted in the full replacement.
	for _, frame := range vu.replayRuntime.frames {
		if len(s.Frames) >= MaxReplayRuntimeSnapshotFrames || !admit(frame, len(s.Frames)) {
			s.Omissions.Frames++
			continue
		}
		frame.StepPath = append([]string(nil), frame.StepPath...)
		frame.IterationPath = append([]int(nil), frame.IterationPath...)
		frame.BranchIndex = cloneRuntimeInt(frame.BranchIndex)
		s.Frames = append(s.Frames, frame)
	}
	rows := make([]ReplayRuntimeRow, 0, len(vu.replayRuntime.rows))
	for _, row := range vu.replayRuntime.rows {
		rows = append(rows, row)
	}
	if vu.Parameters != nil {
		for _, name := range vu.Parameters.order {
			if o, found := vu.Parameters.runtimeObservations[name]; found && o.row != nil {
				rows = append(rows, *o.row)
			}
		}
	}
	if !p.charge((p.input + len(rows) + 1) * 14) {
		return unavailable()
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		if rows[i].Source != rows[j].Source {
			return rows[i].Source < rows[j].Source
		}
		return rows[i].Name < rows[j].Name
	})
	for _, row := range rows {
		if len(s.Rows) >= MaxReplayRuntimeSnapshotRows || entries >= MaxReplayRuntimeSnapshotEntries {
			s.Omissions.Rows++
			continue
		}
		row.Source = p.name(row.Source)
		row.Name = p.name(row.Name)
		row.RowOrdinal = cloneRuntimeInt(row.RowOrdinal)
		if len(s.Rows) >= MaxReplayRuntimeSnapshotRows || !admit(row, len(s.Rows)) {
			s.Omissions.Rows++
			continue
		}
		s.Rows = append(s.Rows, row)
	}
	if pr := vu.Parameters; pr != nil {
		for _, name := range pr.order {
			if entries >= MaxReplayRuntimeSnapshotEntries {
				s.Omissions.Parameters++
				continue
			}
			parameter := pr.params[name]
			o, found := pr.runtimeObservations[name]
			row := ReplayRuntimeParameter{Name: p.name(name), State: "not_evaluated", ValueState: "omitted", UpdatePolicy: effectiveParameterUpdate(parameter)}
			if !runtimeEnum(row.UpdatePolicy, "once", "each_iteration", "each_occurrence") {
				row.UpdatePolicy = "unavailable"
			}
			if found {
				row.State = o.state
				row.ObservedEventID = o.eventID
				if runtimeEnum(o.state, "prepared", "observed") {
					state := "assigned"
					if o.state == "observed" {
						state = "observed"
					}
					row.ValueState, row.Value = p.value(name, o.value, state)
				}
			}
			if !admit(row, len(s.Parameters)) {
				s.Omissions.Parameters++
				continue
			}
			s.Parameters = append(s.Parameters, row)
		}
	}
	names := make([]string, 0, len(vu.Variables))
	for name := range vu.Variables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if entries >= MaxReplayRuntimeSnapshotEntries {
			s.Omissions.Variables++
			continue
		}
		row := ReplayRuntimeBinding{Name: p.name(name)}
		row.ValueState, row.Value = p.value(name, vu.Variables[name], "assigned")
		if !admit(row, len(s.Variables)) {
			s.Omissions.Variables++
			continue
		}
		s.Variables = append(s.Variables, row)
	}
	if p.publicationUnavailable || p.workUnavailable {
		return unavailable()
	}
	s.Omissions.Values = p.omittedValues
	if s.Omissions != (ReplayRuntimeOmissions{}) {
		s.State = "bounded"
	}
	if !ValidateReplayRuntimeSnapshotBounds(s) {
		return emptyReplayRuntimeSnapshot(boundary, "work_limit", false)
	}
	return CloneReplayRuntimeSnapshot(s)
}
