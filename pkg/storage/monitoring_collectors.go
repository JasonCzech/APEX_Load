package storage

import "time"

// MonitoringCollector is a registered restricted-network monitoring agent — the
// APEX analog of a LoadRunner Enterprise SiteScope / monitor-over-firewall
// listener. This is the control-plane record (registration, scoped token, health,
// allowed metric packs); the agent runtime that calls heartbeat is a follow-on.
// The token is stored only as a SHA-256 hash; the plaintext is shown once at
// create/rotate, mirroring service-account API keys.
type MonitoringCollector struct {
	ID                 string     `json:"id"`
	ProjectID          string     `json:"project_id"`
	Name               string     `json:"name"`
	Description        string     `json:"description,omitempty"`
	TokenHash          string     `json:"-"`
	Health             string     `json:"health"` // healthy | degraded | unreachable
	LastSeenAt         *time.Time `json:"last_seen_at,omitempty"`
	AllowedMetricPacks []string   `json:"allowed_metric_packs"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}
