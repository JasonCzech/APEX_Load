package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

const (
	restartTargetController = "controller"
	restartTargetGenerator  = "generator"
	restartScopePod         = "pod"
	restartScopeWorkload    = "workload"
)

type K8sRestartSettings struct {
	Enabled              bool   `json:"enabled"`
	Namespace            string `json:"namespace,omitempty"`
	ControllerDeployment string `json:"controller_deployment,omitempty"`
	GeneratorStatefulSet string `json:"generator_stateful_set,omitempty"`
	GeneratorSelector    string `json:"generator_selector,omitempty"`
	AllowForce           bool   `json:"allow_force"`
}

type SystemRestartRequest struct {
	TargetType   string `json:"target_type"`
	Scope        string `json:"scope"`
	Target       string `json:"target"`
	Reason       string `json:"reason"`
	Confirmation string `json:"confirmation"`
	Force        bool   `json:"force"`
}

type SystemRestartCapabilities struct {
	Enabled              bool   `json:"enabled"`
	DisabledReason       string `json:"disabled_reason,omitempty"`
	ExecutionMode        string `json:"execution_mode"`
	Namespace            string `json:"namespace,omitempty"`
	ControllerDeployment string `json:"controller_deployment,omitempty"`
	GeneratorStatefulSet string `json:"generator_stateful_set,omitempty"`
	AllowForce           bool   `json:"allow_force"`
	Supports             struct {
		ControllerWorkload bool `json:"controller_workload"`
		GeneratorWorkload  bool `json:"generator_workload"`
		GeneratorPod       bool `json:"generator_pod"`
	} `json:"supports"`
}

