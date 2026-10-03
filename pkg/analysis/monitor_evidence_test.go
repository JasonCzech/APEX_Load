package analysis

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMonitorEvidenceLegacyAndUnavailableEnvelope(t *testing.T) {
	_, _, s := managementFixture(t)
	a := managementAttribution(s)
	unavailable, err := NewUnavailableManagementSnapshot(*s, "permission_denied")
	if err != nil {
		t.Fatal(err)
	}
	e := MonitorEvidenceEnvelope{Version: MonitorEvidenceVersion, MessagingManagement: unavailable, Attribution: &a}
	b, err := EncodeMonitorEvidence(e)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"points":[]`)) || !bytes.Contains(b, []byte(`"rows":[]`)) {
		t.Fatal("null public arrays")
	}
	decoded, err := DecodeMonitorEvidence(b)
	if err != nil || decoded.Version != 2 || len(decoded.Points) != 0 || decoded.MessagingManagement.UnavailableReason != "permission_denied" || decoded.Attribution.RunID != a.RunID {
		t.Fatalf("unavailable durable decode %v", err)
	}
	zero := 0.0
	p := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: a.ProjectID, RunID: a.RunID, Source: a.SourceID, TimestampUTC: s.CapturedAt, MetricName: "host.cpu", Kind: KindGauge, Value: &zero, Unit: "percent", Quality: QualityValid}
	legacy, _ := json.Marshal([]MetricPoint{p})
	old, err := DecodeMonitorEvidence(legacy)
	if err != nil || old.Version != 1 || old.Attribution != nil || old.MessagingManagement != nil || len(old.Points) != 1 || *old.Points[0].Value != 0 {
		t.Fatalf("legacy behavior changed %v", err)
	}
	rewritten, err := EncodeMonitorEvidence(old)
	if err != nil || !bytes.Equal(legacy, rewritten) {
		t.Fatalf("legacy format changed %s %v", rewritten, err)
	}
	empty, err := DecodeMonitorEvidence([]byte(`[]`))
	if err != nil || empty.Version != 1 {
		t.Fatal("historical empty array unreadable")
	}
	for _, raw := range [][]byte{[]byte(`null`), []byte(`{"version":2,"points":[]}`), []byte(`{"version":3,"points":[]}`), bytes.Replace(b, []byte(`"points":[]`), []byte(`"points":null`), 1), bytes.Replace(b, []byte(`"version":2`), []byte(`"version":2,"Version":1`), 1), bytes.Replace(b, []byte(`"source_revision":1`), []byte(`"source_revision":null`), 1), append(bytes.Repeat([]byte(" "), MaxMonitorEvidenceBytes), b...)} {
		if _, err := DecodeMonitorEvidence(raw); err == nil {
			t.Fatal("malformed envelope accepted")
		}
	}
}
func TestMonitorEvidenceCoreExcludesMutableReceiptAndRunAttribution(t *testing.T) {
	_, _, s := managementFixture(t)
	a := managementAttribution(s)
	e := MonitorEvidenceEnvelope{Version: 2, MessagingManagement: s, Attribution: &a}
	core, err := MonitorEvidenceCoreBytes(e)
	if err != nil {
		t.Fatal(err)
	}
	other := e.Clone()
	other.Attribution.RunID = "different-run"
	other.Attribution.JobID = "different-run:source-1"
	other.Attribution.ReceivedAt = other.Attribution.ReceivedAt.Add(time.Minute)
	same, err := MonitorEvidenceCoreBytes(other)
	if err != nil || !bytes.Equal(core, same) {
		t.Fatalf("cohort core changed for receipt attribution %v", err)
	}
	changed := e.Clone()
	changed.MessagingManagement.Rows[0].KafkaOffsets.TopicID = "other-topic"
	different, err := MonitorEvidenceCoreBytes(changed)
	if err != nil || bytes.Equal(core, different) {
		t.Fatal("immutable observation excluded from retry core")
	}
	artifact1, _ := EncodeMonitorEvidence(e)
	artifact2, _ := EncodeMonitorEvidence(other)
	if bytes.Equal(artifact1, artifact2) {
		t.Fatal("private attributed artifacts lost run ownership")
	}
	clone := e.Clone()
	*clone.MessagingManagement.Rows[0].KafkaOffsets.CommittedNext.Value = "0"
	clone.Attribution.ProjectID = "other"
	if *e.MessagingManagement.Rows[0].KafkaOffsets.CommittedNext.Value == "0" || e.Attribution.ProjectID == "other" {
		t.Fatal("envelope clone aliases")
	}
}
func TestMonitorEvidenceOwnershipBoundsAndPointClone(t *testing.T) {
	_, _, s := managementFixture(t)
	a := managementAttribution(s)
	e := MonitorEvidenceEnvelope{Version: 2, MessagingManagement: s, Attribution: &a}
	bad := e.Clone()
	bad.Attribution.SourceID = "foreign"
	if err := bad.Validate(); err == nil {
		t.Fatal("foreign attribution accepted")
	}
	values, err := s.ProjectScalars(a)
	if err != nil {
		t.Fatal(err)
	}
	e.Points = values
	e.Points[0].Value = nil
	e.Points[0].Quality = QualityMissing
	elapsed := int64(1)
	e.Points[0].RelativeTimeMS = &elapsed
	clone := e.Clone()
	clone.Points[0].Dimensions["entity_id"] = "changed"
	*clone.Points[0].RelativeTimeMS = 2
	if e.Points[0].Dimensions["entity_id"] == "changed" || *e.Points[0].RelativeTimeMS != 1 {
		t.Fatal("point clone aliases")
	}
	core, err := MonitorEvidenceCoreBytes(e)
	if err != nil {
		t.Fatal(err)
	}
	e.Points[0].ReceivedAt = e.Points[0].ReceivedAt.Add(time.Hour)
	again, err := MonitorEvidenceCoreBytes(e)
	if err != nil || !bytes.Equal(core, again) {
		t.Fatal("point receive time changed immutable core")
	}
	raw, _ := EncodeMonitorEvidence(e)
	badraw := bytes.Replace(raw, []byte(`"points":[`), []byte(`"Points":[`), 1)
	if _, err := DecodeMonitorEvidence(badraw); err == nil {
		t.Fatal("case-aliased required array accepted")
	}
	e.Points = append(e.Points, e.Points[0])
	e.Points[len(e.Points)-1].ProjectID = "foreign"
	if err := e.Validate(); err == nil {
		t.Fatal("foreign point accepted")
	}
	// Point-count and byte limits are explicit, even when all individual points
	// are otherwise legal; no evidence is silently trimmed.
	casepoint := values[0]
	casepoint.Dimensions = map[string]string{"label": "one", "Label": "two"}
	caseenv := MonitorEvidenceEnvelope{Version: 2, Points: []MetricPoint{casepoint}}
	casebody, err := EncodeMonitorEvidence(caseenv)
	if err != nil {
		t.Fatal(err)
	}
	casegot, err := DecodeMonitorEvidence(casebody)
	if err != nil || len(casegot.Points[0].Dimensions) != 2 {
		t.Fatalf("case-distinct dimension semantics changed: %v", err)
	}
	p := values[0]
	e = MonitorEvidenceEnvelope{Version: 2, Points: make([]MetricPoint, 513)}
	for i := range e.Points {
		e.Points[i] = p
	}
	if _, err := EncodeMonitorEvidence(e); err == nil {
		t.Fatal("513 points accepted")
	}
	p.Dimensions = map[string]string{}
	for i := 0; i < 16; i++ {
		p.Dimensions[strings.Repeat(string(rune('a'+i)), 128)] = strings.Repeat("x", 128)
	}
	e.Points = make([]MetricPoint, 512)
	for i := range e.Points {
		e.Points[i] = p
	}
	if _, err := EncodeMonitorEvidence(e); err == nil {
		t.Fatal("oversized encoded envelope accepted")
	}
}

func TestMonitorEvidenceRejectsUnsafeProviderIdentity(t *testing.T) {
	for _, provider := range []string{"https://user:password@broker.internal", "https://broker.internal?token=hidden", "https://broker.internal#hidden"} {
		_, _, snapshot := managementFixture(t)
		snapshot.Provider = provider
		attribution := managementAttribution(snapshot)
		evidence := MonitorEvidenceEnvelope{Version: MonitorEvidenceVersion, MessagingManagement: snapshot, Attribution: &attribution}
		if _, err := EncodeMonitorEvidence(evidence); err == nil {
			t.Fatal("unsafe provider entered a durable envelope")
		}
		// JSON marshaling alone is not the validated encoder. Decoding an
		// existing artifact must independently enforce the disclosure rule.
		raw, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeMonitorEvidence(raw); err == nil {
			t.Fatal("unsafe provider entered a reopened archive envelope")
		}
	}
}
