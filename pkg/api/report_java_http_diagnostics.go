package api

import (
	"fmt"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func javaHTTPReportLines(name string, input *protocol.JavaHTTPSummary, population int64) []string {
	if input != nil && (population < 0 || population < input.Samples || population-input.Samples < input.InvalidSamples) {
		return []string{fmt.Sprintf("%s · Java-over-HTTP diagnostics unavailable: the actual-action population is absent or inconsistent.", name)}
	}
	s := input.RetainedForPopulation(population)
	if s == nil {
		return nil
	}
	lines := []string{fmt.Sprintf("%s · Java-over-HTTP opaque representations: %d observed / %d action samples; %d invalid. JVM object footprint is unavailable.", name, s.Samples, population, s.InvalidSamples)}
	lines = append(lines, fmt.Sprintf("Prepared request compression: none %d; gzip %d; deflate %d observed actions. Mixed compression is not a single comparison cohort.", s.RequestCompressionNone, s.RequestCompressionGzip, s.RequestCompressionDeflate))
	if s.Incomplete || s.SampleLimitReached {
		lines = append(lines, "Java-over-HTTP coverage is incomplete; observed subsets cannot establish whole-population comparisons.")
	}
	for _, item := range []struct {
		name string
		m    protocol.JavaHTTPByteMoments
	}{
		{"Runtime input representation", s.RequestEncoded}, {"Decoded request", s.RequestDecoded}, {"Prepared HTTP request body", s.PreparedRequestBody}, {"Retained response representation", s.ResponseRetained}, {"Base64 response representation", s.ResponseBase64},
	} {
		if mean, ok := item.m.MeanBytes(); ok {
			lines = append(lines, fmt.Sprintf("%s: %d observed samples; mean %.3f bytes; maximum %d bytes", item.name, item.m.Count, mean, item.m.MaxBytes))
		} else {
			lines = append(lines, item.name+": unavailable (missing observations or exact-sum bound exceeded)")
		}
	}
	for _, item := range []struct {
		name string
		p    protocol.JavaHTTPBytePair
	}{{"Prepared / decoded request bytes", s.PreparedPerDecoded}, {"Decoded / base64 request bytes", s.DecodedPerEncoded}, {"Base64 / retained response bytes", s.Base64PerRetained}} {
		if ratio, ok := item.p.Ratio(); ok {
			lines = append(lines, fmt.Sprintf("%s: %.6f across %d paired observations", item.name, ratio, item.p.Samples))
		} else {
			lines = append(lines, item.name+": unavailable (missing pair, zero denominator or exact-sum bound exceeded)")
		}
	}
	cmp := s.SerializedRepresentationVsHTTPBody()
	if cmp.Available && cmp.BodyPerRepresentationRatio != nil {
		lines = append(lines, fmt.Sprintf("serialized_representation_vs_http_body: %.6f ratio (%d / %d bytes; compression %s) across %d paired observations", *cmp.BodyPerRepresentationRatio, cmp.BodyPerRepresentation.NumeratorBytes, cmp.BodyPerRepresentation.DenominatorBytes, cmp.Compression, cmp.Samples))
	} else {
		lines = append(lines, fmt.Sprintf("serialized_representation_vs_http_body: unavailable (%s)", cmp.UnavailableReason))
	}
	if s.ContainedObjectFootprint != nil {
		fp := s.ContainedObjectFootprint
		if mean, ok := fp.ShallowObjectBytes.MeanBytes(); ok {
			lines = append(lines, fmt.Sprintf("java_shallow_object_bytes (%s, JVM %s): %d observed samples; mean %.3f bytes; maximum %d bytes (%s)", fp.ShallowMethod, fp.JVMVersion, fp.ShallowObjectBytes.Count, mean, fp.ShallowObjectBytes.MaxBytes, fp.RetainedHeapNote))
		} else {
			lines = append(lines, fmt.Sprintf("java_shallow_object_bytes (%s, JVM %s): unavailable (%s; %s)", fp.ShallowMethod, fp.JVMVersion, fp.UnavailableReason, fp.RetainedHeapNote))
		}
		if mean, ok := fp.ReachableGraphBytes.MeanBytes(); ok {
			objMean, _ := fp.UniqueObjects.MeanBytes()
			lines = append(lines, fmt.Sprintf("java_reachable_graph_bytes (%s, JVM %s): %d observed samples; mean %.3f bytes; maximum %d bytes; mean %.1f unique objects (max_nodes=%d, max_depth=%d, traversal_exhausted=%d; %s)", fp.ReachableGraphMethod, fp.JVMVersion, fp.ReachableGraphBytes.Count, mean, fp.ReachableGraphBytes.MaxBytes, objMean, fp.MaxNodes, fp.MaxDepth, fp.TraversalExhaustedSamples, fp.RetainedHeapNote))
		} else {
			lines = append(lines, fmt.Sprintf("java_reachable_graph_bytes (%s, JVM %s): unavailable (%s; %s)", fp.ReachableGraphMethod, fp.JVMVersion, fp.UnavailableReason, fp.RetainedHeapNote))
		}
		if fp.CodecSamples > 0 {
			lines = append(lines, fmt.Sprintf("java_serialization_ms: %.6f ms mean; java_deserialization_ms: %.6f ms mean across %d codec samples", fp.JavaSerializationMs.Sum/float64(fp.JavaSerializationMs.Count), fp.JavaDeserializationMs.Sum/float64(fp.JavaDeserializationMs.Count), fp.CodecSamples))
		}
	}
	if share, ok := s.Base64Work.Share(); ok {
		lines = append(lines, fmt.Sprintf("Base64 conversion work / enclosing wrapper time: %.3f%% across %d paired observations", share*100, s.Base64Work.Samples))
	} else {
		lines = append(lines, "Base64 conversion work / enclosing wrapper time: unavailable")
	}
	for _, item := range []struct {
		name string
		d    protocol.DiagnosticDistribution
	}{{"Base64 decoding", s.Base64Decode}, {"Base64 encoding", s.Base64Encode}, {"Enclosing action", s.Enclosing}} {
		if item.d.Count > 0 {
			lines = append(lines, fmt.Sprintf("%s: %.6f ms mean; %d observed samples", item.name, item.d.Sum/float64(item.d.Count), item.d.Count))
		} else {
			lines = append(lines, item.name+": unavailable")
		}
	}
	lines = append(lines, fmt.Sprintf("Response availability: %d unavailable; %d limited to a collection window; %d explicitly discarded.", s.ResponseUnavailable, s.ResponseWindow, s.ResponseDiscarded))
	lines = append(lines, "Prepared body counts exclude framing, retries and delivery claims. Response counts describe the retained representation; they do not measure a Java object graph.")
	return lines
}
