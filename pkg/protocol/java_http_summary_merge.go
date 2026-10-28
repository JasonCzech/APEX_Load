package protocol

import "math"

func javaHTTPEmptySummary() *JavaHTTPSummary {
	return &JavaHTTPSummary{Version: 1, Mode: "embedded_opaque_bytes", ObjectFootprint: "unavailable_embedded_opaque_bytes"}
}

func (s *JavaHTTPSummary) Valid() bool {
	if s == nil || s.Version != 1 || s.Mode != "embedded_opaque_bytes" || s.ObjectFootprint != "unavailable_embedded_opaque_bytes" || s.Samples < 0 || s.InvalidSamples < 0 || s.Samples > javaHTTPMaxSamples || s.InvalidSamples > javaHTTPMaxSamples-s.Samples {
		return false
	}
	if s.Population < 0 || s.Population > javaHTTPMaxSamples || s.Population > 0 && s.Population < s.Samples+s.InvalidSamples {
		return false
	}
	for _, n := range []int64{s.RequestCompressionNone, s.RequestCompressionGzip, s.RequestCompressionDeflate} {
		if n < 0 || n > s.Samples {
			return false
		}
	}
	if s.RequestCompressionNone+s.RequestCompressionGzip+s.RequestCompressionDeflate != s.PreparedRequestBody.Count {
		return false
	}
	for _, m := range []JavaHTTPByteMoments{s.RequestEncoded, s.RequestDecoded, s.PreparedRequestBody, s.ResponseRetained, s.ResponseBase64} {
		if m.Count < 0 || m.Count > s.Samples || m.SumBytes < 0 || m.SumBytes > javaHTTPMaxExactBytes || m.MinBytes < 0 || m.MinBytes > m.MaxBytes || m.MaxBytes > 1<<40 {
			return false
		}
		if m.Count == 0 {
			if m.SumBytes != 0 || m.MinBytes != 0 || m.MaxBytes != 0 || m.Overflow {
				return false
			}
		} else if !m.Overflow && (m.MinBytes > m.SumBytes/m.Count || m.MaxBytes < m.SumBytes/m.Count || m.MaxBytes == m.SumBytes/m.Count && m.SumBytes%m.Count != 0) {
			return false
		}
	}
	if s.RequestEncoded.Count != s.Samples || s.Enclosing.Count != s.Samples || s.PreparedRequestBody.Count > s.RequestDecoded.Count || s.ResponseRetained.Count != s.Samples-s.ResponseUnavailable || s.ResponseBase64.Count > s.ResponseRetained.Count {
		return false
	}
	if s.DecodedPerEncoded.Samples != s.RequestDecoded.Count || s.PreparedPerDecoded.Samples != s.PreparedRequestBody.Count || s.Base64PerRetained.Samples != s.ResponseBase64.Count {
		return false
	}
	for _, pair := range []struct {
		p    JavaHTTPBytePair
		n, d JavaHTTPByteMoments
	}{
		{s.DecodedPerEncoded, s.RequestDecoded, s.RequestEncoded}, {s.PreparedPerDecoded, s.PreparedRequestBody, s.RequestDecoded}, {s.Base64PerRetained, s.ResponseBase64, s.ResponseRetained},
	} {
		p := pair.p
		if p.Samples < 0 || p.Samples > min(pair.n.Count, pair.d.Count) || p.NumeratorBytes < 0 || p.DenominatorBytes < 0 || p.NumeratorBytes > javaHTTPMaxExactBytes || p.DenominatorBytes > javaHTTPMaxExactBytes {
			return false
		}
		if p.Samples == 0 && (p.NumeratorBytes != 0 || p.DenominatorBytes != 0 || p.Overflow) {
			return false
		}
		if !p.Overflow && ((!pair.n.Overflow && p.NumeratorBytes > pair.n.SumBytes) || (!pair.d.Overflow && p.DenominatorBytes > pair.d.SumBytes)) {
			return false
		}
	}
	for _, d := range []DiagnosticDistribution{s.Base64Decode, s.Base64Encode, s.Enclosing} {
		if !javaHTTPDurationSummaryValid(d, s.Samples) {
			return false
		}
	}
	w := s.Base64Work
	tolerance := javaHTTPSumTolerance(s.Samples)
	if w.Samples < 0 || w.Samples > min(s.Base64Decode.Count, s.Base64Encode.Count) || !javaHTTPFinite(w.Base64Ms) || !javaHTTPFinite(w.EnclosingMs) || w.Base64Ms < 0 || w.EnclosingMs < 0 || w.Base64Ms > w.EnclosingMs*(1+tolerance) || w.EnclosingMs > s.Enclosing.Sum*(1+tolerance) || w.Base64Ms > (s.Base64Decode.Sum+s.Base64Encode.Sum)*(1+tolerance) {
		return false
	}
	if w.Samples == 0 && (w.Base64Ms != 0 || w.EnclosingMs != 0) || w.Samples > 0 && w.EnclosingMs <= 0 {
		return false
	}
	for _, n := range []int64{s.ResponseUnavailable, s.ResponseWindow, s.ResponseDiscarded} {
		if n < 0 || n > s.Samples {
			return false
		}
	}
	if s.ResponseUnavailable+s.ResponseWindow+s.ResponseDiscarded > s.Samples {
		return false
	}
	if !javaHTTPObjectFootprintSummaryValid(s.ContainedObjectFootprint, s.Samples) {
		return false
	}
	return true
}

