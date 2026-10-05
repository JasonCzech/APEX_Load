package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/api"
)

const telemetryPolicyCLIResponseLimit = 8 << 20

// This read-only command emits a closed projection, never a raw run/report.
// Scope aliases may resolve to the server's canonical test identity.
func (c *ApiClient) readTelemetryPolicy(ctx context.Context, flags *flag.FlagSet, id, project, mode string, out io.Writer) error {
	if err := analysisCLIContextLive(ctx); err != nil {
		return err
	}
	if err := analysisCLIScopeAdmission(id, project); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("--id is required")
	}
	if mode != "live" && mode != "archive" {
		return errors.New("--mode must be live or archive")
	}
	invalidFlag := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "api", "project", "id", "mode":
		default:
			invalidFlag = true
		}
	})
	if invalidFlag {
		return errors.New("telemetry-policy supports only --api, --project, --id and --mode")
	}
	suffix, source := "/manifest", "manifest"
	if mode == "archive" {
		suffix, source = "/archive/report", "archive_report"
	}
	path := "/api/v1/tests/" + url.PathEscape(id) + suffix
	if project != "" {
		path += "?" + url.Values{"project_id": {project}}.Encode()
	}
	raw, err := c.telemetryPolicyRequest(ctx, path)
	if err != nil {
		return err
	}
	object, err := analysisCLIObject(raw, telemetryPolicyCLIResponseLimit, nil, 256)
	if err != nil {
		return errors.New("invalid telemetry policy response")
	}
	if err := telemetryPolicyCLINoAliases(object, []string{"telemetry_policy", "test_id", "identity", "manifest"}); err != nil {
		return err
	}
	identity := object
	var projectFields map[string]json.RawMessage
	if mode == "archive" {
		identity, err = analysisCLIObject(object["manifest"], telemetryPolicyCLIResponseLimit, nil, 256)
		if err != nil {
			return errors.New("archive policy response requires a manifest")
		}
		if err := telemetryPolicyCLINoAliases(identity, []string{"test_id", "provenance"}); err != nil {
			return err
		}
		if project != "" {
			projectFields, err = analysisCLIObject(identity["provenance"], telemetryPolicyCLIResponseLimit, nil, 256)
		}
	} else if project != "" {
		projectFields, err = analysisCLIObject(object["identity"], 64<<10, nil, 64)
	}
	if project != "" {
		if err != nil || telemetryPolicyCLINoAliases(projectFields, []string{"project_id"}) != nil {
			return errors.New("policy response project identity unavailable")
		}
		actual, textErr := analysisCLIText(projectFields["project_id"], 128, true)
		if textErr != nil || actual != project {
			return errors.New("policy response project identity does not match requested project")
		}
	}
	canonical, err := analysisCLIText(identity["test_id"], 128, true)
	if err != nil {
		return errors.New("policy response test identity unavailable")
	}
	evidence := &api.TelemetryPolicyEvidence{Availability: "unavailable", Reason: "immutable-policy-not-retained"}
	if value, exists := object["telemetry_policy"]; exists {
		evidence, err = decodeTelemetryPolicyCLI(value)
		if err != nil {
			return err
		}
	}
	if err := analysisCLIContextLive(ctx); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		SchemaVersion   string                       `json:"schema_version"`
		Source          string                       `json:"source"`
		TestID          string                       `json:"test_id"`
		TelemetryPolicy *api.TelemetryPolicyEvidence `json:"telemetry_policy"`
	}{"apexload.telemetry_policy_projection.v1", source, canonical, evidence})
}

func (c *ApiClient) telemetryPolicyRequest(ctx context.Context, path string) ([]byte, error) {
	req, err := c.newRequest(http.MethodGet, path, nil, "")
	if err != nil {
		return nil, errors.New("invalid telemetry policy controller URL")
	}
	client := *c.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("telemetry policy controller request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("telemetry policy controller returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, telemetryPolicyCLIResponseLimit+1))
	if err != nil || len(raw) > telemetryPolicyCLIResponseLimit {
		return nil, errors.New("telemetry policy response exceeds bounds or cannot be read")
	}
	return raw, nil
}