type SystemRestartResult struct {
	Status         string   `json:"status"`
	Message        string   `json:"message"`
	TargetType     string   `json:"target_type"`
	Scope          string   `json:"scope"`
	Target         string   `json:"target"`
	Namespace      string   `json:"namespace,omitempty"`
	Workload       string   `json:"workload,omitempty"`
	PodName        string   `json:"pod_name,omitempty"`
	ActiveVUsers   int32    `json:"active_vusers,omitempty"`
	ActiveRunCount int      `json:"active_run_count,omitempty"`
	Reachable      *bool    `json:"reachable,omitempty"`
	Forced         bool     `json:"forced,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	DisabledReason string   `json:"disabled_reason,omitempty"`
}

func normalizeK8sRestartSettings(settings K8sRestartSettings) K8sRestartSettings {
	settings.Namespace = strings.TrimSpace(settings.Namespace)
	settings.ControllerDeployment = firstNonEmpty(settings.ControllerDeployment, "apexload-controller")
	settings.GeneratorStatefulSet = firstNonEmpty(settings.GeneratorStatefulSet, "apexload-generator")
	settings.GeneratorSelector = firstNonEmpty(settings.GeneratorSelector, "app.kubernetes.io/name=apexload-generator")
	return settings
}

func (s *Server) handleGetSystemRestartCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.systemRestartCapabilities(r))
}

func (s *Server) systemRestartCapabilities(r *http.Request) SystemRestartCapabilities {
	inventory := s.systemInventory(r.Context())
	caps := SystemRestartCapabilities{
		Enabled:              false,
		ExecutionMode:        inventory.ExecutionMode,
		Namespace:            s.k8sRestart.Namespace,
		ControllerDeployment: s.k8sRestart.ControllerDeployment,
		GeneratorStatefulSet: s.k8sRestart.GeneratorStatefulSet,
		AllowForce:           s.k8sRestart.AllowForce,
	}
	caps.Supports.ControllerWorkload = true
	caps.Supports.GeneratorWorkload = inventory.ExecutionMode == "distributed"
	caps.Supports.GeneratorPod = inventory.ExecutionMode == "distributed"

	switch {
	case !s.k8sRestart.Enabled:
		caps.DisabledReason = "Kubernetes restart controls are disabled."
	case s.kubeRestarter == nil:
		caps.DisabledReason = "Kubernetes restart client is unavailable."
	case s.k8sRestart.Namespace == "":
		caps.DisabledReason = "Kubernetes namespace is not configured."
	default:
		caps.Enabled = true
	}
	return caps
}

func (s *Server) handleSystemRestart(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	caps := s.systemRestartCapabilities(r)
	if !caps.Enabled {
		writeJSON(w, http.StatusConflict, SystemRestartResult{
			Status:         "disabled",
			Message:        caps.DisabledReason,
			DisabledReason: caps.DisabledReason,
		})
		return
	}

	var req SystemRestartRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req = normalizeSystemRestartRequest(req)
	if err := validateSystemRestartRequest(req, s.k8sRestart.AllowForce); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	key := req.TargetType + ":" + req.Scope + ":" + req.Target
	release, ok := s.beginRestart(key)
	if !ok {
		result := SystemRestartResult{
			Status:     "blocked",
			Message:    "a restart is already in progress for this target",
			TargetType: req.TargetType,
			Scope:      req.Scope,
			Target:     req.Target,
			Namespace:  s.k8sRestart.Namespace,
			Forced:     req.Force,
		}
		s.auditSystemRestart(r, req, "blocked", result, nil)
		writeJSON(w, http.StatusConflict, result)
		return
	}
	defer release()

	var result SystemRestartResult
	var err error
	switch {
	case req.TargetType == restartTargetGenerator && req.Scope == restartScopePod:
		result, err = s.restartGeneratorPod(r, req)
	case req.TargetType == restartTargetGenerator && req.Scope == restartScopeWorkload:
		result, err = s.restartGeneratorWorkload(r, req)
	case req.TargetType == restartTargetController && req.Scope == restartScopeWorkload:
		result, err = s.restartControllerWorkload(r, req)
	default:
		err = fmt.Errorf("unsupported restart target")
	}
	if err != nil {
		var conflict restartConflictError
		if errors.As(err, &conflict) {
			writeJSON(w, http.StatusConflict, conflict.result)
			return
		}
		var notFound restartNotFoundError
		if errors.As(err, &notFound) {
			writeJSON(w, http.StatusNotFound, result)
			return
		}
		s.auditSystemRestart(r, req, "failed", result, map[string]any{"error": err.Error()})
		writeJSON(w, http.StatusInternalServerError, SystemRestartResult{
			Status:     "failed",
			Message:    err.Error(),
			TargetType: req.TargetType,
			Scope:      req.Scope,
			Target:     req.Target,
			Namespace:  s.k8sRestart.Namespace,
			Forced:     req.Force,
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func normalizeSystemRestartRequest(req SystemRestartRequest) SystemRestartRequest {
	req.TargetType = strings.ToLower(strings.TrimSpace(req.TargetType))
	req.Scope = strings.ToLower(strings.TrimSpace(req.Scope))
	req.Target = strings.TrimSpace(req.Target)
	req.Reason = strings.TrimSpace(req.Reason)
	req.Confirmation = strings.TrimSpace(req.Confirmation)
	return req
}

func validateSystemRestartRequest(req SystemRestartRequest, allowForce bool) error {
	switch req.TargetType {
	case restartTargetController, restartTargetGenerator:
	default:
		return fmt.Errorf("target_type must be controller or generator")
	}
	switch req.Scope {
	case restartScopePod, restartScopeWorkload:
	default:
		return fmt.Errorf("scope must be pod or workload")
	}
	if req.Target == "" {
		return fmt.Errorf("target is required")
	}
	if req.Reason == "" {
		return fmt.Errorf("reason is required")
	}
	if req.Confirmation != req.Target {
		return fmt.Errorf("confirmation must match target")
	}
	if req.TargetType == restartTargetController && req.Scope == restartScopePod {
		return fmt.Errorf("controller pod restart is not supported; restart the controller workload")
	}
	if req.Force && !allowForce {
		return fmt.Errorf("force restarts are disabled by operator policy")
	}
	return nil
}

func (s *Server) beginRestart(key string) (func(), bool) {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	if s.restartInFlight == nil {
		s.restartInFlight = make(map[string]struct{})
	}
	if _, exists := s.restartInFlight[key]; exists {
		return nil, false
	}
	s.restartInFlight[key] = struct{}{}
	return func() {
		s.restartMu.Lock()
		delete(s.restartInFlight, key)
		s.restartMu.Unlock()
	}, true
}

func (s *Server) restartGeneratorPod(r *http.Request, req SystemRestartRequest) (SystemRestartResult, error) {
	inventory := s.systemInventory(r.Context())
	if inventory.ExecutionMode != "distributed" {
		result := baseRestartResult(req, s.k8sRestart.Namespace)
		result.Status = "blocked"
		result.Message = "generator pod restart requires distributed execution"
		s.auditSystemRestart(r, req, "blocked", result, nil)
		return result, writeConflictResult(result)
	}
	entry, ok := findGeneratorInventoryEntry(inventory.Generators, req.Target)
	if !ok {
		result := baseRestartResult(req, s.k8sRestart.Namespace)
		result.Status = "not_found"
		result.Message = fmt.Sprintf("generator target %q was not found in inventory", req.Target)
		s.auditSystemRestart(r, req, "failed", result, nil)
		return result, restartNotFoundError{message: result.Message}
	}
	reachable := generatorReachable(entry.Status)
	result := baseRestartResult(req, s.k8sRestart.Namespace)
	result.ActiveVUsers = entry.ActiveVUsers
	result.Reachable = &reachable
	result.Workload = s.k8sRestart.GeneratorStatefulSet

	if !req.Force {
		if !reachable {
			result.Status = "blocked"
			result.Message = "generator is unreachable; force is required to restart an unconfirmed pod"
			s.auditSystemRestart(r, req, "blocked", result, nil)
			return result, writeConflictResult(result)
		}
		if entry.ActiveVUsers > 0 {
			result.Status = "blocked"
			result.Message = fmt.Sprintf("generator has %d active VUsers; force is required", entry.ActiveVUsers)
			s.auditSystemRestart(r, req, "blocked", result, nil)
			return result, writeConflictResult(result)
		}
	}

	pod, err := s.resolveGeneratorPod(r, entry)
	if err != nil {
		result.Status = "blocked"
		result.Message = err.Error()
		s.auditSystemRestart(r, req, "blocked", result, nil)
		return result, writeConflictResult(result)
	}
	result.PodName = pod.Name
	s.bestEffortDrainGenerator(r, entry.Target)
	if err := s.kubeRestarter.DeletePod(r.Context(), s.k8sRestart.Namespace, pod.Name); err != nil {
		result.Status = "failed"
		result.Message = err.Error()
		return result, err
	}
	result.Status = "success"
	result.Message = "generator pod restart requested"
	result.Forced = req.Force
	result.Warnings = []string{"Generator drain intent is recorded by stable generator ID when the controller has observed a heartbeat for this pod."}
	s.auditSystemRestart(r, req, "success", result, nil)
	return result, nil
}

func (s *Server) restartGeneratorWorkload(r *http.Request, req SystemRestartRequest) (SystemRestartResult, error) {
	inventory := s.systemInventory(r.Context())
	result := baseRestartResult(req, s.k8sRestart.Namespace)
	result.Workload = s.k8sRestart.GeneratorStatefulSet
	activeRuns := activeRestartRuns(s.runner.listOperationalTestSnapshots())
	result.ActiveRunCount = len(activeRuns)
	for _, generator := range inventory.Generators {
		result.ActiveVUsers += generator.ActiveVUsers
	}
	if inventory.ExecutionMode != "distributed" {
		result.Status = "blocked"
		result.Message = "generator workload restart requires distributed execution"
		s.auditSystemRestart(r, req, "blocked", result, nil)
		return result, writeConflictResult(result)
	}
	if !req.Force && (result.ActiveVUsers > 0 || result.ActiveRunCount > 0) {
		result.Status = "blocked"
		result.Message = "generator workload has active VUsers or active runs; force is required"
		s.auditSystemRestart(r, req, "blocked", result, nil)
		return result, writeConflictResult(result)
	}
	if err := s.kubeRestarter.RolloutRestart(r.Context(), "statefulset", s.k8sRestart.Namespace, s.k8sRestart.GeneratorStatefulSet); err != nil {
		result.Status = "failed"
		result.Message = err.Error()
		return result, err
	}
	result.Status = "success"
	result.Message = "generator workload restart requested"
	result.Forced = req.Force
	s.auditSystemRestart(r, req, "success", result, nil)
	return result, nil
}

func (s *Server) restartControllerWorkload(r *http.Request, req SystemRestartRequest) (SystemRestartResult, error) {
	result := baseRestartResult(req, s.k8sRestart.Namespace)
	result.Workload = s.k8sRestart.ControllerDeployment
	activeRuns := activeRestartRuns(s.runner.listOperationalTestSnapshots())
	result.ActiveRunCount = len(activeRuns)
	if !req.Force && len(activeRuns) > 0 {
		result.Status = "blocked"
		result.Message = "controller restart would fail active runs; force is required"
		result.Warnings = []string{"Active runs will be marked FAILED if the controller restarts before they complete."}
		s.auditSystemRestart(r, req, "blocked", result, nil)
		return result, writeConflictResult(result)
	}
	if err := s.kubeRestarter.RolloutRestart(r.Context(), "deployment", s.k8sRestart.Namespace, s.k8sRestart.ControllerDeployment); err != nil {
		result.Status = "failed"
		result.Message = err.Error()
		return result, err
	}
	result.Status = "success"
	result.Message = "controller workload restart requested"
	result.Forced = req.Force
	if len(activeRuns) > 0 {
		result.Warnings = []string{"Active runs are expected to reconcile to FAILED after controller restart."}
	}
	s.auditSystemRestart(r, req, "success", result, nil)
	return result, nil
}

func baseRestartResult(req SystemRestartRequest, namespace string) SystemRestartResult {
	return SystemRestartResult{
		TargetType: req.TargetType,
		Scope:      req.Scope,
		Target:     req.Target,
		Namespace:  namespace,
		Forced:     req.Force,
	}
}

func writeConflictResult(result SystemRestartResult) error {
	return restartConflictError{result: result}
}

type restartConflictError struct {
	result SystemRestartResult
}

func (e restartConflictError) Error() string {
	if e.result.Message != "" {
		return e.result.Message
	}
	return "restart blocked"
}

type restartNotFoundError struct {
	message string
}

func (e restartNotFoundError) Error() string {
	if e.message != "" {
		return e.message
	}
	return "restart target not found"
}

func findGeneratorInventoryEntry(entries []GeneratorInventoryEntry, target string) (GeneratorInventoryEntry, bool) {
	for _, entry := range entries {
		if entry.Target == target || entry.GeneratorID == target || entry.PodName == target {
			return entry, true
		}
	}
	return GeneratorInventoryEntry{}, false
}

func generatorReachable(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "connected", "healthy", "ready":
		return true
	default:
		return false
	}
}

func (s *Server) resolveGeneratorPod(r *http.Request, entry GeneratorInventoryEntry) (KubePodRef, error) {
	host := hostOfTarget(entry.Target)
	if net.ParseIP(host) != nil {
		pod, err := s.kubeRestarter.PodByIP(r.Context(), s.k8sRestart.Namespace, s.k8sRestart.GeneratorSelector, host)
		if err == nil {
			return pod, nil
		}
	}
	pods, err := s.kubeRestarter.ListPods(r.Context(), s.k8sRestart.Namespace, s.k8sRestart.GeneratorSelector)
	if err != nil {
		return KubePodRef{}, fmt.Errorf("Kubernetes pod inventory is unavailable: %w", err)
	}
	pod := resolveGeneratorPodFromList(entry, pods, s.k8sRestart.GeneratorStatefulSet)
	if pod.Name == "" {
		return KubePodRef{}, fmt.Errorf("generator pod is not in the cluster inventory; it may already be gone")
	}
	return pod, nil
}

func resolveGeneratorPodFromList(entry GeneratorInventoryEntry, pods []KubePodRef, generatorStatefulSet string) KubePodRef {
	host := hostOfTarget(entry.Target)
	firstDNSLabel := strings.Split(host, ".")[0]
	for _, pod := range pods {
		if pod.Name == "" {
			continue
		}
		if entry.PodName != "" && pod.Name == entry.PodName {
			return pod
		}
		if entry.GeneratorID != "" && pod.Name == entry.GeneratorID {
			return pod
		}
		if pod.IP != "" && pod.IP == host {
			return pod
		}
		if firstDNSLabel != "" && pod.Name == firstDNSLabel {
			return pod
		}
		if generatorStatefulSet != "" && strings.HasPrefix(pod.Name, generatorStatefulSet+"-") && firstDNSLabel == pod.Name {
			return pod
		}
	}
	return KubePodRef{}
}

func generatorRestartability(entry GeneratorInventoryEntry, pod KubePodRef) (bool, string) {
	if pod.Name == "" {
		return false, "No Kubernetes pod matched this generator."
	}
	if entry.ActiveVUsers > 0 {
		return false, "Generator has active VUsers."
	}
	if !generatorReachable(entry.Status) {
		return false, "Generator is unreachable; force confirmation is required."
	}
	return true, ""
}

func hostOfTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(target); err == nil {
		return strings.Trim(host, "[]")
	}
	if strings.Count(target, ":") == 1 {
		parts := strings.SplitN(target, ":", 2)
		return strings.Trim(parts[0], "[]")
	}
	return strings.Trim(target, "[]")
}

func (s *Server) bestEffortDrainGenerator(r *http.Request, target string) {
	backend, ok := s.runner.getExecutionBackend().(drainableExecutionBackend)
	if !ok {
		return
	}
	if err := backend.SetGeneratorDrainState(target, "draining"); err != nil {
		return
	}
	if persistent, ok := backend.(persistentDrainBackend); ok {
		_ = s.saveGeneratorDrainStates(persistent.GeneratorDrainStates())
	}
}

func activeRestartRuns(tests []*ManagedTest) []*ManagedTest {
	out := make([]*ManagedTest, 0)
	for _, test := range tests {
		if test == nil {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(test.Status)) {
		case "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
			out = append(out, test)
		}
	}
	return out
}

func (s *Server) auditSystemRestart(r *http.Request, req SystemRestartRequest, status string, result SystemRestartResult, extra map[string]any) {
	details := map[string]any{
		"namespace":     result.Namespace,
		"workload":      result.Workload,
		"pod":           result.PodName,
		"scope":         result.Scope,
		"active_vusers": result.ActiveVUsers,
		"active_runs":   result.ActiveRunCount,
		"force":         req.Force,
		"reason":        req.Reason,
	}
	if result.Reachable != nil {
		details["reachable"] = *result.Reachable
	}
	for key, value := range extra {
		details[key] = value
	}
	action := "system.restart." + req.TargetType
	if req.TargetType == restartTargetGenerator {
		action += "." + req.Scope
	}
	s.recordAuditEvent(r, action, status, "system_restart", req.Target, "", details)
}
