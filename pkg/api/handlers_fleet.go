package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

const generatorDrainStatesStoreKey = "generator_drain_states"

type persistentDrainBackend interface {
	drainableExecutionBackend
	ApplyGeneratorDrainStates(states map[string]string)
	GeneratorDrainStates() map[string]string
}

type FleetAlertsResponse struct {
	Alerts []FleetAlert `json:"alerts"`
	Count  int          `json:"count"`
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
	Type     string `json:"type"`
	Target   string `json:"target"`
	Region   string `json:"region,omitempty"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

type GeneratorDrainRequest struct {
	Target     string `json:"target"`
	DrainState string `json:"drain_state"`
}

func (s *Server) handleGetFleetAlerts(w http.ResponseWriter, r *http.Request) {
	alerts := buildFleetAlerts(s.systemInventory(r.Context()))
	writeJSON(w, http.StatusOK, FleetAlertsResponse{Alerts: alerts, Count: len(alerts)})
}

// handleGetFleetProvisioner reports the active generator provisioner — whether
// generators are a static fleet or can be created on demand. The seam lets a
// cloud provisioner (AWS/Azure/GCP/K8s) plug in via RegisterGeneratorProvisioner
// and APEXLOAD_GENERATOR_PROVISIONER.
func (s *Server) handleGetFleetProvisioner(w http.ResponseWriter, r *http.Request) {
	provisioner := resolveGeneratorProvisioner(s.systemInventory)
	writeJSON(w, http.StatusOK, provisioner.Describe())
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
	var req GeneratorDrainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Target = strings.TrimSpace(req.Target)
	req.DrainState = strings.TrimSpace(req.DrainState)
	if req.Target == "" {
		writeError(w, http.StatusBadRequest, "target is required")
		return
	}
	if req.DrainState == "" {
		req.DrainState = "active"
	}

	backend, ok := s.runner.getExecutionBackend().(drainableExecutionBackend)
	if !ok {
		writeError(w, http.StatusBadRequest, "generator drain control requires distributed execution")
		return
	}
	if err := backend.SetGeneratorDrainState(req.Target, req.DrainState); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if persistent, ok := backend.(persistentDrainBackend); ok {
		if err := s.saveGeneratorDrainStates(persistent.GeneratorDrainStates()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist generator drain state: "+err.Error())
			return
		}
	}

	s.recordAuditEvent(r, "generator.drain_state.update", "success", "generator", req.Target, "", map[string]any{"drain_state": req.DrainState})
	writeJSON(w, http.StatusOK, map[string]string{
		"target":      req.Target,
		"drain_state": req.DrainState,
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

func buildFleetAlerts(inventory SystemInventoryResponse) []FleetAlert {
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
		if capacityState == "saturated" || generator.AvailableCapacity <= 0 {
			alerts = append(alerts, FleetAlert{
				Type:     "saturation",
				Target:   target,
				Region:   generator.Region,
				Severity: "danger",
				Summary:  fmt.Sprintf("%s is saturated with %d active VUsers.", target, generator.ActiveVUsers),
			})
		} else if capacityState == "constrained" {
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
	return alerts
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
