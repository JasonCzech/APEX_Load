package storage

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// PrometheusInspection shares the durable inspection journal and owner CAS with
// AppDynamics. Provider is an immutable assignment discriminator.
type PrometheusInspection = AppDynamicsInspection

type PrometheusInspectionStore interface {
	EnsurePrometheusInspectionSchema() error
	PrometheusCollectorPublicKey(project, collector, endpoint string, now time.Time, requireKey bool) (string, error)
	CreatePrometheusInspection(*PrometheusInspection) error
	GetPrometheusInspection(string) (*PrometheusInspection, error)
	ListPrometheusInspections(string, string, int) ([]*PrometheusInspection, error)
	ClaimPrometheusInspection(string, string, time.Time, time.Time) (bool, error)
	CompletePrometheusInspection(string, string, string, string, time.Time) (bool, error)
}

func (s *SQLiteStore) EnsurePrometheusInspectionSchema() error {
	return s.EnsureAppDynamicsInspectionSchema()
}
func (s *SQLiteStore) CreatePrometheusInspection(op *PrometheusInspection) error {
	return s.createProviderInspection(op, "prometheus")
}
func (s *SQLiteStore) GetPrometheusInspection(id string) (*PrometheusInspection, error) {
	return s.getProviderInspection(id, "prometheus")
}
func (s *SQLiteStore) ListPrometheusInspections(project, collector string, limit int) ([]*PrometheusInspection, error) {
	return s.listProviderInspections(project, collector, "prometheus", limit)
}
func (s *SQLiteStore) ClaimPrometheusInspection(id, owner string, now, until time.Time) (bool, error) {
	return s.claimProviderInspection(id, owner, "prometheus", now, until)
}
func (s *SQLiteStore) CompletePrometheusInspection(id, owner, result, message string, now time.Time) (bool, error) {
	return s.completeProviderInspection(id, owner, result, message, "prometheus", now)
}

func (s *SQLiteStore) PrometheusCollectorPublicKey(project, collector, endpoint string, now time.Time, requireKey bool) (string, error) {
	var health, packsJSON, readyPacksJSON, destinationsJSON string
	var seen, ready time.Time
	err := s.db.QueryRow(`SELECT c.health,c.last_seen_at,c.allowed_metric_packs_json,r.packs,r.destinations,r.seen_at FROM monitoring_collectors c JOIN run_monitor_collector_readiness r ON r.collector_id=c.id AND r.project_id=c.project_id WHERE c.id=? AND c.project_id=?`, collector, project).Scan(&health, &seen, &packsJSON, &readyPacksJSON, &destinationsJSON, &ready)
	if err != nil {
		return "", err
	}
	if health != "healthy" || seen.Before(now.Add(-90*time.Second)) || seen.After(now.Add(30*time.Second)) || ready.Before(now.Add(-90*time.Second)) || ready.After(now.Add(30*time.Second)) {
		return "", errors.New("collector enrollment is not ready")
	}
	var packs, readyPacks, destinations []string
	if json.Unmarshal([]byte(packsJSON), &packs) != nil || json.Unmarshal([]byte(readyPacksJSON), &readyPacks) != nil || json.Unmarshal([]byte(destinationsJSON), &destinations) != nil || !slices.Contains(packs, "prometheus-query") || !slices.Contains(readyPacks, "prometheus-query") || !slices.Contains(readyPacks, "prometheus-query.v1") {
		return "", errors.New("collector Prometheus query contract unavailable")
	}
	for _, destination := range destinations {
		origin, key, hasKey := strings.Cut(destination, "#")
		if origin != endpoint {
			continue
		}
		if !hasKey && !requireKey {
			return "", nil
		}
		decoded, e := base64.StdEncoding.DecodeString(key)
		if e == nil && len(decoded) == 32 {
			if _, e = ecdh.X25519().NewPublicKey(decoded); e == nil {
				return key, nil
			}
		}
	}
	return "", errors.New("collector destination enrollment unavailable")
}