func javaHTTPObjectFootprintSummaryValid(f *JavaHTTPObjectFootprintSummary, samples int64) bool {
	if f == nil {
		return true
	}
	if f.Samples <= 0 || f.Samples > samples || f.ShallowMethod != JavaHTTPShallowObjectMethodV1 || f.ReachableGraphMethod != JavaHTTPReachableGraphMethodV1 || f.RetainedHeapNote != JavaHTTPObjectFootprintRetainedNote {
		return false
	}
	if f.ShallowAvailableSamples < 0 || f.UnavailableSamples < 0 || f.ShallowAvailableSamples+f.UnavailableSamples != f.Samples {
		return false
	}
	if f.TraversalExhaustedSamples < 0 || f.TraversalExhaustedSamples > f.Samples || f.CodecSamples < 0 || f.CodecSamples > f.Samples {
		return false
	}
	if f.MaxNodes < 1 || f.MaxNodes > 65536 || f.MaxDepth < 1 || f.MaxDepth > 256 {
		return false
	}
	for _, m := range []JavaHTTPByteMoments{f.ShallowObjectBytes, f.ShallowFallbackBytes, f.ReachableGraphBytes, f.ReachableGraphFallback, f.UniqueObjects, f.SerializedBytes} {
		if m.Count < 0 || m.Count > f.Samples || m.SumBytes < 0 || m.SumBytes > javaHTTPMaxExactBytes || m.MinBytes < 0 || m.MinBytes > m.MaxBytes || m.MaxBytes > 1<<40 {
			return false
		}
		if m.Count == 0 && (m.SumBytes != 0 || m.MinBytes != 0 || m.MaxBytes != 0 || m.Overflow) {
			return false
		}
	}
	if f.ShallowObjectBytes.Count != f.ShallowAvailableSamples || f.ReachableGraphBytes.Count != f.ShallowAvailableSamples || f.UniqueObjects.Count != f.Samples || f.SerializedBytes.Count != f.CodecSamples {
		return false
	}
	if !javaHTTPDurationSummaryValid(f.JavaSerializationMs, f.CodecSamples) || !javaHTTPDurationSummaryValid(f.JavaDeserializationMs, f.CodecSamples) {
		return false
	}
	return f.JavaSerializationMs.Count == f.CodecSamples && f.JavaDeserializationMs.Count == f.CodecSamples
}

func javaHTTPFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// javaHTTPSumTolerance bounds the relative rounding of a float sum built from
// count additions, so near-constant samples accumulated in a different order
// than their bounds are not mistaken for inconsistent evidence.
func javaHTTPSumTolerance(count int64) float64 {
	return max(1e-12, float64(count)*0x1p-52)
}

func javaHTTPDurationSummaryValid(d DiagnosticDistribution, samples int64) bool {
	if d.Count < 0 || d.Count > samples || !javaHTTPFinite(d.Sum) || !javaHTTPFinite(d.Min) || !javaHTTPFinite(d.Max) || d.Min < 0 || d.Min > d.Max || d.Max > 24*60*60*1000 || d.Sum < 0 {
		return false
	}
	var total int64
	for _, n := range d.Buckets {
		if n < 0 || n > d.Count-total {
			return false
		}
		total += n
	}
	if total != d.Count {
		return false
	}
	if d.Count == 0 {
		return d.Sum == 0 && d.Min == 0 && d.Max == 0
	}
	tolerance := javaHTTPSumTolerance(d.Count)
	return d.Sum >= d.Min*float64(d.Count)*(1-tolerance) && d.Sum <= d.Max*float64(d.Count)*(1+tolerance)
}

