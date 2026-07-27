package requestimport

import (
	"reflect"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

type runtimeHeaderSurfaceSnapshot struct {
	list    dsl.HeaderList
	headers map[string]string
}

type pendingRuntimeRequirements struct {
	actionPath  string
	requestKey  string
	requirement []credentialpolicy.Requirement
}

func captureRuntimeHeaderSurfaces(script *dsl.Script) (map[string]runtimeHeaderSurfaceSnapshot, error) {
	snapshots := make(map[string]runtimeHeaderSurfaceSnapshot)
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if !runtimeSurfaceIsHTTPRequest(ref) {
			return nil
		}
		list, headers := runtimeSurfaceHeaderSnapshot(ref)
		snapshots[ref.Path] = runtimeHeaderSurfaceSnapshot{
			list:    append(dsl.HeaderList(nil), list...),
			headers: cloneStringMap(headers),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

func runtimeSurfaceHeaderSnapshot(ref dslwalk.SurfaceRef) (dsl.HeaderList, map[string]string) {
	if ref.Action != nil {
		return ref.Action.HeaderList, ref.Action.Headers
	}
	if ref.Request == nil {
		return nil, nil
	}
	rawList, hasList := ref.Request["header_list"]
	parsedList := parseMapRequestHeaderList(rawList, hasList, SourceRef{}, 0)
	rawHeaders, hasHeaders := ref.Request["headers"]
	parsedMap := parseMapRequestHeaderMap(
		rawHeaders,
		hasHeaders,
		SourceRef{},
		parsedList.sourceCount,
	)
	if !hasList && !parsedMap.recognized && hasHeaders {
		legacy := parseMapRequestHeaderList(rawHeaders, true, SourceRef{}, 0)
		if legacy.recognized {
			return legacy.entries, nil
		}
	}
	return parsedList.entries, parsedMap.headers
}

func reconcileRuntimeHeaderRequirements(
	pending pendingRuntimeRequirements,
	before map[string]runtimeHeaderSurfaceSnapshot,
	after map[string]runtimeHeaderSurfaceSnapshot,
) []credentialpolicy.Requirement {
	if len(pending.requirement) == 0 {
		return nil
	}
	path := strings.TrimSpace(pending.actionPath)
	if path == "" {
		path = inferRuntimeRequirementActionPath(pending.requirement, before)
	}
	beforeSurface, beforeFound := before[path]
	afterSurface, afterFound := after[path]
	if !beforeFound || !afterFound {
		// No exact surface identity means there is no safe local index-remap
		// contract. Leave non-header targets intact but drop header targets rather
		// than letting API binding search globally and land in metadata/siblings.
		return reconcileRequirementsAgainstSurface(
			pending.requirement,
			pending.requestKey,
			runtimeHeaderSurfaceSnapshot{},
			runtimeHeaderSurfaceSnapshot{},
			false,
		)
	}
	return reconcileRequirementsAgainstSurface(
		pending.requirement,
		pending.requestKey,
		beforeSurface,
		afterSurface,
		true,
	)
}

func inferRuntimeRequirementActionPath(
	requirements []credentialpolicy.Requirement,
	snapshots map[string]runtimeHeaderSurfaceSnapshot,
) string {
	candidates := make(map[string]struct{})
	for _, requirement := range requirements {
		for _, target := range requirement.Targets {
			if !runtimeHeaderRequirementTarget(target) {
				continue
			}
			for path, snapshot := range snapshots {
				if runtimeHeaderTargetMatches(snapshot, target, requirement.Replacement) {
					candidates[path] = struct{}{}
				}
			}
		}
	}
	if len(candidates) != 1 {
		return ""
	}
	for path := range candidates {
		return path
	}
	return ""
}

func reconcileRequirementsAgainstSurface(
	input []credentialpolicy.Requirement,
	requestKey string,
	before runtimeHeaderSurfaceSnapshot,
	after runtimeHeaderSurfaceSnapshot,
	hasSurface bool,
) []credentialpolicy.Requirement {
	indexRemap := runtimeHeaderListIndexRemap(before.list, after.list)
	output := make([]credentialpolicy.Requirement, 0, len(input))
	for _, requirement := range credentialpolicy.CloneRequirements(input) {
		targets := make([]credentialpolicy.TargetRef, 0, len(requirement.Targets))
		for _, target := range requirement.Targets {
			if target.RequestKey != "" && requestKey != "" && target.RequestKey != requestKey {
				targets = append(targets, target)
				continue
			}
			if !runtimeHeaderRequirementTarget(target) {
				targets = append(targets, target)
				continue
			}
			if !hasSurface ||
				!runtimeHeaderTargetMatches(before, target, requirement.Replacement) {
				continue
			}
			field := strings.ToLower(strings.TrimSpace(target.Field))
			switch field {
			case "header", "header_list":
				nextIndex, retained := indexRemap[target.Index]
				if !retained || nextIndex < 0 || nextIndex >= len(after.list) ||
					after.list[nextIndex].Value != requirement.Replacement {
					continue
				}
				target.Index = nextIndex
				target.Occurrence = nextIndex
			default:
				if !runtimeHeaderTargetMatches(after, target, requirement.Replacement) {
					continue
				}
			}
			targets = append(targets, target)
		}
		if len(targets) == 0 {
			continue
		}
		requirement.Targets = targets
		output = append(output, requirement)
	}
	if len(output) == 0 {
		return nil
	}
	return output
}

func runtimeHeaderRequirementTarget(target credentialpolicy.TargetRef) bool {
	field := strings.ToLower(strings.TrimSpace(target.Field))
	return field == "header" || field == "header_list" || field == "headers" ||
		strings.HasPrefix(field, "headers.")
}

func runtimeHeaderTargetMatches(
	snapshot runtimeHeaderSurfaceSnapshot,
	target credentialpolicy.TargetRef,
	replacement string,
) bool {
	field := strings.ToLower(strings.TrimSpace(target.Field))
	switch field {
	case "header", "header_list":
		return target.Index >= 0 && target.Index < len(snapshot.list) &&
			snapshot.list[target.Index].Value == replacement
	case "headers":
		return runtimeHeaderMapHasValue(snapshot.headers, headerTargetPointerIdentity(target.Pointer), replacement)
	default:
		if strings.HasPrefix(field, "headers.") {
			return runtimeHeaderMapHasValue(
				snapshot.headers,
				headerIdentity(strings.TrimPrefix(field, "headers.")),
				replacement,
			)
		}
		return false
	}
}

func runtimeHeaderMapHasValue(headers map[string]string, identity, replacement string) bool {
	for _, name := range sortedStringKeys(headers) {
		if headerIdentity(name) == identity && headers[name] == replacement {
			return true
		}
	}
	return false
}

func runtimeHeaderListIndexRemap(before, after dsl.HeaderList) map[int]int {
	if reflect.DeepEqual(before, after) {
		output := make(map[int]int, len(before))
		for index := range before {
			output[index] = index
		}
		return output
	}
	output := make(map[int]int, len(after))
	next := 0
	for index, header := range before {
		if next >= len(after) {
			break
		}
		if header == after[next] {
			output[index] = next
			next++
		}
	}
	return output
}
