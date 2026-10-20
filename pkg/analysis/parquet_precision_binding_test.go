package analysis

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func precisionBindingFixture() (ParquetMapping, []ParquetPrecisionInput) {
	p, s := int32(38), int32(2)
	m := ParquetMapping{Version: 2, Timestamp: ParquetColumnPath{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []ParquetMeasure{{Value: ParquetColumnPath{"amount"}, Name: "amount", Kind: KindGauge, Unit: "count"}}}
	schema := ParquetSchemaPreview{Columns: []ParquetColumn{{PathComponents: []string{"ts"}, PhysicalType: "INT64"}, {PathComponents: []string{"amount"}, ConvertedType: "DECIMAL", Precision: &p, Scale: &s}}}
	return m, []ParquetPrecisionInput{{SHA256: strings.Repeat("a", 64), Schema: schema}, {SHA256: strings.Repeat("b", 64), Schema: schema}}
}
func TestParquetPrecisionReviewBindsMappingFilesAndMetadata(t *testing.T) {
	m, in := precisionBindingFixture()
	want, err := ReviewParquetPrecision(m, in)
	if err != nil {
		t.Fatal(err)
	}
	if want.SourceCount != 2 || len(want.Risks) != 2 || want.Risks[0].FileSHA256 != in[0].SHA256 || !precisionDigestValid(want.SHA256) {
		t.Fatal(want)
	}
	reversed := []ParquetPrecisionInput{in[1], in[0]}
	got, err := ReviewParquetPrecision(m, reversed)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("source order changed binding", got, err)
	}
	for _, kind := range []string{"mapping", "content", "schema", "source set"} {
		t.Run(kind, func(t *testing.T) {
			m, in := precisionBindingFixture()
			switch kind {
			case "mapping":
				m.Measures[0].Unit = "bytes"
			case "content":
				in[0].SHA256 = strings.Repeat("c", 64)
			case "schema":
				x := int32(1)
				in[0].Schema.Columns[1].Scale = &x
			case "source set":
				in = in[:1]
			}
			changed, err := ReviewParquetPrecision(m, in)
			if err != nil || changed.SHA256 == want.SHA256 {
				t.Fatal("unbound revision", err)
			}
		})
	}
	want.Risks[0].Column[0] = "changed"
	if in[0].Schema.Columns[1].PathComponents[0] != "amount" {
		t.Fatal("borrowed schema")
	}
}
func TestParquetPrecisionReviewRefusesPartialAndAmbiguousEvidence(t *testing.T) {
	for _, kind := range []string{"empty", "oversized", "duplicate", "bad hash", "uppercase", "absent column", "missing metadata"} {
		t.Run(kind, func(t *testing.T) {
			m, in := precisionBindingFixture()
			switch kind {
			case "empty":
				in = nil
			case "oversized":
				in = make([]ParquetPrecisionInput, 1025)
			case "duplicate":
				in[1].SHA256 = in[0].SHA256
			case "bad hash":
				in[0].SHA256 = strings.Repeat("g", 64)
			case "uppercase":
				in[0].SHA256 = strings.Repeat("A", 64)
			case "absent column":
				in[1].Schema.Columns = in[1].Schema.Columns[:1]
			case "missing metadata":
				in[1].Schema.Columns[1].Precision = nil
			}
			got, err := ReviewParquetPrecision(m, in)
			if err == nil || got != nil {
				t.Fatal("accepted partial review", got, err)
			}
		})
	}
	m, in := precisionBindingFixture()
	in = append(in[:0], in[0])
	in[0].Schema.Columns[1].ConvertedType = ""
	in[0].Schema.Columns[1].PhysicalType = "DOUBLE"
	got, err := ReviewParquetPrecision(m, in)
	if err != nil || len(got.Risks) != 0 {
		t.Fatal("nondecimal input invented risks", got, err)
	}
}
func TestParquetPrecisionReviewBoundsWholeDisclosure(t *testing.T) {
	m, in := precisionBindingFixture()
	one := in[0]
	in = nil
	for i := 0; i < 65; i++ {
		v := one
		v.SHA256 = fmt.Sprintf("%064x", i+1)
		in = append(in, v)
	}
	if got, err := ReviewParquetPrecision(m, in); err == nil || got != nil {
		t.Fatal("accepted risk prefix")
	}
	got, err := ReviewParquetPrecision(m, in[:64])
	if err != nil || len(got.Risks) != 64 {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil || len(raw) > MaxParquetPrecisionReviewBytes {
		t.Fatal("review exceeds byte bound", len(raw), err)
	}
	// Maximum allowed nesting and escaped name bytes amplify encoded disclosure.
	path := make(ParquetColumnPath, 16)
	for i := range path {
		path[i] = strings.Repeat("\"", 256)
	}
	m.Measures[0].Value = path
	one.Schema.Columns[1].PathComponents = path
	for i := range in {
		in[i].Schema = one.Schema
	}
	if got, err := ReviewParquetPrecision(m, in[:64]); err == nil || got != nil || !strings.Contains(err.Error(), "encoded byte bound") {
		t.Fatal("encoded review was not refused at its byte bound", err)
	}
}

func TestParquetPrecisionReviewRetainsFailingFileProvenance(t *testing.T) {
	mapping, inputs := precisionBindingFixture()
	inputs[1].Schema.Columns = inputs[1].Schema.Columns[:1]
	review, err := ReviewParquetPrecision(mapping, inputs)
	if review != nil || err == nil || !strings.Contains(err.Error(), "file 1:") {
		t.Fatalf("schema drift lost file provenance: %+v %v", review, err)
	}
}

func TestParquetPrecisionAcknowledgementIdentityBounds(t *testing.T) {
	mapping := ParquetMapping{Version: 3, PrecisionPolicy: PrecisionAllowFloat64Rounding, Timestamp: ParquetColumnPath{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []ParquetMeasure{{Value: ParquetColumnPath{"value"}, Name: "value", Kind: KindGauge, Unit: "count"}}}
	digest := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, project, id string
		revision          int64
		valid             bool
	}{
		{"normal", "project", "import", 7, true},
		{"maximum", strings.Repeat("p", 128), strings.Repeat("i", 128), (1 << 62) - 1, true},
		{"empty", "", "import", 7, false},
		{"whitespace", "project", " ", 7, false},
		{"long project", strings.Repeat("p", 129), "import", 7, false},
		{"long import", "project", strings.Repeat("i", 129), 7, false},
		{"nul", "project", "a\x00b", 7, false},
		{"invalid UTF8", "\xff", "import", 7, false},
		{"zero revision", "project", "import", 0, false},
		{"overflow revision", "project", "import", 1 << 62, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParquetPrecisionAcknowledgement(tc.project, tc.id, tc.revision, mapping, digest)
			if (err == nil) != tc.valid || tc.valid && len(got) != 64 {
				t.Fatal(got, err)
			}
		})
	}
}

