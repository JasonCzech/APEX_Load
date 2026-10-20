package analysis

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParquetPrecisionMappingVersionIsSeparateFromIdentity(t *testing.T) {
	for _, policy := range []string{PrecisionRejectPotentialLoss, PrecisionAllowFloat64Rounding} {
		m, in := precisionBindingFixture()
		m.Version = 3
		m.PrecisionPolicy = policy
		if err := m.Validate(in[0].Schema); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := DecodeParquetMappingDefinition(raw)
		if err != nil || restored.Version != 3 || restored.IdentityVersion() != 2 || restored.PrecisionPolicy != policy {
			t.Fatal(restored, err)
		}
		original, err := ReviewParquetPrecision(m, in)
		if err != nil {
			t.Fatal(err)
		}
		if policy == PrecisionRejectPotentialLoss {
			m.PrecisionPolicy = PrecisionAllowFloat64Rounding
			changed, err := ReviewParquetPrecision(m, in)
			if err != nil || changed.SHA256 == original.SHA256 {
				t.Fatal("choice unbound", err)
			}
		}
	}
	for _, version := range []int{1, 2} {
		m, in := precisionBindingFixture()
		m.Version = version
		if m.IdentityVersion() != version {
			t.Fatal("legacy identity changed")
		}
		if err := m.Validate(in[0].Schema); err != nil {
			t.Fatal(err)
		}
		m.PrecisionPolicy = PrecisionAllowFloat64Rounding
		if err := m.Validate(in[0].Schema); err == nil {
			t.Fatal("new policy silently admitted under legacy version")
		}
	}
}
func TestParquetPrecisionMappingRejectsUnknownOrMissingPolicy(t *testing.T) {
	for _, policy := range []string{"", "allow", "ALLOW_FLOAT64_ROUNDING", strings.Repeat("x", 257)} {
		m, in := precisionBindingFixture()
		m.Version = 3
		m.PrecisionPolicy = policy
		if err := m.Validate(in[0].Schema); err == nil {
			t.Fatal("invalid precision policy admitted")
		}
		raw, _ := json.Marshal(m)
		if _, err := DecodeParquetMappingDefinition(raw); err == nil {
			t.Fatal("invalid precision policy decoded")
		}
	}
	m, _ := precisionBindingFixture()
	m.Version = 3
	m.PrecisionPolicy = PrecisionRejectPotentialLoss
	raw, _ := json.Marshal(m)
	for _, bad := range []string{strings.Replace(string(raw), `"precision_policy"`, `"Precision_Policy"`, 1), strings.Replace(string(raw), `"precision_policy":"reject_potential_loss"`, `"precision_policy":"reject_potential_loss","precision_policy":"allow_float64_rounding"`, 1)} {
		if _, err := DecodeParquetMappingDefinition([]byte(bad)); err == nil {
			t.Fatal("ambiguous key accepted")
		}
	}
}

func TestParquetPrecisionManifestRetainsIdentityVersionTwo(t *testing.T) {
	old, mapping := manifestFixture(t)
	mapping.Version = 3
	mapping.PrecisionPolicy = PrecisionRejectPotentialLoss
	m, err := NewNormalizedPartitionManifest(old.ProjectID, old.DatasetID, mapping, old.Partitions)
	if err != nil {
		t.Fatal(err)
	}
	if m.MappingVersion != 3 || m.Partitions[0].Stats.IdentityVersion != 2 {
		t.Fatal("mapping version became identity schema")
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var restored NormalizedPartitionManifest
	if err := json.Unmarshal(raw, &restored); err != nil || restored.Validate() != nil {
		t.Fatal("retained manifest refused", err)
	}
	at := *m.Partitions[0].Stats.MinTime
	q := PartitionSelection{Metric: "latency", IdentityVersion: 2, From: at, To: at.Add(time.Second)}
	if got, err := m.Select(q); err != nil || len(got) != 1 {
		t.Fatal("version2 identity query lost partition", err)
	}
	q.IdentityVersion = 3
	if _, err := m.Select(q); err == nil {
		t.Fatal("invented identity schema admitted")
	}
	m.Partitions[0].Stats.IdentityVersion = 3
	m.Digest, _ = m.canonicalDigest()
	if m.Validate() == nil {
		t.Fatal("invented identity persisted")
	}
}
func TestParquetPrecisionConsentRequiresExactReviewForNewLossyWork(t *testing.T) {
	m, in := precisionBindingFixture()
	if _, err := AdmitParquetPrecision(m, in, ""); err == nil {
		t.Fatal("legacy new import silently rounded")
	}
	m.Version = 3
	m.PrecisionPolicy = PrecisionRejectPotentialLoss
	review, err := ReviewParquetPrecision(m, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitParquetPrecision(m, in, review.SHA256); err == nil {
		t.Fatal("reject policy ignored")
	}
	m.PrecisionPolicy = PrecisionAllowFloat64Rounding
	if _, err := AdmitParquetPrecision(m, in, review.SHA256); err == nil {
		t.Fatal("stale policy acknowledgement accepted")
	}
	review, err = ReviewParquetPrecision(m, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AdmitParquetPrecision(m, in, ""); err == nil {
		t.Fatal("missing acknowledgement accepted")
	}
	if got, err := AdmitParquetPrecision(m, in, review.SHA256); err != nil || got.SHA256 != review.SHA256 {
		t.Fatal(err)
	}
	in[0].SHA256 = strings.Repeat("c", 64)
	if _, err := AdmitParquetPrecision(m, in, review.SHA256); err == nil {
		t.Fatal("changed file accepted")
	}
	// Non-DECIMAL legacy input still works; stale acknowledgements never disappear.
	m, in = precisionBindingFixture()
	in = in[:1]
	in[0].Schema.Columns[1].ConvertedType = ""
	in[0].Schema.Columns[1].PhysicalType = "DOUBLE"
	if got, err := AdmitParquetPrecision(m, in, ""); err != nil || len(got.Risks) != 0 {
		t.Fatal(err)
	}
	if _, err := AdmitParquetPrecision(m, in, review.SHA256); err == nil {
		t.Fatal("stale acknowledgement ignored on risk-free input")
	}
}
