package sample

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// recordedConcurrencyMetadataKey marks a request group built from the
// recording's overlapping requests, with its group number.
const recordedConcurrencyMetadataKey = "recorded_concurrent_group"

var recordedConcurrencyActionPath = regexp.MustCompile(`^actions\[(\d+)\](.*)$`)

// recordedTiming is when a top-level action's request ran in the recording,
// and whether the recorder measured the pause after it.
type recordedTiming struct {
	start, end    time.Time
	recordedPause bool
}

// groupRecordedConcurrency replays the requests the browser had in flight at
// the same time as one concurrent request group (an http_sequence in
// concurrent mode), as LoadRunner replays a concurrent group, instead of one
// after another. A group holds consecutive request steps that were all in
// flight together: each starts before every earlier member finished. Because
// every member was sent before any other finished, none can use a value from
// another's response; a member whose request references an extractor of an
// earlier member still ends the group, as does a member the DSL cannot run in
// a group or one the group's size limits leave out. The last member's think
// time becomes the group's.
//
// A group that does not validate (a dependency the pre-check missed, the
// preparation limit) is retried without its last member, down to two, before
// its first request is left sequential. The group's think time is the
// recorded pause after the whole group, measured from its last request to
// finish, when it exceeds threshold.
//
// newIndex maps each exchange to its top-level action (nil: the same index).
// It returns the source mappings re-pointed at the grouped actions, the new
// top-level index of each exchange (-1 for a request now inside a group, so
// the replay baseline leaves it out), how many groups it built, and how many
// runs of in-flight requests it had to leave sequential.
func groupRecordedConcurrency(script *dsl.Script, exchanges []exchange, newIndex []int, mappings []sourceview.Mapping, threshold time.Duration) ([]sourceview.Mapping, []int, int, int) {
	if script == nil || len(script.Actions) < 2 {
		return mappings, newIndex, 0, 0
	}
	timings := make(map[int]recordedTiming, len(exchanges))
	for index, ex := range exchanges {
		if !ex.HasStartedAt {
			continue
		}
		position := index
		if newIndex != nil {
			if index >= len(newIndex) {
				continue
			}
			position = newIndex[index]
		}
		if position < 0 || position >= len(script.Actions) {
			continue
		}
		timings[position] = recordedTiming{start: ex.StartedAt, end: ex.StartedAt.Add(time.Duration(ex.DurationMs * float64(time.Millisecond))), recordedPause: ex.HasRecordedThinkTime}
	}

	actions := script.Actions
	out := make([]dsl.Action, 0, len(actions))
	// moved[old] is the new path prefix of top-level action old.
	moved := make([]string, len(actions))
	topLevel := make([]int, len(actions))
	groups, refused := 0, 0
	for index := 0; index < len(actions); {
		members := recordedConcurrentMembers(actions, timings, index)
		grouped := false
		for size := len(members); size >= 2 && !grouped; size-- {
			candidate := members[:size]
			thinkTime, thinkTimeAfter := recordedGroupThinkTime(actions, timings, candidate, threshold)
			wrapper, ok := recordedConcurrentGroup(actions, candidate, groups+1, script, thinkTime, thinkTimeAfter)
			if !ok {
				continue
			}
			grouped = true
			groups++
			position := len(out)
			out = append(out, wrapper)
			for child, old := range candidate {
				moved[old] = fmt.Sprintf("actions[%d].params.requests[%d]", position, child)
				topLevel[old] = -1
			}
			index = candidate[len(candidate)-1] + 1
		}
		if grouped {
			continue
		}
		if len(members) >= 2 {
			refused++
		}
		moved[index] = fmt.Sprintf("actions[%d]", len(out))
		topLevel[index] = len(out)
		out = append(out, actions[index])
		index++
	}
	if groups == 0 {
		return mappings, newIndex, 0, refused
	}
	script.Actions = out

	remapped := make([]sourceview.Mapping, 0, len(mappings))
	for _, mapping := range mappings {
		if match := recordedConcurrencyActionPath.FindStringSubmatch(mapping.TargetPath); match != nil {
			if old, err := strconv.Atoi(match[1]); err == nil && old < len(moved) {
				mapping.TargetPath = moved[old] + match[2]
			}
		}
		remapped = append(remapped, mapping)
	}
	positions := make([]int, len(exchanges))
	for index := range exchanges {
		old := index
		if newIndex != nil {
			old = -1
			if index < len(newIndex) {
				old = newIndex[index]
			}
		}
		positions[index] = -1
		if old >= 0 && old < len(topLevel) {
			positions[index] = topLevel[old]
		}
	}
	return remapped, positions, groups, refused
}

// recordedGroupThinkTime is the think time after a group: the last member's
// own when the recorder measured it, else the recorded pause from the group's
// last request to finish to the next step's start, when it exceeds threshold.
func recordedGroupThinkTime(actions []dsl.Action, timings map[int]recordedTiming, members []int, threshold time.Duration) (dsl.ThinkTimeConfig, bool) {
	last := members[len(members)-1]
	if timings[last].recordedPause {
		return actions[last].ThinkTime, actions[last].ThinkTimeAfter
	}
	next, timed := timings[last+1]
	if !timed {
		return actions[last].ThinkTime, actions[last].ThinkTimeAfter
	}
	var end time.Time
	for _, member := range members {
		if timings[member].end.After(end) {
			end = timings[member].end
		}
	}
	if pause := next.start.Sub(end); pause > threshold {
		return dsl.ThinkTimeConfig{Type: "fixed", MeanMs: float64(pause / time.Millisecond)}, true
	}
	return dsl.ThinkTimeConfig{}, false
}