func telemetryPolicyCLINoAliases(object map[string]json.RawMessage, fields []string) error {
	for key := range object {
		for _, field := range fields {
			if key != field && strings.EqualFold(key, field) {
				return errors.New("ambiguous telemetry policy response field")
			}
		}
	}
	return nil
}

func decodeTelemetryPolicyCLI(raw []byte) (*api.TelemetryPolicyEvidence, error) {
	invalid := errors.New("invalid telemetry policy receipt")
	fields, err := analysisCLIObject(raw, 32<<10, []string{"availability", "reason", "policy"})
	if err != nil {
		return nil, invalid
	}
	availability, err := analysisCLIText(fields["availability"], 32, true)
	if err != nil {
		return nil, invalid
	}
	evidence := &api.TelemetryPolicyEvidence{Availability: availability}
	if reason, exists := fields["reason"]; exists {
		evidence.Reason, err = analysisCLIText(reason, 64, false)
		if err != nil {
			return nil, invalid
		}
	}
	if availability == "unavailable" {
		if evidence.Reason != "immutable-policy-not-retained" && evidence.Reason != "immutable-policy-invalid" {
			return nil, invalid
		}
		if policy, exists := fields["policy"]; exists && !bytes.Equal(bytes.TrimSpace(policy), []byte("null")) {
			return nil, invalid
		}
		return evidence, nil
	}
	if availability != "available" || evidence.Reason != "" {
		return nil, invalid
	}
	names := []string{"schema_version", "policy_id", "revision", "hash", "captured_at", "capture_boundary", "classification_algorithm", "expected_controller_publication_interval_ms", "cadence_scope", "sustained_breach_window_ms", "coverage_rule", "budgets"}
	policyFields, err := analysisCLIObject(fields["policy"], 32<<10, names)
	if err != nil || len(policyFields) != len(names) {
		return nil, invalid
	}
	for _, field := range []string{"schema_version", "policy_id", "hash", "captured_at", "capture_boundary", "classification_algorithm", "cadence_scope", "coverage_rule"} {
		if _, err := analysisCLIText(policyFields[field], 128, true); err != nil {
			return nil, invalid
		}
	}
	for _, field := range []string{"revision", "expected_controller_publication_interval_ms", "sustained_breach_window_ms"} {
		if _, err := strconv.ParseInt(string(policyFields[field]), 10, 64); err != nil {
			return nil, invalid
		}
	}
	d := json.NewDecoder(bytes.NewReader(policyFields["budgets"]))
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return nil, invalid
	}
	count := 0
	for d.More() {
		if count == 5 {
			return nil, invalid
		}
		count++
		var budgetRaw json.RawMessage
		if err := d.Decode(&budgetRaw); err != nil {
			return nil, invalid
		}
		budget, err := analysisCLIObject(budgetRaw, 1024, []string{"stage", "enabled", "limit_ms"})
		if err != nil || len(budget) != 3 {
			return nil, invalid
		}
		if _, err := analysisCLIText(budget["stage"], 64, true); err != nil {
			return nil, invalid
		}
		enabled := bytes.TrimSpace(budget["enabled"])
		if !bytes.Equal(enabled, []byte("true")) && !bytes.Equal(enabled, []byte("false")) {
			return nil, invalid
		}
		if _, err := strconv.ParseFloat(string(budget["limit_ms"]), 64); err != nil {
			return nil, invalid
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim(']') || count != 5 {
		return nil, invalid
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, invalid
	}
	var policy api.TelemetryBudgetPolicy
	if err := json.Unmarshal(fields["policy"], &policy); err != nil || !api.ValidateTelemetryBudgetPolicy(&policy) {
		return nil, invalid
	}
	evidence.Policy = &policy
	return evidence, nil
}
