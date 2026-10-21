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

// Browser intelligence CLI (BI-4): interaction patterns, one request's HAR
// entry, and the revisioned browser and journey policies.

const analysisInteractionsUsage = `Usage:
  apexctl analysis interactions --run ID [--api URL] [--api-key KEY|--bearer-token TOKEN]
Prints a finalized run's interaction patterns and heatmaps, including the
analysis version, coverage and limitations.`

const analysisHAREntryUsage = `Usage:
  apexctl analysis har-entry --run ID --request-id ID [--api URL] [--api-key KEY|--bearer-token TOKEN]
Prints one retained browser request as a synthetic HAR entry, names the
native HAR that holds it when its browser context recorded one, or says why
the synthetic HAR leaves it out (fidelity "omitted"). Needs the HAR download
permission (editor). A 503 (another browser archive read in progress, or out
of time) can be retried.`

const analysisPoliciesUsage = `Usage:
  apexctl analysis policies list --project ID --kind KIND
  apexctl analysis policies get --project ID --id ID [--revision N]
  apexctl analysis policies save --project ID --kind KIND --name NAME --file DEFINITION.json [--id ID --revision N]
  apexctl analysis policies delete --project ID --id ID --revision N
Common flags: [--api URL] [--api-key KEY|--bearer-token TOKEN]
KIND is browser_policy, journey_policy, telemetry_policy,
telemetry_acceptance_policy or derived_operation. Saving an existing policy
(--id) names the revision it was read at; a newer revision is refused, so
concurrent edits never overwrite each other. Runs pin the revision they
started with, and findings keep the revision they were evaluated under.`

type analysisCLIFlags struct {
	set      *flag.FlagSet
	endpoint *string
	apiKey   *string
	bearer   *string
}

func newAnalysisCLIFlags(name string) analysisCLIFlags {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	return analysisCLIFlags{
		set:      set,
		endpoint: set.String("api", firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://127.0.0.1:8080"), "controller URL"),
		apiKey:   set.String("api-key", os.Getenv("APEXLOAD_API_KEY"), "controller API key"),
		bearer:   set.String("bearer-token", os.Getenv("APEXLOAD_BEARER_TOKEN"), "controller bearer token"),
	}
}

func (f analysisCLIFlags) parse(args []string, usage string, out io.Writer) (bool, error) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		_, err := fmt.Fprintln(out, usage)
		return true, err
	}
	if err := f.set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := fmt.Fprintln(out, usage)
			return true, err
		}
		return false, err
	}
	if f.set.NArg() != 0 {
		return false, fmt.Errorf("%s does not accept positional arguments", f.set.Name())
	}
	return false, nil
}

func (f analysisCLIFlags) client() (*ApiClient, error) {
	address := strings.TrimRight(strings.TrimSpace(*f.endpoint), "/")
	if err := validateCLIControllerURL(address); err != nil {
		return nil, err
	}
	return NewApiClientWithAuth(address, *f.apiKey, *f.bearer), nil
}

func writeIndentedJSON(out io.Writer, data []byte, what string) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err != nil {
		return fmt.Errorf("invalid %s", what)
	}
	pretty.WriteByte('\n')
	_, err := out.Write(pretty.Bytes())
	return err
}

func executeAnalysisInteractionsCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := newAnalysisCLIFlags("interactions")
	run := flags.set.String("run", "", "run ID")
	if done, err := flags.parse(args, analysisInteractionsUsage, out); done || err != nil {
		return err
	}
	if strings.TrimSpace(*run) == "" {
		return errors.New("interactions needs --run")
	}
	if err := analysisCLIScopeAdmission(*run, ""); err != nil {
		return err
	}
	client, err := flags.client()
	if err != nil {
		return err
	}
	data, err := client.workflowRequest(ctx, http.MethodGet, "/api/v1/tests/"+url.PathEscape(*run)+"/archive/browser-interactions", nil, "")
	if err != nil {
		return err
	}
	return writeIndentedJSON(out, data, "interaction analysis")
}

func executeAnalysisHAREntryCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := newAnalysisCLIFlags("har-entry")
	run := flags.set.String("run", "", "run ID")
	requestID := flags.set.String("request-id", "", "the retained request's native request identity")
	if done, err := flags.parse(args, analysisHAREntryUsage, out); done || err != nil {
		return err
	}
	if strings.TrimSpace(*run) == "" || strings.TrimSpace(*requestID) == "" {
		return errors.New("har-entry needs --run and --request-id")
	}
	if err := analysisCLIScopeAdmission(*run, *requestID); err != nil {
		return err
	}
	client, err := flags.client()
	if err != nil {
		return err
	}
	data, err := client.workflowRequest(ctx, http.MethodGet, "/api/v1/tests/"+url.PathEscape(*run)+"/archive/browser/har-entry?request_id="+url.QueryEscape(*requestID), nil, "")
	if err != nil {
		return err
	}
	return writeIndentedJSON(out, data, "HAR entry")
}