func (s *JavaHTTPSummary) Retained() *JavaHTTPSummary {
	if s == nil {
		return nil
	}
	if !s.Valid() {
		out := javaHTTPEmptySummary()
		out.Incomplete = true
		return out
	}
	return s.Clone()
}

// RetainedForPopulation binds observed diagnostics to the authoritative action
// count. Missing peers and invalid observations never become measured zeros.
func (s *JavaHTTPSummary) RetainedForPopulation(count int64) *JavaHTTPSummary {
	if s == nil {
		return nil
	}
	s = s.Retained()
	// A summary that counted its own java_http population supersedes the
	// caller's action count, which can include another protocol's actions.
	if s.Population > 0 {
		count = s.Population
	}
	if count < 0 || s.Samples+s.InvalidSamples > count {
		s = javaHTTPEmptySummary()
		s.Incomplete = true
		return s
	}
	if s.Samples < count {
		s.Incomplete = true
	}
	return s
}

// MergeJavaHTTPSummary adds bounded sufficient statistics. Nil peers stay
// absent; the caller compares observed samples to its authoritative action
// population and marks any mixed-peer coverage incomplete.
func MergeJavaHTTPSummary(dst **JavaHTTPSummary, src *JavaHTTPSummary) {
	if dst == nil || src == nil {
		return
	}
	if *dst == nil {
		*dst = javaHTTPEmptySummary()
	}
	d := *dst
	// A refused source's actions are uncounted, so the merged population is
	// unknown and callers fall back to their own action count.
	if !src.Valid() {
		d.Incomplete = true
		d.Population = 0
		return
	}
	if src.Samples+src.InvalidSamples > javaHTTPMaxSamples-d.Samples-d.InvalidSamples {
		d.SampleLimitReached = true
		d.Incomplete = true
		d.Population = 0
		return
	}
	// A population merges only when both sides counted theirs; otherwise the
	// caller's action count remains the denominator.
	population := int64(0)
	if d.populationKnown() && src.populationKnown() && src.Population <= javaHTTPMaxSamples-d.Population {
		population = d.Population + src.Population
	}
	d.Population = population
	d.Samples += src.Samples
	d.InvalidSamples += src.InvalidSamples
	d.Incomplete = d.Incomplete || src.Incomplete
	d.SampleLimitReached = d.SampleLimitReached || src.SampleLimitReached
	for _, x := range []struct {
		d *JavaHTTPByteMoments
		s JavaHTTPByteMoments
	}{
		{&d.RequestEncoded, src.RequestEncoded}, {&d.RequestDecoded, src.RequestDecoded}, {&d.PreparedRequestBody, src.PreparedRequestBody}, {&d.ResponseRetained, src.ResponseRetained}, {&d.ResponseBase64, src.ResponseBase64},
	} {
		if x.s.Count == 0 {
			continue
		}
		if x.d.Count == 0 || x.s.MinBytes < x.d.MinBytes {
			x.d.MinBytes = x.s.MinBytes
		}
		x.d.Count += x.s.Count
		x.d.MaxBytes = max(x.d.MaxBytes, x.s.MaxBytes)
		if x.d.Overflow || x.s.Overflow || x.s.SumBytes > javaHTTPMaxExactBytes-x.d.SumBytes {
			x.d.Overflow = true
		} else {
			x.d.SumBytes += x.s.SumBytes
		}
	}
	for _, x := range []struct {
		d *JavaHTTPBytePair
		s JavaHTTPBytePair
	}{{&d.DecodedPerEncoded, src.DecodedPerEncoded}, {&d.PreparedPerDecoded, src.PreparedPerDecoded}, {&d.Base64PerRetained, src.Base64PerRetained}} {
		x.d.Samples += x.s.Samples
		if x.d.Overflow || x.s.Overflow || x.s.NumeratorBytes > javaHTTPMaxExactBytes-x.d.NumeratorBytes || x.s.DenominatorBytes > javaHTTPMaxExactBytes-x.d.DenominatorBytes {
			x.d.Overflow = true
		} else {
			x.d.NumeratorBytes += x.s.NumeratorBytes
			x.d.DenominatorBytes += x.s.DenominatorBytes
		}
	}
	for _, x := range []struct {
		d *DiagnosticDistribution
		s DiagnosticDistribution
	}{{&d.Base64Decode, src.Base64Decode}, {&d.Base64Encode, src.Base64Encode}, {&d.Enclosing, src.Enclosing}} {
		if x.s.Count == 0 {
			continue
		}
		if x.d.Count == 0 || x.s.Min < x.d.Min {
			x.d.Min = x.s.Min
		}
		x.d.Count += x.s.Count
		x.d.Sum += x.s.Sum
		x.d.Max = max(x.d.Max, x.s.Max)
		for i, n := range x.s.Buckets {
			x.d.Buckets[i] += n
		}
	}
	d.Base64Work.Samples += src.Base64Work.Samples
	d.Base64Work.Base64Ms += src.Base64Work.Base64Ms
	d.Base64Work.EnclosingMs += src.Base64Work.EnclosingMs
	d.RequestCompressionNone += src.RequestCompressionNone
	d.RequestCompressionGzip += src.RequestCompressionGzip
	d.RequestCompressionDeflate += src.RequestCompressionDeflate
	d.ResponseUnavailable += src.ResponseUnavailable
	d.ResponseWindow += src.ResponseWindow
	d.ResponseDiscarded += src.ResponseDiscarded
	if src.ContainedObjectFootprint != nil {
		mergeJavaHTTPObjectFootprintSummary(&d.ContainedObjectFootprint, src.ContainedObjectFootprint)
	}
}

