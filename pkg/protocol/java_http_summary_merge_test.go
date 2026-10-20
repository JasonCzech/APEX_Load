package protocol

import (
	"math"
	"reflect"
	"testing"
)

func TestJavaHTTPSummaryMergeMatchesDirectObservation(t *testing.T) {
	var direct, merged *JavaHTTPSummary
	for i := 0; i < 7; i++ {
		d := javaDiagnosticFixture()
		if i == 1 {
			d.ResponseState = "unavailable"
			d.ResponseRetainedBytes = nil
			d.ResponseBase64Bytes = nil
			d.Base64EncodeMs = nil
		}
		if i == 2 {
			d = &JavaHTTPDiagnostics{}
		}
		if i == 3 {
			d = nil
		}
		var chunk *JavaHTTPSummary
		MergeJavaHTTPDiagnostic(&direct, d)
		MergeJavaHTTPDiagnostic(&chunk, d)
		MergeJavaHTTPSummary(&merged, chunk)
	}
	if !direct.Valid() || !merged.Valid() || !reflect.DeepEqual(direct, merged) {
		t.Fatal("merge changed populations", direct, merged)
	}
	copy := merged.Clone()
	copy.Base64Work.EnclosingMs = 123
	if merged.Base64Work.EnclosingMs == 123 {
		t.Fatal("summary clone aliases moments")
	}
}

// Repeated near-constant timings accumulate float rounding beyond a fixed
// 1e-12 relative bound (0.1 ms x 100,000 sums to 10000.000000018848).
func TestJavaHTTPSummaryAcceptsAccumulatedNearConstantTimings(t *testing.T) {
	var s *JavaHTTPSummary
	for range 100_000 {
		d := javaDiagnosticFixture()
		decode, encode, enclosing := 0.1, 0.3, 1.7
		d.Base64DecodeMs, d.Base64EncodeMs, d.EnclosingMs = &decode, &encode, enclosing
		MergeJavaHTTPDiagnostic(&s, d)
	}
	if s.Base64Decode.Sum <= 0.1*100_000*(1+1e-12) && s.Base64Encode.Sum >= 0.3*100_000*(1-1e-12) {
		t.Fatal("fixture no longer exercises accumulated rounding", s.Base64Decode.Sum, s.Base64Encode.Sum)
	}
	if !s.Valid() || s.Retained().Samples != 100_000 || s.RetainedForPopulation(100_000).Incomplete {
		t.Fatal("consistent accumulated evidence was refused", s.Base64Decode.Sum, s.Base64Encode.Sum, s.Enclosing.Sum)
	}
	forged := s.Clone()
	forged.Base64Decode.Sum = 0.1 * 100_000 * 1.001
	if forged.Valid() {
		t.Fatal("sum outside the observed bounds accepted")
	}
}

func TestJavaHTTPSummaryRefusesForgedOrUnboundedPeerStatistics(t *testing.T) {
	for _, change := range []func(*JavaHTTPSummary){
		func(s *JavaHTTPSummary) { s.Version = 2 }, func(s *JavaHTTPSummary) { s.Mode = "payload-secret" },
		func(s *JavaHTTPSummary) { s.RequestDecoded.SumBytes = -1 }, func(s *JavaHTTPSummary) { s.RequestDecoded.SumBytes = javaHTTPMaxExactBytes + 1 },
		func(s *JavaHTTPSummary) { s.RequestDecoded.Count = 2 }, func(s *JavaHTTPSummary) { s.PreparedPerDecoded.Samples = 2 },
		func(s *JavaHTTPSummary) { s.Base64Decode.Buckets[0]++ }, func(s *JavaHTTPSummary) { s.Base64Decode.Sum = math.NaN() },
		func(s *JavaHTTPSummary) { s.Base64Decode.Sum = math.Inf(1) }, func(s *JavaHTTPSummary) { s.Base64Work.Base64Ms = 9 },
		func(s *JavaHTTPSummary) { s.Base64Work.EnclosingMs = 11 }, func(s *JavaHTTPSummary) { s.ResponseUnavailable = 1 },
		func(s *JavaHTTPSummary) { s.Samples = javaHTTPMaxSamples + 1 },
	} {
		var src *JavaHTTPSummary
		MergeJavaHTTPDiagnostic(&src, javaDiagnosticFixture())
		change(src)
		if src.Valid() {
			t.Fatal("forged summary admitted", src)
		}
		var dst *JavaHTTPSummary
		MergeJavaHTTPSummary(&dst, src)
		if !dst.Valid() || !dst.Incomplete || dst.Samples != 0 || dst.InvalidSamples != 0 {
			t.Fatal("unknown batch became one measured/invalid action", dst)
		}
	}
}

