package openapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
)

const openAPIPathCredentialPointerPrefix = "openapi-path-v1:"

type openAPIPathRenderedSpan struct {
	parameter string
	start     int
	end       int
}

type openAPIPathParameterProjection struct {
	replacement string
	credential  bool
}

func buildOpenAPIPathCredentialTargets(
	path string,
	target string,
	params []parameter,
	requestKey string,
) ([]credentialpolicy.TargetRef, bool) {
	projections := make(map[string]openAPIPathParameterProjection)
	parameterOrder := make([]string, 0)
	for _, param := range params {
		name := strings.TrimSpace(param.Name)
		if name == "" || !strings.EqualFold(strings.TrimSpace(param.In), "path") {
			continue
		}
		if _, exists := projections[name]; exists {
			// templatePath replaces every occurrence with the first effective
			// parameter of a given name, so its locator must use the same
			// precedence.
			continue
		}
		replacement, governed := openAPIParameterTemplate(param)
		_, authored := authoredParameterWireValue(param)
		projections[name] = openAPIPathParameterProjection{
			replacement: replacement,
			credential:  governed && authored,
		}
		parameterOrder = append(parameterOrder, name)
	}

	sourceSegments := strings.Split(path, "/")
	renderedSegments := make([]string, len(sourceSegments))
	spansBySegment := make([][]openAPIPathRenderedSpan, len(sourceSegments))
	for index, segment := range sourceSegments {
		renderedSegments[index], spansBySegment[index] =
			renderOpenAPIPathSegment(segment, projections)
	}
	renderedPath := strings.Join(renderedSegments, "/")
	targetPath, _, _ := strings.Cut(target, "?")
	if renderedPath != targetPath {
		// A locator that does not describe the exact executable target is worse
		// than no locator: the runtime would believe it had protected a value
		// that it had not actually found. The shared finalizer will still retain
		// the action, but without a fabricated path locator.
		return unresolvedOpenAPIPathCredentialTargets(
			path,
			params,
			requestKey,
		), true
	}

	parameterRank := make(map[string]int, len(parameterOrder))
	for index, name := range parameterOrder {
		parameterRank[name] = index
	}
	var targets []credentialpolicy.TargetRef
	occurrence := 0
	for segmentIndex, spans := range spansBySegment {
		if len(spans) == 0 {
			continue
		}
		sort.SliceStable(spans, func(i, j int) bool {
			if spans[i].start != spans[j].start {
				return spans[i].start < spans[j].start
			}
			return parameterRank[spans[i].parameter] <
				parameterRank[spans[j].parameter]
		})
		allSpans := make([]openAPIPathRenderedSpan, len(spans))
		copy(allSpans, spans)
		shapeHash, ok := openAPIPathCredentialShapeHash(
			renderedSegments[segmentIndex],
			allSpans,
		)
		if !ok {
			return unresolvedOpenAPIPathCredentialTargets(
				path,
				params,
				requestKey,
			), true
		}
		for _, parameterName := range parameterOrder {
			targetSpans := make([]openAPIPathRenderedSpan, 0)
			for _, span := range spans {
				if span.parameter == parameterName {
					targetSpans = append(targetSpans, span)
				}
			}
			if len(targetSpans) == 0 {
				continue
			}
			pointer := encodeOpenAPIPathCredentialPointer(
				parameterName,
				len(renderedSegments[segmentIndex]),
				shapeHash,
				targetSpans,
				allSpans,
			)
			if len(pointer) > 4096 {
				return unresolvedOpenAPIPathCredentialTargets(
					path,
					params,
					requestKey,
				), true
			}
			targets = append(targets, credentialpolicy.TargetRef{
				RequestKey: requestKey,
				Field:      "url.path",
				Index:      segmentIndex,
				Pointer:    pointer,
				Occurrence: occurrence,
			})
			occurrence++
		}
	}
	return targets, false
}

