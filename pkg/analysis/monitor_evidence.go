package analysis

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"
)

// Attribution is controller-assigned run ownership and receipt provenance.
// It is intentionally excluded from the collector's immutable retry core.
type MonitorEvidenceAttribution struct {
	ProjectID      string    `json:"project_id"`
	RunID          string    `json:"run_id"`
	ScenarioID     string    `json:"scenario_id,omitempty"`
	JobID          string    `json:"job_id"`
	SourceID       string    `json:"source_id"`
	SourceRevision int64     `json:"source_revision"`
	CollectorID    string    `json:"collector_id"`
	ReceivedAt     time.Time `json:"received_at"`
}

func (a MonitorEvidenceAttribution) Validate() error {
	for _, x := range []string{a.ProjectID, a.RunID, a.SourceID, a.CollectorID} {
		if !validManagementText(x, 256, false) {
			return errors.New("invalid monitor attribution identity")
		}
	}
	if !validManagementText(a.JobID, 768, false) || !validManagementText(a.ScenarioID, 256, true) || a.SourceRevision < 1 || !validManagementTime(a.ReceivedAt) {
		return errors.New("invalid monitor attribution receipt")
	}
	return nil
}
func (a *MonitorEvidenceAttribution) UnmarshalJSON(data []byte) error {
	type plain MonitorEvidenceAttribution
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := requireManagementFields(data, "project_id", "run_id", "job_id", "source_id", "source_revision", "collector_id", "received_at"); err != nil {
		return err
	}
	if err := MonitorEvidenceAttribution(x).Validate(); err != nil {
		return err
	}
	*a = MonitorEvidenceAttribution(x)
	return nil
}

type MonitorEvidenceEnvelope struct {
	Version             uint32                       `json:"version"`
	Points              []MetricPoint                `json:"points"`
	MessagingManagement *MessagingManagementSnapshot `json:"messaging_management,omitempty"`
	Attribution         *MonitorEvidenceAttribution  `json:"attribution,omitempty"`
}

func (e MonitorEvidenceEnvelope) Validate() error {
	if e.Version != 1 && e.Version != MonitorEvidenceVersion {
		return errors.New("unsupported monitor evidence version")
	}
	if len(e.Points) > MaxMonitorEvidencePoints {
		return errors.New("monitor evidence exceeds point limit")
	}
	if e.Version == 1 && (e.MessagingManagement != nil || e.Attribution != nil) {
		return errors.New("legacy monitor evidence cannot carry new fields")
	}
	if e.Version == MonitorEvidenceVersion && len(e.Points) == 0 && e.MessagingManagement == nil {
		return errors.New("empty monitor evidence requires management snapshot")
	}
	for _, p := range e.Points {
		if err := p.Validate(); err != nil {
			return err
		}
	}
	if e.MessagingManagement != nil {
		if err := e.MessagingManagement.Validate(); err != nil {
			return err
		}
	}
	if e.Attribution != nil {
		a := e.Attribution
		if err := a.Validate(); err != nil {
			return err
		}
		if s := e.MessagingManagement; s != nil && (s.SourceID != a.SourceID || s.SourceRevision != a.SourceRevision || s.CollectorID != a.CollectorID) {
			return errors.New("monitor management attribution mismatch")
		}
		for _, p := range e.Points {
			if p.ProjectID != a.ProjectID || p.RunID != a.RunID || p.ScenarioID != a.ScenarioID || p.Source != a.SourceID || p.Host != a.CollectorID {
				return errors.New("monitor point attribution mismatch")
			}
		}
	}
	return nil
}
func (e MonitorEvidenceEnvelope) Clone() MonitorEvidenceEnvelope {
	e.Points = append([]MetricPoint{}, e.Points...)
	for i := range e.Points {
		p := &e.Points[i]
		p.Dimensions = maps.Clone(p.Dimensions)
		p.Derivation = slices.Clone(p.Derivation)
		if p.Value != nil {
			x := *p.Value
			p.Value = &x
		}
		if p.RelativeTimeMS != nil {
			x := *p.RelativeTimeMS
			p.RelativeTimeMS = &x
		}
		if p.ActiveRuntimeMS != nil {
			x := *p.ActiveRuntimeMS
			p.ActiveRuntimeMS = &x
		}
	}
	e.MessagingManagement = e.MessagingManagement.Clone()
	if e.Attribution != nil {
		x := *e.Attribution
		e.Attribution = &x
	}
	return e
}
func (e MonitorEvidenceEnvelope) MarshalJSON() ([]byte, error) {
	type plain MonitorEvidenceEnvelope
	x := plain(e)
	if x.Points == nil {
		x.Points = []MetricPoint{}
	}
	return json.Marshal(x)
}
func (e *MonitorEvidenceEnvelope) UnmarshalJSON(data []byte) error {
	type plain MonitorEvidenceEnvelope
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := requireManagementFields(data, "version", "points"); err != nil {
		return err
	}
	p := MonitorEvidenceEnvelope(x)
	if p.Version != MonitorEvidenceVersion {
		return errors.New("object monitor evidence requires version2")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	*e = p
	return nil
}

// DecodeMonitorEvidence accepts historical bare point arrays and the new
// bounded envelope. Historical arrays never gain guessed management evidence.
func DecodeMonitorEvidence(data []byte) (MonitorEvidenceEnvelope, error) {
	if len(data) > MaxMonitorEvidenceBytes {
		return MonitorEvidenceEnvelope{}, errors.New("monitor evidence exceeds byte limit")
	}
	b := bytes.TrimSpace(data)
	if len(b) > 0 && b[0] == '[' {
		var points []MetricPoint
		if err := json.Unmarshal(b, &points); err != nil {
			return MonitorEvidenceEnvelope{}, err
		}
		e := MonitorEvidenceEnvelope{Version: 1, Points: points}
		if err := e.Validate(); err != nil {
			return MonitorEvidenceEnvelope{}, err
		}
		return e, nil
	}
	var e MonitorEvidenceEnvelope
	if err := json.Unmarshal(b, &e); err != nil {
		return MonitorEvidenceEnvelope{}, err
	}
	return e, nil
}
func EncodeMonitorEvidence(e MonitorEvidenceEnvelope) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	x := e.Clone()
	if s := x.MessagingManagement; s != nil {
		b, err := EncodeMessagingManagement(s)
		if err != nil {
			return nil, err
		}
		x.MessagingManagement, err = DecodeMessagingManagement(b)
		if err != nil {
			return nil, err
		}
	}
	var b []byte
	var err error
	if x.Version == 1 {
		b, err = json.Marshal(x.Points)
	} else {
		b, err = json.Marshal(x)
	}
	if err == nil && len(b) > MaxMonitorEvidenceBytes {
		return nil, errors.New("monitor evidence exceeds byte limit")
	}
	return b, err
}

// Core bytes omit controller attribution/receive times. Snapshot capture,
// generation, source revision and observations are immutable and remain hashed.
func MonitorEvidenceCoreBytes(e MonitorEvidenceEnvelope) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	x := e.Clone()
	x.Attribution = nil
	for i := range x.Points {
		x.Points[i].ReceivedAt = time.Time{}
	}
	return EncodeMonitorEvidence(x)
}