// recordedConcurrentMembers is the run of top-level actions from start that
// were all in flight together and can share a group; a single action when
// none can join it.
func recordedConcurrentMembers(actions []dsl.Action, timings map[int]recordedTiming, start int) []int {
	first, timed := timings[start]
	if !timed || !recordedConcurrentEligible(actions[start]) {
		return []int{start}
	}
	members := []int{start}
	earliestEnd := first.end
	extractors := recordedExtractorNames(actions[start], nil)
	for next := start + 1; next < len(actions) && len(members) < dsl.MaxHTTPConcurrentChildren; next++ {
		if actions[members[len(members)-1]].ThinkTimeAfter {
			break
		}
		timing, timed := timings[next]
		if !timed || !timing.start.Before(earliestEnd) || !recordedConcurrentEligible(actions[next]) {
			break
		}
		if recordedReferencesAny(actions[next], extractors) || recordedExtractorCollides(actions[next], extractors) {
			break
		}
		members = append(members, next)
		if timing.end.Before(earliestEnd) {
			earliestEnd = timing.end
		}
		extractors = recordedExtractorNames(actions[next], extractors)
	}
	return members
}

// recordedConcurrentEligible reports a request step a concurrent group can
// run: a plain HTTP request, not an HTML-mode step; its think time moves to
// the group.
func recordedConcurrentEligible(action dsl.Action) bool {
	if action.Type != "request" || action.Params["html_step"] != nil {
		return false
	}
	child := action
	child.ThinkTime = dsl.ThinkTimeConfig{}
	child.ThinkTimeAfter = false
	return dsl.ValidateHTTPConcurrentChild(child) == nil
}

func recordedExtractorNames(action dsl.Action, names map[string]bool) map[string]bool {
	if names == nil {
		names = map[string]bool{}
	}
	for _, extractor := range action.Extractors {
		if extractor.Name != "" {
			names[extractor.Name] = true
		}
	}
	return names
}

func recordedExtractorCollides(action dsl.Action, names map[string]bool) bool {
	for _, extractor := range action.Extractors {
		if names[extractor.Name] {
			return true
		}
	}
	return false
}

// recordedReferencesAny reports whether the request references a variable
// one of names extracts.
func recordedReferencesAny(action dsl.Action, names map[string]bool) bool {
	if len(names) == 0 {
		return false
	}
	surfaces := []string{action.Target, action.Body}
	for _, value := range action.Headers {
		surfaces = append(surfaces, value)
	}
	for _, header := range action.HeaderList {
		surfaces = append(surfaces, header.Value)
	}
	for _, surface := range surfaces {
		if !strings.Contains(surface, "{{") {
			continue
		}
		for name := range names {
			if strings.Contains(surface, "{{var."+name) || strings.Contains(surface, "{{"+name+"}}") {
				return true
			}
		}
	}
	return false
}

// recordedConcurrentGroup wraps members in a concurrent request group with
// the given think time, or reports false when the group would not validate or
// exceeds the preparation work the runtime allows one group.
func recordedConcurrentGroup(actions []dsl.Action, members []int, number int, script *dsl.Script, thinkTime dsl.ThinkTimeConfig, thinkTimeAfter bool) (dsl.Action, bool) {
	children := make([]dsl.Action, len(members))
	for child, old := range members {
		children[child] = actions[old]
		children[child].ThinkTime = dsl.ThinkTimeConfig{}
		children[child].ThinkTimeAfter = false
	}
	if err := dsl.ValidateHTTPConcurrentChildren(children); err != nil {
		return dsl.Action{}, false
	}
	// The group only sends its requests together; it is not a business step
	// of its own, so it records no transaction (the step transactions and the
	// iteration transaction around it stay as they were).
	metadata := map[string]any{recordedConcurrencyMetadataKey: number, "source_request_count": len(children), dsl.HTTPSequenceTransparentMetadataKey: true}
	if transaction, shared := recordedSharedTransaction(children); shared {
		metadata[harSourceTransactionMeta] = transaction
	}
	wrapper := dsl.Action{
		Name:           fmt.Sprintf("Concurrent requests %d (%d requests)", number, len(children)),
		Type:           "http_sequence",
		Method:         "SEQUENCE",
		Target:         children[0].Target,
		Params:         map[string]any{"requests": children, "execution_mode": dsl.HTTPSequenceConcurrent},
		Metadata:       metadata,
		ThinkTime:      thinkTime,
		ThinkTimeAfter: thinkTimeAfter,
	}
	if _, _, err := dsl.HTTPConcurrentPreparationSourceSizeWithWorkLimit(dsl.MaxHTTPConcurrentPreparationWork, wrapper, script.Config.HTTP.DefaultHeaders, script.Config.HTTP.DefaultHeaderList); err != nil {
		return dsl.Action{}, false
	}
	return wrapper, true
}

// recordedSharedTransaction is the transaction every child is tagged with.
func recordedSharedTransaction(children []dsl.Action) (string, bool) {
	first, _ := children[0].Metadata[harSourceTransactionMeta].(string)
	if first == "" {
		return "", false
	}
	for _, child := range children[1:] {
		if name, _ := child.Metadata[harSourceTransactionMeta].(string); name != first {
			return "", false
		}
	}
	return first, true
}