func TestJavaHTTPSummaryMergedByteOverflowStaysExplicit(t *testing.T) {
	d := javaDiagnosticFixture()
	d.RequestEncodedBytes = 1 << 40
	*d.RequestDecodedBytes = 3 << 38
	*d.PreparedRequestBodyBytes = *d.RequestDecodedBytes
	var s *JavaHTTPSummary
	MergeJavaHTTPDiagnostic(&s, d)
	for range 14 {
		next := s.Clone()
		MergeJavaHTTPSummary(&s, next)
	}
	if !s.Valid() || !s.RequestEncoded.Overflow || !s.DecodedPerEncoded.Overflow || s.Samples != 1<<14 {
		t.Fatal("overflow lost population or became invalid", s)
	}
	if _, ok := s.DecodedPerEncoded.Ratio(); ok {
		t.Fatal("partial sums became qualified ratio")
	}
}

// The summary counts its own java_http actions, with or without diagnostics,
// so a caller's count of same-named actions of other protocols never becomes
// the Java denominator.
func TestJavaHTTPSummaryCountsItsOwnActionPopulation(t *testing.T) {
	var s *JavaHTTPSummary
	ObserveJavaHTTPAction(&s, javaDiagnosticFixture())
	ObserveJavaHTTPAction(&s, nil)
	if !s.Valid() || s.Population != 2 || s.Samples != 1 {
		t.Fatal("actual java_http actions miscounted", s)
	}
	if r := s.RetainedForPopulation(5); r.Samples != 1 || !r.Incomplete {
		t.Fatal("caller count replaced the Java population", r)
	}
	complete := (*JavaHTTPSummary)(nil)
	ObserveJavaHTTPAction(&complete, javaDiagnosticFixture())
	if r := complete.RetainedForPopulation(3); r.Incomplete {
		t.Fatal("another protocol's same-named actions diluted Java coverage", r)
	}
	var actions *JavaHTTPActions
	AddJavaHTTPAction(&actions, "same", 3, complete)
	if a := actions.Actions["same"]; a.Population != 1 || a.Diagnostics.Incomplete || actions.Incomplete {
		t.Fatal("action envelope kept the mixed-protocol count", actions)
	}
	legacy := s.Clone()
	legacy.Population = 0
	if !legacy.Valid() || legacy.populationKnown() {
		t.Fatal("a legacy summary must stay valid with an unknown population", legacy)
	}
	forged := s.Clone()
	forged.InvalidSamples = 1
	if !forged.Valid() {
		t.Fatal("population equal to the observed samples refused", forged)
	}
	forged.Population = 1
	if forged.Valid() {
		t.Fatal("population below the observed samples accepted")
	}
}

func TestJavaHTTPSummaryPopulationMergesOnlyWhenEverySideCounted(t *testing.T) {
	counted := func() *JavaHTTPSummary {
		var s *JavaHTTPSummary
		ObserveJavaHTTPAction(&s, javaDiagnosticFixture())
		ObserveJavaHTTPAction(&s, nil)
		return s
	}
	var merged *JavaHTTPSummary
	MergeJavaHTTPSummary(&merged, counted())
	MergeJavaHTTPSummary(&merged, counted())
	if merged.Population != 4 || merged.Samples != 2 {
		t.Fatal("counted populations did not add", merged)
	}
	var legacy *JavaHTTPSummary
	MergeJavaHTTPDiagnostic(&legacy, javaDiagnosticFixture())
	MergeJavaHTTPSummary(&merged, legacy)
	if merged.Population != 0 || merged.Samples != 3 || !merged.Valid() {
		t.Fatal("a legacy peer's uncounted actions became a known population", merged)
	}
	MergeJavaHTTPSummary(&merged, counted())
	if merged.Population != 0 {
		t.Fatal("unknown population recovered after a counted peer", merged)
	}
	// A refused peer's actions are uncounted even when nothing was merged yet.
	var refusedFirst *JavaHTTPSummary
	MergeJavaHTTPSummary(&refusedFirst, &JavaHTTPSummary{Version: 9})
	MergeJavaHTTPSummary(&refusedFirst, counted())
	if refusedFirst.Population != 0 || !refusedFirst.Incomplete {
		t.Fatal("a refused peer became a counted zero", refusedFirst)
	}
	var capped JavaHTTPSummary
	capped.SetPopulation(javaHTTPMaxSamples + 1)
	if capped.Population != javaHTTPMaxSamples || !capped.SampleLimitReached {
		t.Fatal("population did not saturate", capped)
	}
}
