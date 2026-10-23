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
	"os"
	"strings"

	"github.com/google/uuid"
)

const telemetryPolicyChangeUsage = `Usage:
  apexctl analysis telemetry-policy-change --id RUN --policy ID|project_default [--interval SECONDS] [--expected-sequence N] [--request-id ID] [--api URL] [--api-key KEY|--bearer-token TOKEN]
Changes a running test's telemetry budget policy from now on (TEL-B): the
run's live telemetry is classified by the chosen project telemetry budget
policy (or project_default, the controller's threshold settings) from the
change, as a new append-only policy segment; the receipts before it are kept.
--interval changes a local run's publication cadence (whole seconds).
--expected-sequence is the run's latest segment as you last saw it (read now
when omitted); a change made meanwhile refuses this one. --request-id makes
a retry return the change it made (one is generated and printed otherwise).
Prints the new segment.`

func executeTelemetryPolicyChangeCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		_, err := fmt.Fprintln(out, telemetryPolicyChangeUsage)
		return err
	}
	flags := flag.NewFlagSet("apexctl analysis telemetry-policy-change", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("api", firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://127.0.0.1:8080"), "controller URL")
	id := flags.String("id", "", "run ID")
	policy := flags.String("policy", "", "telemetry budget policy ID, or project_default for the threshold settings")
	interval := flags.Int("interval", 0, "a local run's publication cadence in whole seconds")
	expected := flags.Int64("expected-sequence", 0, "the run's latest segment (read when omitted)")
	requestID := flags.String("request-id", "", "the change's identity, for a retry")
	apiKey := flags.String("api-key", os.Getenv("APEXLOAD_API_KEY"), "controller API key")
	bearer := flags.String("bearer-token", os.Getenv("APEXLOAD_BEARER_TOKEN"), "controller bearer token")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := fmt.Fprintln(out, telemetryPolicyChangeUsage)
			return err
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("telemetry-policy-change does not accept positional arguments")
	}
	run, chosen := strings.TrimSpace(*id), strings.TrimSpace(*policy)
	if run == "" || chosen == "" {
		return errors.New("telemetry-policy-change needs --id and --policy")
	}
	if err := analysisCLIScopeAdmission(run, ""); err != nil {
		return err
	}
	if *interval < 0 || *expected < 0 {
		return errors.New("--interval and --expected-sequence cannot be negative")
	}
	address := strings.TrimRight(strings.TrimSpace(*endpoint), "/")
	if err := validateCLIControllerURL(address); err != nil {
		return err
	}
	client := NewApiClientWithAuth(address, *apiKey, *bearer)
	path := "/api/v1/tests/" + url.PathEscape(run) + "/telemetry-policy-segments"
	if *expected == 0 {
		data, err := client.workflowRequest(ctx, http.MethodGet, path, nil, "")
		if err != nil {
			return err
		}
		var list struct {
			Segments []struct {
				Sequence int64 `json:"sequence"`
			} `json:"segments"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			return errors.New("invalid telemetry policy segments response")
		}
		if len(list.Segments) == 0 {
			return errors.New("the run has no telemetry policy receipt to change")
		}
		*expected = list.Segments[len(list.Segments)-1].Sequence
	}
	if strings.TrimSpace(*requestID) == "" {
		*requestID = uuid.NewString()
		fmt.Fprintf(os.Stderr, "request ID %s (pass --request-id %s to retry this change)\n", *requestID, *requestID)
	}
	body, err := json.Marshal(map[string]any{"request_id": strings.TrimSpace(*requestID), "telemetry_budget_policy_id": chosen, "controller_publication_interval_seconds": *interval, "expected_sequence": *expected})
	if err != nil {
		return err
	}
	data, err := client.workflowRequest(ctx, http.MethodPost, path, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	var segment map[string]any
	if err := json.Unmarshal(data, &segment); err != nil {
		return errors.New("invalid telemetry policy segment response")
	}
	encoded, err := json.MarshalIndent(segment, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}
