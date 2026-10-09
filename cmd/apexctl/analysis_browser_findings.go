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
	"time"
)

const analysisBrowserFindingsUsage = `Usage:
  apexctl analysis browser-findings --run ID [--evaluate [--policy ID --revision N] --project ID [--wait] [--timeout 5m]] [--api URL] [--api-key KEY|--bearer-token TOKEN]
Prints a run's latest browser findings and qualified score (browser_findings.v1,
browser_score.v1) under its pinned browser_policy. --evaluate queues a
browser_intelligence evaluation first; --wait polls it and fails unless it is
ready. An evaluation under a named revision (--policy, --revision) is not the
run's result: its own evaluation job is printed instead (with --wait, once
it settles).`

// browserFindingsPollInterval spaces --wait polls.
var browserFindingsPollInterval = 2 * time.Second

func executeAnalysisBrowserFindingsCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		_, err := fmt.Fprintln(out, analysisBrowserFindingsUsage)
		return err
	}
	flags := flag.NewFlagSet("apexctl analysis browser-findings", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("api", firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://127.0.0.1:8080"), "controller URL")
	run := flags.String("run", "", "run ID")
	project := flags.String("project", "", "project ID (with --evaluate)")
	evaluate := flags.Bool("evaluate", false, "queue an evaluation first")
	policy := flags.String("policy", "", "browser_policy definition ID (default: the run's pin)")
	revision := flags.Int64("revision", 0, "browser_policy revision")
	wait := flags.Bool("wait", false, "wait for the queued evaluation")
	timeout := flags.Duration("timeout", 5*time.Minute, "how long --wait waits")
	apiKey := flags.String("api-key", os.Getenv("APEXLOAD_API_KEY"), "controller API key")
	bearer := flags.String("bearer-token", os.Getenv("APEXLOAD_BEARER_TOKEN"), "controller bearer token")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := fmt.Fprintln(out, analysisBrowserFindingsUsage)
			return err
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("browser-findings does not accept positional arguments")
	}
	if strings.TrimSpace(*run) == "" {
		return errors.New("browser-findings needs --run")
	}
	if !*evaluate && (*policy != "" || *revision != 0 || *wait) {
		return errors.New("--policy, --revision and --wait need --evaluate")
	}
	if *evaluate && strings.TrimSpace(*project) == "" {
		return errors.New("--evaluate needs --project")
	}
	if (*policy == "") != (*revision == 0) || *revision < 0 {
		return errors.New("--policy and a positive --revision go together")
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	if err := analysisCLIScopeAdmission(*run, *project); err != nil {
		return err
	}
	if err := analysisCLIScopeAdmission(*policy, ""); err != nil {
		return err
	}
	address := strings.TrimRight(strings.TrimSpace(*endpoint), "/")
	if err := validateCLIControllerURL(address); err != nil {
		return err
	}
	client := NewApiClientWithAuth(address, *apiKey, *bearer)
	if *evaluate {
		request := map[string]any{"run": *run}
		if *policy != "" {
			request["policy"] = map[string]any{"id": *policy, "revision": *revision}
		}
		body, err := json.Marshal(map[string]any{"kind": "browser_intelligence", "request": request})
		if err != nil {
			return err
		}
		query := "?project_id=" + url.QueryEscape(*project)
		data, err := client.workflowRequest(ctx, http.MethodPost, "/api/v1/analysis/evaluations"+query, bytes.NewReader(body), "application/json")
		if err != nil {
			return err
		}
		var job struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Failure string `json:"failure"`
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
				case <-time.After(browserFindingsPollInterval):
				}
				if data, err = client.workflowRequest(waitCtx, http.MethodGet, "/api/v1/analysis/evaluations/"+url.PathEscape(job.ID)+query, nil, ""); err != nil {
					return err
				}
				if err := json.Unmarshal(data, &job); err != nil {
					return errors.New("invalid evaluation response")
				}
			}
			if job.State != "ready" {
				return fmt.Errorf("evaluation %s ended %s: %s", job.ID, job.State, job.Failure)
			}
		}
		// An evaluation under a named revision is its own job, never the run's
		// result: print it (with its ID), waited for or not.
		if *policy != "" {
			return printIndentedJSON(out, data, "invalid evaluation response")
		}
	}
	data, err := client.workflowRequest(ctx, http.MethodGet, "/api/v1/tests/"+url.PathEscape(*run)+"/archive/browser-findings", nil, "")
	if err != nil {
		return err
	}
	return printIndentedJSON(out, data, "invalid browser findings response")
}

func printIndentedJSON(out io.Writer, data []byte, invalid string) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		return errors.New(invalid)
	}
	pretty.WriteByte('\n')
	_, err := out.Write(pretty.Bytes())
	return err
}
