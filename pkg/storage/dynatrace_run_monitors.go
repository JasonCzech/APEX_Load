package storage

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

func (m MonitorSource) IsProvider() bool { return m.CanonicalProvider() }

func DynatraceDestination(c *DynatraceConnectionSnapshot) string {
	if c == nil {
		return ""
	}
	if c.Config.EnvironmentURL != "" {
		return c.Config.EnvironmentURL
	}
	return c.Config.PlatformURL
}

func validateDynatraceSourceTx(tx *storeTx, m *MonitorSource) error {
	c, err := scanDynatraceConnection(tx.QueryRow(`SELECT `+dynatraceConnectionColumns+` FROM dynatrace_connections WHERE id=? AND project_id=?`, m.ConnectionID, m.ProjectID))
	if err != nil {
		return err
	}
	if m.LegacyConnectionRevision > 0 && m.LegacyConnectionRevision != c.Revision {
		return ErrMonitorRevision
	}
	if m.ExecutorMode == "" {
		m.ExecutorMode = c.CollectionMode
	}
	if m.ExecutorMode != c.CollectionMode || m.ExecutorMode == "collector" && m.CollectorID != c.CollectorAgentID || m.ExecutorMode == "direct" && m.CollectorID != "" {
		return errors.New("monitor execution location must match its connection")
	}
	if err := validateDynatraceSelectedCapabilities(m.Dynatrace, c.Snapshot().AllowedCapabilities); err != nil {
		return err
	}
	m.Destination = DynatraceDestination(c.Snapshot())
	if m.ExecutorMode == "collector" {
		var raw string
		if err = tx.QueryRow(`SELECT destinations FROM run_monitor_collector_readiness WHERE collector_id=? AND project_id=?`, m.CollectorID, m.ProjectID).Scan(&raw); err != nil {
			return errors.New("collector destination enrollment required")
		}
		var dests []string
		if err = json.Unmarshal([]byte(raw), &dests); err != nil {
			return err
		}
		key := ""
		for _, d := range dests {
			origin, k, ok := strings.Cut(d, "#")
			if ok && origin == m.Destination {
				key = k
				break
			}
		}
		if key == "" || m.CollectorPublicKey != "" && m.CollectorPublicKey != key {
			return errors.New("collector key differs from enrolled destination")
		}
		m.CollectorPublicKey = key
		for _, origin := range []string{c.EnvironmentURL, c.PlatformURL} {
			if origin == "" {
				continue
			}
			found := false
			for _, d := range dests {
				found = found || d == origin+"#"+key
			}
			if !found {
				return errors.New("every Dynatrace origin requires the assigned collector encryption identity")
			}
		}
	}
	return ValidateDynatraceSourceConnection(*m, c.Snapshot())
}
func ValidateDynatraceSourceConnection(m MonitorSource, c *DynatraceConnectionSnapshot) error {
	if c == nil || m.Pack != monitorprovider.PackDynatrace || m.ProjectID != c.ProjectID || m.ConnectionID != c.ConnectionID || m.ExecutorMode != c.CollectionMode || m.Destination != DynatraceDestination(c) {
		return errors.New("Dynatrace source must match its connection execution location and destination")
	}
	if err := validateDynatraceSelectedCapabilities(m.Dynatrace, c.AllowedCapabilities); err != nil {
		return err
	}
	if m.ExecutorMode == "direct" {
		if m.CollectorID != "" || m.CollectorPublicKey != "" {
			return errors.New("direct Dynatrace source cannot retain collector identity")
		}
		return nil
	}
	if m.ExecutorMode != "collector" || m.CollectorID == "" || m.CollectorID != c.CollectorAgentID {
		return errors.New("Dynatrace source must use its assigned collector")
	}
	key, err := base64.StdEncoding.DecodeString(m.CollectorPublicKey)
	if err != nil {
		return errors.New("collector encryption identity unavailable")
	}
	if _, err = ecdh.X25519().NewPublicKey(key); err != nil {
		return errors.New("collector encryption identity unavailable")
	}
	return nil
}

func ValidateDynatraceVerifiedSelection(selection *dynatrace.Selection, c *DynatraceConnectionSnapshot) error {
	if c == nil || c.VerifiedRevision != c.Revision || c.VerifiedRevision < 1 {
		return errors.New("Dynatrace connection snapshot required")
	}
	return validateDynatraceSelectedCapabilities(selection, c.Capabilities)
}
func validateDynatraceSelectedCapabilities(s *dynatrace.Selection, c dynatrace.Capabilities) error {
	if s == nil || len(s.Metrics) > 0 && !c.Metrics || s.Problems && !c.Problems || s.Events && !c.Events || s.TopologyMode == "classic" && !c.ClassicTopology || s.TopologyMode == "smartscape" && !c.Smartscape {
		return errors.New("Dynatrace selection exceeds connection capabilities")
	}
	for _, query := range s.Grail {
		if query.Kind == "log" && !c.Logs || (query.Kind == "span" || query.Kind == "trace") && !c.Spans || (query.Kind == "metric" || query.Kind == "aggregate") && !c.GrailMetrics || query.Kind == "event" && !c.GrailEvents {
			return errors.New("Dynatrace selection exceeds connection capabilities")
		}
	}
	return nil
}

// Query progress is not proof of complete evidence. Every selected operation
// must have committed coverage for the same frozen window before that window
// can advance its watermark or satisfy reconciliation stability.
func dynatraceSelectedCoverageComplete(s *dynatrace.Selection, coverage map[string]analysis.ProviderCoverage, from, to time.Time) bool {
	if s == nil || from.IsZero() || !to.After(from) {
		return false
	}
	keys := make([]string, 0, len(s.Metrics)+len(s.Grail)+3)
	for _, m := range s.Metrics {
		keys = append(keys, "metrics:"+m.Name)
	}
	for _, q := range s.Grail {
		keys = append(keys, "grail:"+q.Name)
	}
	if s.Problems {
		keys = append(keys, "problems")
	}
	if s.Events {
		keys = append(keys, "events")
	}
	if s.TopologyMode != "" {
		keys = append(keys, "topology")
	}
	if len(keys) == 0 {
		return false
	}
	for _, key := range keys {
		c, ok := coverage[key]
		if !ok || c.Status != "complete" || c.Truncated || !c.From.Equal(from) || !c.To.Equal(to) {
			return false
		}
	}
	return true
}
