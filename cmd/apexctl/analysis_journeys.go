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
	"strconv"
	"strings"
)

const analysisJourneysUsage = `Usage:
  apexctl analysis journeys --run ID [--policy ID [--revision N]] [--api URL] [--api-key KEY|--bearer-token TOKEN]
Prints a finalized run's journey accounting: completed, failed, aborted,
skipped and censored attempts per journey, step failures, and the weighted
failure index (weighted test-journey risk, never revenue). The run's pinned
journey_policy applies unless --policy names another (its latest revision,
or --revision).`

func executeAnalysisJourneysCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		_, err := fmt.Fprintln(out, analysisJourneysUsage)
		return err
	}
	flags := flag.NewFlagSet("apexctl analysis journeys", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("api", firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://127.0.0.1:8080"), "controller URL")
	run := flags.String("run", "", "run ID")
	policy := flags.String("policy", "", "journey_policy definition ID (default: the run's pin)")
	revision := flags.Int64("revision", 0, "journey_policy revision (default: latest)")
	apiKey := flags.String("api-key", os.Getenv("APEXLOAD_API_KEY"), "controller API key")
	bearer := flags.String("bearer-token", os.Getenv("APEXLOAD_BEARER_TOKEN"), "controller bearer token")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := fmt.Fprintln(out, analysisJourneysUsage)
			return err
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("journeys does not accept positional arguments")
	}
	if strings.TrimSpace(*run) == "" {
		return errors.New("journeys needs --run")
	}
	if *revision < 0 || (*revision > 0 && strings.TrimSpace(*policy) == "") {
		return errors.New("--revision must be positive and needs --policy")
	}
	if err := analysisCLIScopeAdmission(*run, *policy); err != nil {
		return err
	}
	address := strings.TrimRight(strings.TrimSpace(*endpoint), "/")
	if err := validateCLIControllerURL(address); err != nil {
		return err
	}
	query := url.Values{}
	if *policy != "" {
		query.Set("policy", *policy)
		if *revision > 0 {
			query.Set("revision", strconv.FormatInt(*revision, 10))
		}
	}
	path := "/api/v1/tests/" + url.PathEscape(*run) + "/archive/journeys"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	client := NewApiClientWithAuth(address, *apiKey, *bearer)
	data, err := client.workflowRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		return errors.New("invalid journey report")
	}
	pretty.WriteByte('\n')
	_, err = out.Write(pretty.Bytes())
	return err
}
