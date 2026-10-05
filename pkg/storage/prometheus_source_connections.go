package storage

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

func validatePrometheusSourceTx(tx *storeTx, m *MonitorSource) error {
	var raw string
	if err := tx.QueryRow(`SELECT payload_json FROM prometheus_connections WHERE id=? AND project_id=?`, m.ConnectionID, m.ProjectID).Scan(&raw); err != nil {
		return fmt.Errorf("Prometheus connection unavailable in project: %w", err)
	}
	c, err := UnmarshalPrometheusConnectionPrivate([]byte(raw))
	if err != nil {
		return err
	}
	if m.ExecutorMode == "" {
		m.ExecutorMode = c.CollectionMode
	}
	if m.ExecutorMode != c.CollectionMode || m.ExecutorMode == "collector" && m.CollectorID != c.CollectorAgentID || m.ExecutorMode == "direct" && m.CollectorID != "" {
		return errors.New("monitor execution location must match its connection")
	}
	m.Destination = c.Config.Endpoint
	if m.ExecutorMode == "collector" {
		var rawDestinations string
		if err = tx.QueryRow(`SELECT destinations FROM run_monitor_collector_readiness WHERE collector_id=? AND project_id=?`, m.CollectorID, m.ProjectID).Scan(&rawDestinations); err != nil {
			return errors.New("collector must advertise the Prometheus destination before source creation")
		}
		var destinations []string
		if err = json.Unmarshal([]byte(rawDestinations), &destinations); err != nil {
			return err
		}
		enrolled := false
		for _, destination := range destinations {
			origin, key, hasKey := strings.Cut(destination, "#")
			if origin != m.Destination {
				continue
			}
			if c.Snapshot().RequiresCredentials() && !hasKey {
				continue
			}
			if m.CollectorPublicKey != "" && m.CollectorPublicKey != key {
				return errors.New("collector key differs from enrolled destination")
			}
			m.CollectorPublicKey = key
			enrolled = true
			break
		}
		if !enrolled {
			return errors.New("collector destination enrollment unavailable")
		}
	}
	return ValidatePrometheusSourceConnection(*m, c.Snapshot())
}
func ValidatePrometheusSourceConnection(source MonitorSource, connection *PrometheusConnectionSnapshot) error {
	mode := source.ExecutorMode
	if mode == "" {
		mode = "collector"
	}
	if source.Pack != monitorprovider.PackPrometheusQuery || connection == nil || source.ProjectID != connection.ProjectID || source.ConnectionID != connection.ConnectionID || mode != connection.CollectionMode || source.Destination != connection.Config.Endpoint {
		return errors.New("Prometheus source must match its connection execution location and destination")
	}
	if mode == "direct" {
		if source.CollectorID != "" || source.CollectorPublicKey != "" {
			return errors.New("direct Prometheus source cannot retain a collector assignment or encryption key")
		}
		return nil
	}
	if source.CollectorID != connection.CollectorAgentID {
		return errors.New("Prometheus source must use its connection assigned collector")
	}
	if source.CollectorPublicKey == "" && !connection.RequiresCredentials() {
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(source.CollectorPublicKey)
	if err != nil {
		return errors.New("Prometheus collector encryption identity unavailable")
	}
	if _, err = ecdh.X25519().NewPublicKey(key); err != nil {
		return errors.New("Prometheus collector encryption identity unavailable")
	}
	return nil
}
