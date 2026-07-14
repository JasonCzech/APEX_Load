package api

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	generatorDrainStatesStoreKey        = "generator_drain_states"
	fleetAlertNotificationStateStoreKey = "fleet_alert_notification_state"
)

type persistentDrainBackend interface {
	drainableExecutionBackend
	ApplyGeneratorDrainStates(states map[string]string)
	GeneratorDrainStates() map[string]string
}

type FleetAlertsResponse struct {
	Alerts              []FleetAlert `json:"alerts"`
	Count               int          `json:"count"`
	UnacknowledgedCount int          `json:"unacknowledged_count"`
}

type FleetCapacityResponse struct {
	ProjectID           string                       `json:"project_id,omitempty"`
	Region              string                       `json:"region,omitempty"`
	Protocol            string                       `json:"protocol,omitempty"`
	TotalGenerators     int                          `json:"total_generators"`
	EligibleGenerators  int                          `json:"eligible_generators"`
	ActiveSlots         int                          `json:"active_slots"`
	AvailableSlots      int                          `json:"available_slots"`
	ProjectActiveSlots  int                          `json:"project_active_slots,omitempty"`
	ProjectPendingSlots int                          `json:"project_pending_slots,omitempty"`
	ReservedGenerators  int                          `json:"reserved_generators,omitempty"`
	ReservedSlots       int                          `json:"reserved_slots,omitempty"`
	MaxSlots            int                          `json:"max_slots"`
	CompatibleFeatures  []string                     `json:"compatible_features,omitempty"`
	RejectionReasons    []string                     `json:"rejection_reasons,omitempty"`
	ProjectSlots        []FleetProjectSlotAccounting `json:"project_slots,omitempty"`
}

type FleetLoadZonesResponse struct {
	LoadZones []FleetLoadZone `json:"load_zones"`
	Count     int             `json:"count"`
}

