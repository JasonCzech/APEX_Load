package api

// Collector heartbeat + metric-pack ingestion. A registered monitoring collector
// agent authenticates with its own token (hashed, compared in constant time — the
// GitHub-webhook public-route precedent: registered without the RBAC auth
// middleware, verified in-handler) and pushes health + scoped metric packs. The
// latest snapshot per collector is held in memory (live state, like running-test
// metrics — not persisted) and broadcast to the live-monitoring WebSocket feed;
// only health + last-seen are persisted on the collector row.

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// collectorStaleAfter is the freshness window after which a collector's latest
// snapshot is reported stale (≈ a few missed heartbeats at a typical interval).
const collectorStaleAfter = 90 * time.Second
const collectorSnapshotRetention = 10 * time.Minute

// Heartbeat ingest bounds. The heartbeat route is collector-token authenticated
// in-handler and not in the RBAC catalog, so these caps (not the 128 MiB global
// backstop) bound what a single collector can push and pin in memory.
const (
	maxCollectorHeartbeatBytes = 256 << 10 // 256 KiB request body
	maxCollectorPacks          = 64        // packs per heartbeat
	maxCollectorSamplesPerPack = 512       // samples per pack
)

// collectorMetricPack is one scoped metric pack pushed by a collector agent.
type collectorMetricPack struct {
	Pack      string             `json:"pack"`
	Timestamp time.Time          `json:"timestamp"`
	Samples   map[string]float64 `json:"samples"`
}

// collectorMetricSnapshot is the latest heartbeat payload for a collector.
type collectorMetricSnapshot struct {
	CollectorID string                `json:"collector_id"`
	ProjectID   string                `json:"project_id,omitempty"`
	Health      string                `json:"health"`
	ReceivedAt  time.Time             `json:"received_at"`
	Packs       []collectorMetricPack `json:"packs"`
}

// collectorMetricStore holds the latest snapshot per collector in memory.
type collectorMetricStore struct {
	mu        sync.RWMutex
	snapshots map[string]collectorMetricSnapshot
}

func newCollectorMetricStore() *collectorMetricStore {
	return &collectorMetricStore{snapshots: make(map[string]collectorMetricSnapshot)}
}

func (c *collectorMetricStore) put(snap collectorMetricSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cutoff := snap.ReceivedAt.Add(-collectorSnapshotRetention)
	for id, existing := range c.snapshots {
		if existing.ReceivedAt.Before(cutoff) {
			delete(c.snapshots, id)
		}
	}
	c.snapshots[snap.CollectorID] = snap
}

func (c *collectorMetricStore) get(id string) (collectorMetricSnapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap, ok := c.snapshots[id]
	return snap, ok
}

func (c *collectorMetricStore) delete(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.snapshots, id)
}

type collectorHeartbeatRequest struct {
	Health string                `json:"health,omitempty"`
	Packs  []collectorMetricPack `json:"packs,omitempty"`
}

// handleCollectorHeartbeat ingests a collector agent's heartbeat: it verifies the
// collector token, records health + last-seen, accepts in-scope metric packs, and
// routes the latest snapshot to live monitoring. Registered WITHOUT the RBAC auth
// middleware (collector-token authenticated in-handler).
func (s *Server) handleCollectorHeartbeat(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	collector, ok := s.loadMonitoringCollector(w, r)
	if !ok {
		return
	}
	token := extractAPIKey(r)
	if token == "" || subtle.ConstantTimeCompare([]byte(hashAPIKey(token)), []byte(collector.TokenHash)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid collector token")
		return
	}

	// This route is not in the RBAC catalog, so it inherits the 128 MiB global
	// body backstop. Heartbeats are tiny — cap the body explicitly so a holder of
	// one valid collector token cannot pin a large allocation in the controller.
	r.Body = http.MaxBytesReader(w, r.Body, maxCollectorHeartbeatBytes)
	var req collectorHeartbeatRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Packs) > maxCollectorPacks {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("too many metric packs (max %d)", maxCollectorPacks))
		return
	}
	health := strings.TrimSpace(req.Health)
	if health == "" {
		health = "healthy"
	}
	if !validCollectorHealth(health) {
		writeError(w, http.StatusBadRequest, "health must be healthy, degraded, or unreachable")
		return
	}

	// Scope enforcement: every pushed pack must be in the collector's allow-list.
	allowed := make(map[string]struct{}, len(collector.AllowedMetricPacks))
	for _, p := range collector.AllowedMetricPacks {
		allowed[p] = struct{}{}
	}
	now := time.Now().UTC()
	packs := make([]collectorMetricPack, 0, len(req.Packs))
	accepted := make([]string, 0, len(req.Packs))
	for _, p := range req.Packs {
		name := strings.TrimSpace(p.Pack)
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; !ok {
			writeError(w, http.StatusUnprocessableEntity, "metric pack not allowed for this collector: "+name)
			return
		}
		if len(p.Samples) > maxCollectorSamplesPerPack {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("metric pack %q has too many samples (max %d)", name, maxCollectorSamplesPerPack))
			return
		}
		ts := p.Timestamp
		if ts.IsZero() {
			ts = now
		}
		packs = append(packs, collectorMetricPack{Pack: name, Timestamp: ts, Samples: p.Samples})
		accepted = append(accepted, name)
	}

	if err := s.store.UpdateMonitoringCollectorHealth(collector.ID, health, now); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record heartbeat")
		return
	}
	snap := collectorMetricSnapshot{
		CollectorID: collector.ID,
		ProjectID:   normalizeProjectID(collector.ProjectID),
		Health:      health,
		ReceivedAt:  now,
		Packs:       packs,
	}
	s.collectorMetrics.put(snap)
	s.broadcastCollectorHeartbeat(snap, accepted)
	// Heartbeats are high-frequency telemetry, not audited (would flood the log).
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "accepted",
		"health":         health,
		"accepted_packs": accepted,
		"received_at":    now,
	})
}

// handleGetCollectorMetrics returns the latest ingested snapshot for a collector
// (RBAC: project viewers). Returns a pending/stale placeholder before the first
// heartbeat.
func (s *Server) handleGetCollectorMetrics(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	collector, ok := s.loadMonitoringCollector(w, r)
	if !ok {
		return
	}
	snap, ok := s.collectorMetrics.get(collector.ID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"collector_id": collector.ID,
			"project_id":   normalizeProjectID(collector.ProjectID),
			"health":       collector.Health,
			"packs":        []collectorMetricPack{},
			"stale":        true,
			"pending":      true,
		})
		return
	}
	age := time.Since(snap.ReceivedAt)
	writeJSON(w, http.StatusOK, map[string]any{
		"collector_id": snap.CollectorID,
		"project_id":   snap.ProjectID,
		"health":       snap.Health,
		"received_at":  snap.ReceivedAt,
		"age_seconds":  int(age.Seconds()),
		"stale":        age > collectorStaleAfter,
		"packs":        snap.Packs,
	})
}

func (s *Server) broadcastCollectorHeartbeat(snap collectorMetricSnapshot, packs []string) {
	if s.hub == nil {
		return
	}
	s.hub.BroadcastJSON(map[string]any{
		"type":         "monitoring_collector_heartbeat",
		"collector_id": snap.CollectorID,
		"project_id":   snap.ProjectID,
		"health":       snap.Health,
		"packs":        packs,
		"received_at":  snap.ReceivedAt,
		"timestamp":    time.Now(),
	})
}