func TestParquetPrecisionStreamingOwnsSnapshotAndPreservesWireContract(t *testing.T) {
	mapping, inputs := precisionBindingFixture()
	r, err := NewParquetPrecisionReviewer(mapping, len(inputs))
	if err != nil {
		t.Fatal(err)
	}
	// Caller edits after construction must not silently change the bound mapping.
	mapping.Measures[0].Value[0] = "changed"
	mapping.Measures[0].Unit = "bytes"
	for i := len(inputs) - 1; i >= 0; i-- {
		if err := r.AddSource(inputs[i]); err != nil {
			t.Fatal(err)
		}
	}
	// Schema paths and metadata can be discarded/reused after AddSource.
	inputs[0].Schema.Columns[1].PathComponents[0] = "discarded"
	*inputs[0].Schema.Columns[1].Scale = 0
	got, err := r.Review()
	if err != nil {
		t.Fatal(err)
	}
	const wantSHA = "5f3966b658abfd51cd6cbcf2a0837d7c575ef75f48d8d94fb429b8dea413e64b"
	if got.SHA256 != wantSHA || got.Risks[0].Column[0] != "amount" || got.Risks[0].Scale != 2 {
		t.Fatalf("wire contract changed: %+v", got)
	}
	got.Risks[0].Column[0] = "tampered"
	got.Risks[0].Scale = 0
	again, err := r.Review()
	if err != nil || again.SHA256 != wantSHA || again.Risks[0].Column[0] != "amount" || again.Risks[0].Scale != 2 {
		t.Fatal("review borrowed internal evidence", again, err)
	}
}