// The controller serves at most analysisPolicyPage policies a request, and a
// project keeps up to analysisPoliciesPerKind of each kind.
const (
	analysisPolicyPage      = 100
	analysisPoliciesPerKind = 1000
)

var analysisPolicyKinds = []string{"browser_policy", "journey_policy", "telemetry_policy", "telemetry_acceptance_policy", "derived_operation"}

func executeAnalysisPoliciesCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		_, err := fmt.Fprintln(out, analysisPoliciesUsage)
		return err
	}
	verb := args[0]
	flags := newAnalysisCLIFlags("policies " + verb)
	project := flags.set.String("project", "", "project ID")
	kind := flags.set.String("kind", "", "policy kind")
	id := flags.set.String("id", "", "policy ID")
	revision := flags.set.Int64("revision", 0, "policy revision")
	name := flags.set.String("name", "", "policy name")
	file := flags.set.String("file", "", "policy definition JSON file")
	if done, err := flags.parse(args[1:], analysisPoliciesUsage, out); done || err != nil {
		return err
	}
	if strings.TrimSpace(*project) == "" {
		return fmt.Errorf("policies %s needs --project", verb)
	}
	if err := analysisCLIScopeAdmission(*id, *project); err != nil {
		return err
	}
	if *revision < 0 {
		return errors.New("--revision must be positive")
	}
	kindValid := func() error {
		for _, known := range analysisPolicyKinds {
			if *kind == known {
				return nil
			}
		}
		return fmt.Errorf("--kind must be one of %s", strings.Join(analysisPolicyKinds, ", "))
	}
	scope := url.Values{"project_id": {*project}}
	var method, path string
	var body []byte
	switch verb {
	case "list":
		if err := kindValid(); err != nil {
			return err
		}
		scope.Set("kind", *kind)
		return listAllAnalysisPolicies(ctx, flags, scope, out)
	case "get":
		if strings.TrimSpace(*id) == "" {
			return errors.New("policies get needs --id")
		}
		if *revision > 0 {
			scope.Set("revision", strconv.FormatInt(*revision, 10))
		}
		method, path = http.MethodGet, "/api/v1/analysis/policies/"+url.PathEscape(*id)
	case "save":
		if err := kindValid(); err != nil {
			return err
		}
		if strings.TrimSpace(*name) == "" || strings.TrimSpace(*file) == "" {
			return errors.New("policies save needs --name and --file")
		}
		if (*id == "") != (*revision == 0) {
			return errors.New("updating a policy needs both --id and the --revision it was read at; a new policy takes neither")
		}
		definition, err := os.ReadFile(*file)
		if err != nil {
			return fmt.Errorf("read --file: %w", err)
		}
		if !json.Valid(definition) {
			return errors.New("--file is not JSON")
		}
		request := map[string]any{"kind": *kind, "name": *name, "definition": json.RawMessage(definition)}
		method, path = http.MethodPost, "/api/v1/analysis/policies"
		if *id != "" {
			request["revision"] = *revision
			method, path = http.MethodPut, "/api/v1/analysis/policies/"+url.PathEscape(*id)
		}
		if body, err = json.Marshal(request); err != nil {
			return err
		}
	case "delete":
		if strings.TrimSpace(*id) == "" || *revision == 0 {
			return errors.New("policies delete needs --id and --revision")
		}
		scope.Set("revision", strconv.FormatInt(*revision, 10))
		method, path = http.MethodDelete, "/api/v1/analysis/policies/"+url.PathEscape(*id)
	default:
		return fmt.Errorf("unknown policies command %q (list, get, save or delete)", verb)
	}
	client, err := flags.client()
	if err != nil {
		return err
	}
	var reader io.Reader
	contentType := ""
	if body != nil {
		reader, contentType = bytes.NewReader(body), "application/json"
	}
	data, err := client.workflowRequest(ctx, method, path+"?"+scope.Encode(), reader, contentType)
	if err != nil {
		return err
	}
	return writeIndentedJSON(out, data, "policy response")
}

// listAllAnalysisPolicies reads every page of a project's policies of one
// kind and writes them as one list.
func listAllAnalysisPolicies(ctx context.Context, flags analysisCLIFlags, scope url.Values, out io.Writer) error {
	client, err := flags.client()
	if err != nil {
		return err
	}
	policies := []json.RawMessage{}
	for offset := 0; offset < analysisPoliciesPerKind; offset += analysisPolicyPage {
		scope.Set("limit", strconv.Itoa(analysisPolicyPage))
		scope.Set("offset", strconv.Itoa(offset))
		data, err := client.workflowRequest(ctx, http.MethodGet, "/api/v1/analysis/policies?"+scope.Encode(), nil, "")
		if err != nil {
			return err
		}
		var page []json.RawMessage
		if err := json.Unmarshal(data, &page); err != nil {
			return fmt.Errorf("decode policies: %w", err)
		}
		policies = append(policies, page...)
		if len(page) < analysisPolicyPage {
			break
		}
	}
	data, err := json.Marshal(policies)
	if err != nil {
		return err
	}
	return writeIndentedJSON(out, data, "policy response")
}