func mergeJavaHTTPObjectFootprintSummary(dst **JavaHTTPObjectFootprintSummary, src *JavaHTTPObjectFootprintSummary) {
	if dst == nil || src == nil {
		return
	}
	if *dst == nil {
		cp := *src
		*dst = &cp
		return
	}
	d := *dst
	d.Samples += src.Samples
	if d.JVMVersion == "" {
		d.JVMVersion = src.JVMVersion
	} else if src.JVMVersion != "" && d.JVMVersion != src.JVMVersion {
		d.JVMVersion = "mixed"
	}
	d.ShallowAvailableSamples += src.ShallowAvailableSamples
	d.UnavailableSamples += src.UnavailableSamples
	if d.UnavailableReason == "" {
		d.UnavailableReason = src.UnavailableReason
	} else if src.UnavailableReason != "" && d.UnavailableReason != src.UnavailableReason {
		d.UnavailableReason = "mixed"
	}
	d.MaxNodes = max(d.MaxNodes, src.MaxNodes)
	d.MaxDepth = max(d.MaxDepth, src.MaxDepth)
	d.TraversalExhaustedSamples += src.TraversalExhaustedSamples
	d.CodecSamples += src.CodecSamples
	for _, x := range []struct {
		d *JavaHTTPByteMoments
		s JavaHTTPByteMoments
	}{
		{&d.ShallowObjectBytes, src.ShallowObjectBytes},
		{&d.ShallowFallbackBytes, src.ShallowFallbackBytes},
		{&d.ReachableGraphBytes, src.ReachableGraphBytes},
		{&d.ReachableGraphFallback, src.ReachableGraphFallback},
		{&d.UniqueObjects, src.UniqueObjects},
		{&d.SerializedBytes, src.SerializedBytes},
	} {
		if x.s.Count == 0 {
			continue
		}
		if x.d.Count == 0 || x.s.MinBytes < x.d.MinBytes {
			x.d.MinBytes = x.s.MinBytes
		}
		x.d.Count += x.s.Count
		x.d.MaxBytes = max(x.d.MaxBytes, x.s.MaxBytes)
		if x.d.Overflow || x.s.Overflow || x.s.SumBytes > javaHTTPMaxExactBytes-x.d.SumBytes {
			x.d.Overflow = true
		} else {
			x.d.SumBytes += x.s.SumBytes
		}
	}
	for _, x := range []struct {
		d *DiagnosticDistribution
		s DiagnosticDistribution
	}{
		{&d.JavaSerializationMs, src.JavaSerializationMs},
		{&d.JavaDeserializationMs, src.JavaDeserializationMs},
	} {
		if x.s.Count == 0 {
			continue
		}
		if x.d.Count == 0 || x.s.Min < x.d.Min {
			x.d.Min = x.s.Min
		}
		x.d.Count += x.s.Count
		x.d.Sum += x.s.Sum
		x.d.Max = max(x.d.Max, x.s.Max)
		for i, n := range x.s.Buckets {
			x.d.Buckets[i] += n
		}
	}
}