func TestParquetPrecisionStreamingRefusesIncompleteOrFailedCohort(t *testing.T) {
	for _, kind := range []string{"partial", "invalid then valid", "duplicate", "extra source", "zero", "nil"} {
		t.Run(kind, func(t *testing.T) {
			mapping, inputs := precisionBindingFixture()
			r, err := NewParquetPrecisionReviewer(mapping, 2)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.AddSource(inputs[0]); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "invalid then valid":
				invalid := inputs[1]
				invalid.SHA256 = "invalid"
				if err := r.AddSource(invalid); err == nil {
					t.Fatal("accepted invalid source")
				}
				if err := r.AddSource(inputs[1]); err == nil {
					t.Fatal("recovered a poisoned review")
				}
			case "duplicate":
				if err := r.AddSource(inputs[0]); err == nil {
					t.Fatal("accepted duplicate source")
				}
			case "extra source":
				if err := r.AddSource(inputs[1]); err != nil {
					t.Fatal(err)
				}
				extra := inputs[1]
				extra.SHA256 = strings.Repeat("c", 64)
				if err := r.AddSource(extra); err == nil {
					t.Fatal("accepted undeclared source")
				}
			case "zero":
				r = &ParquetPrecisionReviewer{}
			case "nil":
				r = nil
			}
			if review, err := r.Review(); err == nil || review != nil {
				t.Fatal("published partial/failed review", review, err)
			}
			if review, err := r.Admit(""); err == nil || review != nil {
				t.Fatal("admitted partial/failed review", review, err)
			}
			if kind == "zero" || kind == "nil" {
				if err := r.AddSource(inputs[1]); err == nil {
					t.Fatal("uninitialized reviewer accepted input")
				}
			}
		})
	}
}

func TestParquetPrecisionStreamingConsentUsesFrozenMapping(t *testing.T) {
	mapping, inputs := precisionBindingFixture()
	mapping.Version = 3
	mapping.PrecisionPolicy = PrecisionAllowFloat64Rounding
	r, err := NewParquetPrecisionReviewer(mapping, len(inputs))
	if err != nil {
		t.Fatal(err)
	}
	mapping.PrecisionPolicy = PrecisionRejectPotentialLoss
	for _, input := range inputs {
		if err := r.AddSource(input); err != nil {
			t.Fatal(err)
		}
	}
	review, err := r.Review()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Admit(review.SHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Admit(""); err == nil {
		t.Fatal("missing acknowledgement accepted")
	}
	if _, err := r.Admit(strings.Repeat("f", 64)); err == nil {
		t.Fatal("foreign acknowledgement accepted")
	}
}

func TestParquetPrecisionStreamingMaximumSourceCohort(t *testing.T) {
	mapping, inputs := precisionBindingFixture()
	input := inputs[0]
	input.Schema.Columns[1].ConvertedType = ""
	input.Schema.Columns[1].PhysicalType = "DOUBLE"
	for i := 2; i < 1024; i++ {
		input.Schema.Columns = append(input.Schema.Columns, ParquetColumn{PathComponents: []string{fmt.Sprintf("unused_%d", i)}, PhysicalType: "DOUBLE"})
	}
	r, err := NewParquetPrecisionReviewer(mapping, 1024)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1024; i++ {
		input.SHA256 = fmt.Sprintf("%064x", i+1)
		if err := r.AddSource(input); err != nil {
			t.Fatal(err)
		}
	}
	input.Schema.Columns = nil
	review, err := r.Review()
	if err != nil || review.SourceCount != 1024 || len(review.Risks) != 0 {
		t.Fatal("maximum source cohort failed", review, err)
	}
}
