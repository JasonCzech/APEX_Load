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
	"slices"
	"strconv"
	"strings"
	"time"
)

const telemetryReevaluateUsage = `Usage:
  apexctl analysis telemetry-reevaluate --project ID --runs RUN[,RUN...] --policy ID --revision N [--segment RUN=N ...] [--wait] [--timeout 5m] [--api URL] [--api-key KEY|--bearer-token TOKEN]
Queues a telemetry re-evaluation: the named runs' retained interval evidence
is re-classified under one project telemetry_policy revision, as a derived
result; the runs' own classifications are unchanged. --segment selects one
telemetry policy segment of a run whose policy changed while it ran (1 is
the admission receipt): only the intervals it classified are re-evaluated. Prints the evaluation
job; --wait polls until it is ready, failed, cancelled or expired, prints
the final job and fails unless it is ready.`

// telemetryReevaluatePollInterval spaces --wait polls.
var telemetryReevaluatePollInterval = 2 * time.Second

func executeTelemetryReevaluateCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		_, err := fmt.Fprintln(out, telemetryReevaluateUsage)
		return err
	}
	flags := flag.NewFlagSet("apexctl analysis telemetry-reevaluate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("api", firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://127.0.0.1:8080"), "controller URL")
	project := flags.String("project", "", "project ID")
	runs := flags.String("runs", "", "comma-separated run IDs (1 to 10)")
	policy := flags.String("policy", "", "telemetry_policy definition ID")
	revision := flags.Int64("revision", 0, "telemetry_policy revision")
	segments := map[string]int64{}
	flags.Func("segment", "RUN=N: re-evaluate only that segment of the run (repeatable)", func(value string) error {
		run, raw, ok := strings.Cut(value, "=")
		sequence, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if run = strings.TrimSpace(run); !ok || run == "" || err != nil || sequence < 1 {
			return fmt.Errorf("--segment takes RUN=N with N at least 1, not %q", value)
		}
		if _, seen := segments[run]; seen {
			return fmt.Errorf("--segment names run %s twice", run)
		}
		segments[run] = sequence
		return nil
	})
	wait := flags.Bool("wait", false, "wait for the result")
	timeout := flags.Duration("timeout", 5*time.Minute, "how long --wait waits")
	apiKey := flags.String("api-key", os.Getenv("APEXLOAD_API_KEY"), "controller API key")
	bearer := flags.String("bearer-token", os.Getenv("APEXLOAD_BEARER_TOKEN"), "controller bearer token")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := fmt.Fprintln(out, telemetryReevaluateUsage)
			return err
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("telemetry-reevaluate does not accept positional arguments")
	}
	var ids []string
	for _, id := range strings.Split(*runs, ",") {
		if id = strings.TrimSpace(id); id != "" {
			if err := analysisCLIScopeAdmission(id, ""); err != nil {
				return err
			}
			ids = append(ids, id)
		}
	}
	if *project == "" || len(ids) == 0 || len(ids) > 10 || strings.TrimSpace(*policy) == "" || *revision < 1 {
		return errors.New("telemetry-reevaluate needs --project, --runs (1 to 10), --policy and --revision")
	}
	if err := analysisCLIScopeAdmission(*policy, *project); err != nil {
		return err
	}
	for run := range segments {
		if !slices.Contains(ids, run) {
			return fmt.Errorf("--segment names run %s, which --runs does not", run)
		}
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	address := strings.TrimRight(strings.TrimSpace(*endpoint), "/")
	if err := validateCLIControllerURL(address); err != nil {
		return err
	}
	client := NewApiClientWithAuth(address, *apiKey, *bearer)
	request := map[string]any{"runs": ids, "policy": map[string]any{"id": *policy, "revision": *revision}}
	if len(segments) > 0 {
		request["segments"] = segments
	}
	body, err := json.Marshal(map[string]any{"kind": "telemetry_acceptance", "request": request})
	if err != nil {
		return err
	}
	query := "?project_id=" + url.QueryEscape(*project)
	data, err := client.workflowRequest(ctx, http.MethodPost, "/api/v1/analysis/evaluations"+query, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	var job struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(data, &job); err != nil || job.ID == "" {
		return errors.New("invalid evaluation response")
	}
	if *wait {
		waitCtx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		for !telemetryEvaluationTerminal(job.State) {
			select {
			case <-waitCtx.Done():
				return fmt.Errorf("evaluation %s is still %s after %s", job.ID, job.State, *timeout)
			case <-time.After(telemetryReevaluatePollInterval):
			}
			if data, err = client.workflowRequest(waitCtx, http.MethodGet, "/api/v1/analysis/evaluations/"+url.PathEscape(job.ID)+query, nil, ""); err != nil {
				return err
			}
			if err := json.Unmarshal(data, &job); err != nil {
				return errors.New("invalid evaluation response")
			}
		}
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		return errors.New("invalid evaluation response")
	}
	pretty.WriteByte('\n')
	if _, err := out.Write(pretty.Bytes()); err != nil {
		return err
	}
	if *wait && job.State != "ready" {
		return fmt.Errorf("evaluation %s ended %s", job.ID, job.State)
	}
	return nil
}

func telemetryEvaluationTerminal(state string) bool {
	switch state {
	case "ready", "failed", "cancelled", "expired":
		return true
	}
	return false
}