type FleetLoadZone struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Region              string   `json:"region"`
	CostLabel           string   `json:"cost_label,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	SupportedProtocols  []string `json:"supported_protocols,omitempty"`
	BenchmarkEvidenceID string   `json:"benchmark_evidence_id,omitempty"`
	BenchmarkEvidence   []string `json:"benchmark_evidence,omitempty"`
	CapacityState       string   `json:"capacity_state"`
	TotalGenerators     int      `json:"total_generators"`
	EligibleGenerators  int      `json:"eligible_generators"`
	ActiveSlots         int      `json:"active_slots"`
	AvailableSlots      int      `json:"available_slots"`
	MaxSlots            int      `json:"max_slots"`
	ActiveVUsers        int      `json:"active_vusers,omitempty"`
	QueuedVUsers        int      `json:"queued_vusers,omitempty"`
	RejectedVUsers      int      `json:"rejected_vusers,omitempty"`
	ErrorPct            float64  `json:"error_pct,omitempty"`
	P95Ms               float64  `json:"p95_ms,omitempty"`
	P99Ms               float64  `json:"p99_ms,omitempty"`
	RejectionReasons    []string `json:"rejection_reasons,omitempty"`
}

type FleetProjectSlotAccounting struct {
	ProjectID          string `json:"project_id"`
	ActiveSlots        int    `json:"active_slots"`
	PendingSlots       int    `json:"pending_slots"`
	RunningTests       int    `json:"running_tests"`
	PendingTests       int    `json:"pending_tests"`
	ReservedGenerators int    `json:"reserved_generators,omitempty"`
	ReservedSlots      int    `json:"reserved_slots,omitempty"`
	FairShareTier      string `json:"fair_share_tier"`
}

type FleetAlert struct {
	ID                        string     `json:"id"`
	Type                      string     `json:"type"`
	Target                    string     `json:"target"`
	Region                    string     `json:"region,omitempty"`
	Severity                  string     `json:"severity"`
	Summary                   string     `json:"summary"`
	Acknowledged              bool       `json:"acknowledged,omitempty"`
	AcknowledgedAt            *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBySubject     string     `json:"acknowledged_by_subject,omitempty"`
	AcknowledgedByDisplayName string     `json:"acknowledged_by_display_name,omitempty"`
	AcknowledgementNote       string     `json:"acknowledgement_note,omitempty"`
}

type GeneratorDrainRequest struct {
	Target      string `json:"target"`
	GeneratorID string `json:"generator_id,omitempty"`
	DrainState  string `json:"drain_state"`
}

func (s *Server) handleGetFleetAlerts(w http.ResponseWriter, r *http.Request) {
	alerts := buildFleetAlerts(s.systemInventory(r.Context()), s.runner.GetSystemSettings().Thresholds)
	s.applyFleetAlertAcknowledgements(alerts)
	s.routeFleetAlertNotifications(alerts)
	writeJSON(w, http.StatusOK, FleetAlertsResponse{Alerts: alerts, Count: len(alerts), UnacknowledgedCount: countUnacknowledgedFleetAlerts(alerts)})
}

type fleetAlertAckRequest struct {
	Acknowledged *bool  `json:"acknowledged,omitempty"`
	Note         string `json:"note,omitempty"`
}

func (s *Server) handleAckFleetAlert(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	alertID := strings.TrimSpace(r.PathValue("id"))
	if alertID == "" {
		writeError(w, http.StatusBadRequest, "alert id is required")
		return
	}
	var req fleetAlertAckRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	acknowledged := true
	if req.Acknowledged != nil {
		acknowledged = *req.Acknowledged
	}
	alerts := buildFleetAlerts(s.systemInventory(r.Context()), s.runner.GetSystemSettings().Thresholds)
	var current *FleetAlert
	for i := range alerts {
		if alerts[i].ID == alertID {
			current = &alerts[i]
			break
		}
	}
	if current == nil {
		writeError(w, http.StatusNotFound, "fleet alert not found")
		return
	}
	if !acknowledged {
		if err := s.store.DeleteFleetAlertAck(alertID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clear fleet alert acknowledgement")
			return
		}
		s.recordAuditEvent(r, "fleet.alert.unack", "success", "fleet_alert", alertID, "", map[string]any{"alert_type": current.Type, "target": current.Target})
		s.broadcastFleetEvent("fleet_alert_unacknowledged", map[string]any{"alert_id": alertID, "alert_type": current.Type, "target": current.Target})
		s.dispatchFleetAlertNotification("fleet_alert_unacknowledged", *current)
		current.Acknowledged = false
		writeJSON(w, http.StatusOK, current)
		return
	}
	actorName, actorSubject, _ := s.resolveAuditActor(r)
	now := time.Now().UTC()
	ack := &storage.FleetAlertAck{
		ID:                        alertID,
		AlertType:                 current.Type,
		Target:                    current.Target,
		Region:                    current.Region,
		Severity:                  current.Severity,
		Summary:                   current.Summary,
		Note:                      strings.TrimSpace(req.Note),
		AcknowledgedBySubject:     actorSubject,
		AcknowledgedByDisplayName: actorName,
		AcknowledgedAt:            now,
	}
	if err := s.store.SaveFleetAlertAck(ack); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to acknowledge fleet alert")
		return
	}
	applyFleetAlertAck(current, ack)
	s.recordAuditEvent(r, "fleet.alert.ack", "success", "fleet_alert", alertID, "", map[string]any{"alert_type": current.Type, "target": current.Target})
	s.broadcastFleetEvent("fleet_alert_acknowledged", map[string]any{"alert_id": alertID, "alert_type": current.Type, "target": current.Target, "severity": current.Severity})
	s.dispatchFleetAlertNotification("fleet_alert_acknowledged", *current)
	writeJSON(w, http.StatusOK, current)
}

// handleGetFleetProvisioner reports the active generator provisioner — whether
// generators are a static fleet or can be created on demand. The seam lets a
// cloud provisioner (AWS/Azure/GCP/K8s) plug in via RegisterGeneratorProvisioner
// and APEXLOAD_GENERATOR_PROVISIONER.
func (s *Server) handleGetFleetProvisioner(w http.ResponseWriter, r *http.Request) {
	info := resolveGeneratorProvisioner(s.systemInventory).Describe()
	// When a real fleet scaler is active (in-cluster Kubernetes), surface it —
	// keeping the inventory-derived SupportedRegions.
	if s.fleetScaler != nil && s.fleetScaler.CanProvision() {
		backend := s.fleetScaler.Describe()
		info.Name = backend.Name
		info.CanProvision = true
		info.Description = backend.Description
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleGetFleetCapacity(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	region := strings.TrimSpace(r.URL.Query().Get("region"))
	protocolName := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol")))
	capacity := buildFleetCapacity(s.systemInventory(r.Context()), s.runner.GetSystemSettings(), s.runner.ListTests(), projectID, region, protocolName)
	writeJSON(w, http.StatusOK, capacity)
}

func (s *Server) handleGetFleetLoadZones(w http.ResponseWriter, r *http.Request) {
	protocolName := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("protocol")))
	zones := buildFleetLoadZones(s.systemInventory(r.Context()), s.runner.ListTests(), protocolName)
	writeJSON(w, http.StatusOK, FleetLoadZonesResponse{LoadZones: zones, Count: len(zones)})
}

func (s *Server) handleSetGeneratorDrainState(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req GeneratorDrainRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Target = strings.TrimSpace(req.Target)
	req.GeneratorID = strings.TrimSpace(req.GeneratorID)
	req.DrainState = strings.TrimSpace(req.DrainState)
	if req.Target == "" && req.GeneratorID == "" {
		writeError(w, http.StatusBadRequest, "target or generator_id is required")
		return
	}
	if req.DrainState == "" {
		req.DrainState = "active"
	}

	generatorID, target, err := s.setGeneratorLifecycleState(req.GeneratorID, req.Target, req.DrainState)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.syncGeneratorMetadataCordon(generatorID, target, req.DrainState != "active"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist generator cordon metadata: "+err.Error())
		return
	}

	auditTarget := firstNonEmpty(generatorID, target, req.Target)
	s.recordAuditEvent(r, "generator.drain_state.update", "success", "generator", auditTarget, "", map[string]any{"drain_state": req.DrainState, "target": target, "generator_id": generatorID})
	s.broadcastFleetEvent("generator_drain_state_updated", map[string]any{"target": target, "generator_id": generatorID, "drain_state": req.DrainState})
	writeJSON(w, http.StatusOK, map[string]string{
		"target":       target,
		"generator_id": generatorID,
		"drain_state":  req.DrainState,
	})
}

func (s *Server) saveGeneratorDrainStates(states map[string]string) error {
	if s == nil || s.store == nil {
		return nil
	}
	payload, err := json.Marshal(states)
	if err != nil {
		return err
	}
	return s.store.SaveSetting(generatorDrainStatesStoreKey, string(payload))
}

func loadGeneratorDrainStates(store settingsPersistence) map[string]string {
	if store == nil {
		return nil
	}
	raw, err := store.GetSetting(generatorDrainStatesStoreKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	var states map[string]string
	if err := json.Unmarshal([]byte(raw), &states); err != nil {
		return nil
	}
	return states
}

func buildFleetAlerts(inventory SystemInventoryResponse, thresholds ThresholdSettings) []FleetAlert {
	alerts := make([]FleetAlert, 0)
	versionCounts := map[string]int{}
	for _, generator := range inventory.Generators {
		target := firstNonEmpty(generator.GeneratorID, generator.Target)
		status := strings.ToLower(strings.TrimSpace(generator.Status))
		drainState := strings.ToLower(strings.TrimSpace(generator.DrainState))
		capacityState := strings.ToLower(strings.TrimSpace(generator.CapacityState))
		compatibilityState := strings.ToLower(strings.TrimSpace(generator.CompatibilityState))
		version := strings.TrimSpace(generator.Version)
		if version != "" && version != "unknown" {
			versionCounts[version]++
		}
		if status != "" && status != "connected" && status != "healthy" && status != "ready" {
			alerts = append(alerts, FleetAlert{
				Type:     "unreachable",
				Target:   target,
				Region:   generator.Region,
				Severity: "danger",
				Summary:  fmt.Sprintf("%s is %s; placement cannot use this generator.", target, status),
			})
		}
		if drainState == "" {
			drainState = "active"
		}
		if drainState != "active" {
			alerts = append(alerts, FleetAlert{
				Type:     "drain_state",
				Target:   target,
				Region:   generator.Region,
				Severity: "warning",
				Summary:  fmt.Sprintf("%s is %s; new placements are excluded.", target, drainState),
			})
		}
		if fleetGeneratorSaturated(generator, capacityState, thresholds) {
			alerts = append(alerts, FleetAlert{
				Type:     "saturation",
				Target:   target,
				Region:   generator.Region,
				Severity: "danger",
				Summary:  fmt.Sprintf("%s is saturated with %d active VUsers.", target, generator.ActiveVUsers),
			})
		} else if fleetGeneratorConstrained(generator, capacityState, thresholds) {
			alerts = append(alerts, FleetAlert{
				Type:     "saturation",
				Target:   target,
				Region:   generator.Region,
				Severity: "warning",
				Summary:  fmt.Sprintf("%s capacity is constrained: %d slots remain.", target, generator.AvailableCapacity),
			})
		}
		if compatibilityState != "" && compatibilityState != "compatible" {
			reason := compatibilityState
			if len(generator.CompatibilityIssues) > 0 && strings.TrimSpace(generator.CompatibilityIssues[0]) != "" {
				reason = strings.TrimSpace(generator.CompatibilityIssues[0])
			}
			alerts = append(alerts, FleetAlert{
				Type:     "compatibility",
				Target:   target,
				Region:   generator.Region,
				Severity: "danger",
				Summary:  fmt.Sprintf("%s is not placement-compatible: %s.", target, reason),
			})
		}
	}
	controllerMajor := leadingMajorVersion(inventory.Controller.Version)
	for version, count := range versionCounts {
		if controllerMajor == "" || leadingMajorVersion(version) == controllerMajor {
			continue
		}
		alerts = append(alerts, FleetAlert{
			Type:     "version_mismatch",
			Target:   version,
			Severity: "warning",
			Summary:  fmt.Sprintf("%d generator%s report version %s, which differs from controller %s.", count, pluralSuffix(count), version, inventory.Controller.Version),
		})
	}
	for i := range alerts {
		alerts[i].ID = stableFleetAlertID(alerts[i])
	}
	return alerts
}

func fleetGeneratorSaturated(generator GeneratorInventoryEntry, capacityState string, thresholds ThresholdSettings) bool {
	if thresholds.FleetSaturatedAvailableSlots != nil && int(generator.AvailableCapacity) <= *thresholds.FleetSaturatedAvailableSlots {
		return true
	}
	return capacityState == "saturated" || generator.AvailableCapacity <= 0
}

func fleetGeneratorConstrained(generator GeneratorInventoryEntry, capacityState string, thresholds ThresholdSettings) bool {
	if thresholds.FleetConstrainedAvailableSlots != nil && int(generator.AvailableCapacity) <= *thresholds.FleetConstrainedAvailableSlots {
		return true
	}
	return capacityState == "constrained"
}

func stableFleetAlertID(alert FleetAlert) string {
	sum := sha1.Sum([]byte(strings.Join([]string{
		strings.ToLower(strings.TrimSpace(alert.Type)),
		strings.ToLower(strings.TrimSpace(alert.Target)),
		strings.ToLower(strings.TrimSpace(alert.Region)),
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (s *Server) applyFleetAlertAcknowledgements(alerts []FleetAlert) {
	if s == nil || s.store == nil || len(alerts) == 0 {
		return
	}
	acks, err := s.store.ListFleetAlertAcks()
	if err != nil {
		return
	}
	byID := make(map[string]*storage.FleetAlertAck, len(acks))
	for _, ack := range acks {
		if ack != nil && strings.TrimSpace(ack.ID) != "" {
			byID[ack.ID] = ack
		}
	}
	for i := range alerts {
		if ack := byID[alerts[i].ID]; ack != nil {
			applyFleetAlertAck(&alerts[i], ack)
		}
	}
}

func applyFleetAlertAck(alert *FleetAlert, ack *storage.FleetAlertAck) {
	if alert == nil || ack == nil {
		return
	}
	alert.Acknowledged = true
	ackedAt := ack.AcknowledgedAt
	alert.AcknowledgedAt = &ackedAt
	alert.AcknowledgedBySubject = ack.AcknowledgedBySubject
	alert.AcknowledgedByDisplayName = ack.AcknowledgedByDisplayName
	alert.AcknowledgementNote = ack.Note
}

func countUnacknowledgedFleetAlerts(alerts []FleetAlert) int {
	count := 0
	for _, alert := range alerts {
		if !alert.Acknowledged {
			count++
		}
	}
	return count
}

type fleetAlertNotificationState struct {
	Routed map[string]string `json:"routed,omitempty"`
}

func (s *Server) routeFleetAlertNotifications(alerts []FleetAlert) {
	if s == nil || s.store == nil || s.runner == nil {
		return
	}
	state := loadFleetAlertNotificationState(s.store)
	if state.Routed == nil {
		state.Routed = map[string]string{}
	}
	activeIDs := make(map[string]struct{}, len(alerts))
	changed := false
	for _, alert := range alerts {
		if strings.TrimSpace(alert.ID) == "" {
			continue
		}
		activeIDs[alert.ID] = struct{}{}
	}
	for id := range state.Routed {
		if _, ok := activeIDs[id]; !ok {
			delete(state.Routed, id)
			changed = true
		}
	}
	settings := s.runner.currentNotificationSettings()
	if !fleetNotificationConfigured(settings, "fleet_alert") {
		if changed {
			if encoded, err := json.Marshal(state); err == nil {
				if err := s.store.SaveSetting(fleetAlertNotificationStateStoreKey, string(encoded)); err != nil {
					slog.Warn("failed to persist fleet alert notification state", "error", err)
				}
			}
		}
		return
	}
	now := time.Now().UTC()
	for _, alert := range alerts {
		if strings.TrimSpace(alert.ID) == "" || alert.Acknowledged {
			continue
		}
		if _, alreadyRouted := state.Routed[alert.ID]; alreadyRouted {
			continue
		}
		s.dispatchFleetAlertNotification("fleet_alert", alert)
		state.Routed[alert.ID] = now.Format(time.RFC3339Nano)
		changed = true
	}
	if changed {
		if encoded, err := json.Marshal(state); err == nil {
			if err := s.store.SaveSetting(fleetAlertNotificationStateStoreKey, string(encoded)); err != nil {
				slog.Warn("failed to persist fleet alert notification state", "error", err)
			}
		}
	}
}

func loadFleetAlertNotificationState(store settingsPersistence) fleetAlertNotificationState {
	if store == nil {
		return fleetAlertNotificationState{}
	}
	raw, err := store.GetSetting(fleetAlertNotificationStateStoreKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return fleetAlertNotificationState{}
	}
	var state fleetAlertNotificationState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return fleetAlertNotificationState{}
	}
	return state
}

func fleetNotificationConfigured(settings NotificationsSettings, eventType string) bool {
	for _, webhook := range settings.Webhooks {
		if webhook.Enabled && notificationWebhookMatchesEvent(webhook, eventType) {
			return true
		}
	}
	return false
}

func (s *Server) dispatchFleetAlertNotification(eventType string, alert FleetAlert) {
	if s == nil || s.runner == nil || s.runner.notifier == nil {
		return
	}
	s.runner.notifier.Dispatch(s.runner.currentNotificationSettings(), NotificationEvent{
		Type:       eventType,
		TestID:     alert.ID,
		TestName:   firstNonEmpty(alert.Target, "Fleet"),
		Status:     firstNonEmpty(alert.Type, "fleet_alert"),
		Severity:   alert.Severity,
		Summary:    alert.Summary,
		Conditions: []string{firstNonEmpty(alert.Region, "unscoped")},
		Timestamp:  time.Now().UTC(),
	})
}

func buildFleetCapacity(inventory SystemInventoryResponse, settings SystemSettings, tests []*ManagedTest, projectID string, region string, protocolName string) FleetCapacityResponse {
	out := FleetCapacityResponse{
		ProjectID: projectID,
		Region:    region,
		Protocol:  protocolName,
	}
	featureSet := map[string]struct{}{}
	for _, generator := range inventory.Generators {
		if region != "" && !strings.EqualFold(strings.TrimSpace(generator.Region), region) {
			continue
		}
		out.TotalGenerators++
		out.ActiveSlots += int(generator.ActiveVUsers)
		out.MaxSlots += int(generator.MaxVUsers)
		features := generator.CompatibleFeatures
		if len(features) == 0 {
			features = generator.SupportedProtocols
		}
		for _, feature := range features {
			feature = strings.TrimSpace(feature)
			if feature != "" {
				featureSet[feature] = struct{}{}
			}
		}
		reasons := generatorCapacityRejectionReasons(generator, protocolName)
		if len(reasons) > 0 {
			out.RejectionReasons = append(out.RejectionReasons, reasons...)
			continue
		}
		out.EligibleGenerators++
		out.AvailableSlots += int(generator.AvailableCapacity)
	}
	if out.TotalGenerators == 0 {
		if region != "" {
			out.RejectionReasons = append(out.RejectionReasons, "No generators matched region "+region+".")
		} else {
			out.RejectionReasons = append(out.RejectionReasons, "No generator inventory is available.")
		}
	}
	if protocolName != "" && out.TotalGenerators > 0 && out.EligibleGenerators == 0 {
		out.RejectionReasons = append(out.RejectionReasons, "No eligible generators support protocol "+protocolName+".")
	}
	if projectID != "" {
		_, projectReserved := capacityReservationTotals(settings, projectID)
		out.ProjectID = normalizeProjectID(projectID)
		out.ReservedGenerators = projectReserved
		out.ReservedSlots = projectReserved * settings.CapacityPlanning.Generator.MaxVUsers
	}
	out.ProjectSlots = buildFleetProjectSlotAccounting(settings, tests)
	if out.ProjectID != "" {
		for _, slots := range out.ProjectSlots {
			if slots.ProjectID == out.ProjectID {
				out.ProjectActiveSlots = slots.ActiveSlots
				out.ProjectPendingSlots = slots.PendingSlots
				break
			}
		}
	}
	out.CompatibleFeatures = sortedStringSet(featureSet)
	out.RejectionReasons = sortedUniqueStrings(out.RejectionReasons)
	return out
}

type zoneMetricAccumulator struct {
	weight   int
	errorPct float64
	p95Ms    float64
	p99Ms    float64
	active   int
	queued   int
	rejected int
}

func buildFleetLoadZones(inventory SystemInventoryResponse, tests []*ManagedTest, protocolName string) []FleetLoadZone {
	byID := map[string]*FleetLoadZone{}
	protocols := map[string]map[string]struct{}{}
	for _, generator := range inventory.Generators {
		region := firstNonEmpty(strings.TrimSpace(generator.Region), "default")
		zoneID := loadZoneID(region)
		zone := byID[zoneID]
		if zone == nil {
			zone = &FleetLoadZone{
				ID:                  zoneID,
				Name:                loadZoneName(region),
				Region:              region,
				CostLabel:           "self-hosted",
				Tags:                []string{"self-hosted", "region:" + region},
				BenchmarkEvidenceID: "protocol-density-envelope:" + firstNonEmpty(protocolName, "mixed"),
				BenchmarkEvidence:   []string{"docs/ENTERPRISE_AUTOSCALING_SIGNALS.md#protocol-density-envelopes", "/api/v1/fleet/capacity", "/api/v1/system/inventory"},
			}
			byID[zoneID] = zone
			protocols[zoneID] = map[string]struct{}{}
		}
		zone.TotalGenerators++
		zone.ActiveSlots += int(generator.ActiveVUsers)
		zone.AvailableSlots += int(generator.AvailableCapacity)
		zone.MaxSlots += int(generator.MaxVUsers)
		for _, protocol := range generator.SupportedProtocols {
			protocol = strings.TrimSpace(protocol)
			if protocol != "" {
				protocols[zoneID][protocol] = struct{}{}
			}
		}
		reasons := generatorCapacityRejectionReasons(generator, protocolName)
		if len(reasons) > 0 {
			zone.RejectionReasons = append(zone.RejectionReasons, reasons...)
			continue
		}
		zone.EligibleGenerators++
	}

	accumulators := map[string]*zoneMetricAccumulator{}
	for _, test := range tests {
		if test == nil {
			continue
		}
		demand := capacityDemandByRegion(test)
		for region, vusers := range demand {
			if vusers <= 0 {
				continue
			}
			zoneID := loadZoneID(region)
			if _, ok := byID[zoneID]; !ok {
				continue
			}
			acc := accumulators[zoneID]
			if acc == nil {
				acc = &zoneMetricAccumulator{}
				accumulators[zoneID] = acc
			}
			switch test.Status {
			case "RUNNING", "INITIALIZING", "PAUSED":
				acc.active += vusers
				if test.LiveMetrics != nil {
					acc.weight += vusers
					acc.errorPct += test.LiveMetrics.ErrorPct * float64(vusers)
					acc.p95Ms += test.LiveMetrics.P95Ms * float64(vusers)
					acc.p99Ms += test.LiveMetrics.P99Ms * float64(vusers)
				}
			case "QUEUED", "PENDING", "RESERVED":
				acc.queued += vusers
			}
			if test.CapacityAdvisory != nil && test.CapacityAdvisory.RequiresOverride {
				acc.rejected += vusers
			}
		}
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]FleetLoadZone, 0, len(ids))
	for _, id := range ids {
		zone := byID[id]
		zone.SupportedProtocols = sortedStringSet(protocols[id])
		zone.RejectionReasons = sortedUniqueStrings(zone.RejectionReasons)
		zone.CapacityState = aggregateZoneCapacityState(*zone)
		if acc := accumulators[id]; acc != nil {
			zone.ActiveVUsers = acc.active
			zone.QueuedVUsers = acc.queued
			zone.RejectedVUsers = acc.rejected
			if acc.weight > 0 {
				zone.ErrorPct = acc.errorPct / float64(acc.weight)
				zone.P95Ms = acc.p95Ms / float64(acc.weight)
				zone.P99Ms = acc.p99Ms / float64(acc.weight)
			}
		}
		out = append(out, *zone)
	}
	return out
}

func aggregateZoneCapacityState(zone FleetLoadZone) string {
	if zone.TotalGenerators == 0 || zone.EligibleGenerators == 0 {
		return "unavailable"
	}
	if zone.AvailableSlots <= 0 {
		return "saturated"
	}
	if zone.MaxSlots > 0 && zone.AvailableSlots <= zone.MaxSlots/5 {
		return "constrained"
	}
	return "available"
}

func loadZoneID(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		return "default"
	}
	return region
}

func loadZoneName(region string) string {
	region = strings.TrimSpace(region)
	if region == "" || region == "default" {
		return "Default Load Zone"
	}
	parts := strings.FieldsFunc(region, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + strings.ToLower(parts[i][1:])
	}
	return strings.Join(parts, " ") + " Load Zone"
}

func buildFleetProjectSlotAccounting(settings SystemSettings, tests []*ManagedTest) []FleetProjectSlotAccounting {
	byProject := map[string]*FleetProjectSlotAccounting{}
	for _, reservation := range settings.CapacityPlanning.Reservations {
		// Pool-scoped reservations draw from a fleet pool's member envelope, not the
		// global fleet, and are excluded from the global reservation math everywhere
		// else (capacityReservationTotalsAt, validateSystemSettings); excluding them
		// here keeps this per-project view consistent with that envelope split.
		if strings.TrimSpace(reservation.PoolID) != "" {
			continue
		}
		projectID := normalizeProjectID(reservation.ProjectID)
		if projectID == "" {
			continue
		}
		entry := fleetProjectSlotEntry(byProject, projectID)
		entry.ReservedGenerators += reservation.ReservedGenerators
		entry.ReservedSlots += reservation.ReservedGenerators * settings.CapacityPlanning.Generator.MaxVUsers
	}
	for _, test := range tests {
		if test == nil {
			continue
		}
		projectID := normalizeProjectID(test.ProjectID)
		entry := fleetProjectSlotEntry(byProject, projectID)
		switch test.Status {
		case "RUNNING", "INITIALIZING", "PAUSED":
			entry.ActiveSlots += test.TotalVUsers
			entry.RunningTests++
		case "PENDING", "RESERVED":
			entry.PendingSlots += test.TotalVUsers
			entry.PendingTests++
		}
	}
	out := make([]FleetProjectSlotAccounting, 0, len(byProject))
	for _, entry := range byProject {
		if entry.ReservedGenerators > 0 {
			entry.FairShareTier = "reserved"
		} else {
			entry.FairShareTier = "shared"
		}
		if entry.ActiveSlots == 0 && entry.PendingSlots == 0 && entry.ReservedGenerators == 0 {
			continue
		}
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PendingSlots != out[j].PendingSlots {
			return out[i].PendingSlots > out[j].PendingSlots
		}
		if out[i].ActiveSlots != out[j].ActiveSlots {
			return out[i].ActiveSlots > out[j].ActiveSlots
		}
		return out[i].ProjectID < out[j].ProjectID
	})
	return out
}

func fleetProjectSlotEntry(values map[string]*FleetProjectSlotAccounting, projectID string) *FleetProjectSlotAccounting {
	projectID = normalizeProjectID(projectID)
	if projectID == "" {
		projectID = storage.DefaultProjectID
	}
	if existing, ok := values[projectID]; ok {
		return existing
	}
	entry := &FleetProjectSlotAccounting{ProjectID: projectID}
	values[projectID] = entry
	return entry
}

func generatorCapacityRejectionReasons(generator GeneratorInventoryEntry, protocolName string) []string {
	reasons := make([]string, 0, 3)
	target := firstNonEmpty(generator.GeneratorID, generator.Target)
	status := strings.ToLower(strings.TrimSpace(generator.Status))
	if status != "" && status != "connected" && status != "healthy" && status != "ready" {
		reasons = append(reasons, target+" is "+status+".")
	}
	drainState := strings.ToLower(strings.TrimSpace(generator.DrainState))
	if drainState == "" {
		drainState = "active"
	}
	if drainState != "active" {
		reasons = append(reasons, target+" is "+drainState+".")
	}
	compatibilityState := strings.ToLower(strings.TrimSpace(generator.CompatibilityState))
	if compatibilityState != "" && compatibilityState != "compatible" {
		reason := compatibilityState
		if len(generator.CompatibilityIssues) > 0 && strings.TrimSpace(generator.CompatibilityIssues[0]) != "" {
			reason = strings.TrimSpace(generator.CompatibilityIssues[0])
		}
		reasons = append(reasons, target+" is incompatible: "+reason+".")
	}
	if protocolName != "" && !generatorSupportsProtocol(generator, protocolName) {
		reasons = append(reasons, target+" does not support protocol "+protocolName+".")
	}
	if generator.AvailableCapacity <= 0 {
		reasons = append(reasons, target+" has no available slots.")
	}
	return reasons
}

func generatorSupportsProtocol(generator GeneratorInventoryEntry, protocolName string) bool {
	protocolName = strings.ToLower(strings.TrimSpace(protocolName))
	if protocolName == "" {
		return true
	}
	for _, supported := range generator.SupportedProtocols {
		if strings.EqualFold(strings.TrimSpace(supported), protocolName) {
			return true
		}
	}
	return false
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedUniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func leadingMajorVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "unknown" {
		return ""
	}
	if dot := strings.Index(version, "."); dot >= 0 {
		version = version[:dot]
	}
	version = strings.TrimLeft(version, "vV")
	if version == "" {
		return ""
	}
	for _, r := range version {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return version
}