func unresolvedOpenAPIPathCredentialTargets(
	path string,
	params []parameter,
	requestKey string,
) []credentialpolicy.TargetRef {
	segments := strings.Split(path, "/")
	seenParameters := make(map[string]struct{})
	targets := make([]credentialpolicy.TargetRef, 0)
	occurrence := 0
	for _, param := range params {
		name := strings.TrimSpace(param.Name)
		if name == "" || !strings.EqualFold(strings.TrimSpace(param.In), "path") ||
			!schemaCredentialField(name, parameterSchema(param)) {
			continue
		}
		if _, authored := authoredParameterWireValue(param); !authored {
			continue
		}
		if _, duplicate := seenParameters[name]; duplicate {
			continue
		}
		seenParameters[name] = struct{}{}
		placeholder := "{" + name + "}"
		for segmentIndex, segment := range segments {
			if !strings.Contains(segment, placeholder) {
				continue
			}
			targets = append(targets, credentialpolicy.TargetRef{
				RequestKey: requestKey,
				Field:      "url.path",
				Index:      segmentIndex,
				// This deliberately cannot match a source placeholder. The
				// action also receives a durable review gate, and the invalid
				// locator makes a manually removed gate fail closed before
				// transport.
				Pointer: "openapi-path-unresolved:" +
					base64.RawURLEncoding.EncodeToString([]byte(name)),
				Occurrence: occurrence,
			})
			occurrence++
		}
	}
	return targets
}

func renderOpenAPIPathSegment(
	source string,
	projections map[string]openAPIPathParameterProjection,
) (string, []openAPIPathRenderedSpan) {
	var rendered strings.Builder
	var spans []openAPIPathRenderedSpan
	for offset := 0; offset < len(source); {
		start := strings.IndexByte(source[offset:], '{')
		if start < 0 {
			rendered.WriteString(source[offset:])
			break
		}
		start += offset
		rendered.WriteString(source[offset:start])
		end := strings.IndexByte(source[start+1:], '}')
		if end < 0 {
			rendered.WriteString(source[start:])
			break
		}
		end += start + 1
		name := strings.TrimSpace(source[start+1 : end])
		projection, exists := projections[name]
		if !exists {
			rendered.WriteString(source[start : end+1])
			offset = end + 1
			continue
		}
		valueStart := rendered.Len()
		rendered.WriteString(projection.replacement)
		if projection.credential && projection.replacement != "" {
			spans = append(spans, openAPIPathRenderedSpan{
				parameter: name,
				start:     valueStart,
				end:       rendered.Len(),
			})
		}
		offset = end + 1
	}
	return rendered.String(), spans
}

func openAPIPathCredentialShapeHash(
	segment string,
	spans []openAPIPathRenderedSpan,
) (string, bool) {
	shape := []byte(segment)
	previousEnd := 0
	for _, span := range spans {
		if span.start < previousEnd || span.start < 0 || span.end <= span.start ||
			span.end > len(shape) {
			return "", false
		}
		for index := span.start; index < span.end; index++ {
			shape[index] = 0
		}
		previousEnd = span.end
	}
	digest := sha256.Sum256(shape)
	return hex.EncodeToString(digest[:]), true
}

func encodeOpenAPIPathCredentialPointer(
	parameter string,
	segmentLength int,
	shapeHash string,
	targetSpans []openAPIPathRenderedSpan,
	allSpans []openAPIPathRenderedSpan,
) string {
	return openAPIPathCredentialPointerPrefix +
		base64.RawURLEncoding.EncodeToString([]byte(parameter)) + ":" +
		strconv.Itoa(segmentLength) + ":" +
		shapeHash + ":" +
		encodeOpenAPIPathSpans(targetSpans) + ":" +
		encodeOpenAPIPathSpans(allSpans)
}

func encodeOpenAPIPathSpans(spans []openAPIPathRenderedSpan) string {
	encoded := make([]string, 0, len(spans))
	for _, span := range spans {
		encoded = append(
			encoded,
			fmt.Sprintf("%d-%d", span.start, span.end),
		)
	}
	return strings.Join(encoded, ",")
}
