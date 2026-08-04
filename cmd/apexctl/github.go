package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type githubCLIArgs struct {
	command     string
	api         string
	apiKey      string
	bearerToken string
	projectID   string
	kind        string
	owner       string
	repo        string
	branch      string
	path        string
	strategy    string
	scriptID    string
	testID      string
	commitSHA   string
	detailsURL  string
	refresh     bool
	format      string
	help        bool
	err         error
}

func parseGithubArgs(args []string) githubCLIArgs {
	parsed := githubCLIArgs{
		api:         firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://localhost:8080"),
		apiKey:      strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY")),
		bearerToken: strings.TrimSpace(os.Getenv("APEXLOAD_BEARER_TOKEN")),
		kind:        "import",
		format:      "text",
	}
	if len(args) > 0 {
		parsed.command = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("apexctl github", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&parsed.help, "help", false, "show help")
	flags.BoolVar(&parsed.help, "h", false, "show help")
	flags.StringVar(&parsed.api, "api", parsed.api, "controller URL")
	flags.StringVar(&parsed.apiKey, "api-key", parsed.apiKey, "API key")
	flags.StringVar(&parsed.bearerToken, "bearer-token", parsed.bearerToken, "bearer token")
	flags.StringVar(&parsed.projectID, "project", "", "project ID")
	flags.StringVar(&parsed.projectID, "project-id", "", "project ID")
	flags.StringVar(&parsed.kind, "kind", parsed.kind, "sync kind")
	flags.StringVar(&parsed.owner, "owner", "", "repository owner")
	flags.StringVar(&parsed.repo, "repo", "", "repository name")
	flags.StringVar(&parsed.branch, "branch", "", "repository branch")
	flags.StringVar(&parsed.path, "path", "", "repository path")
	flags.StringVar(&parsed.strategy, "strategy", "", "sync strategy")
	flags.StringVar(&parsed.scriptID, "script", "", "script ID")
	flags.StringVar(&parsed.scriptID, "script-id", "", "script ID")
	flags.StringVar(&parsed.testID, "test", "", "test ID")
	flags.StringVar(&parsed.testID, "test-id", "", "test ID")
	flags.StringVar(&parsed.commitSHA, "commit", "", "commit SHA")
	flags.StringVar(&parsed.commitSHA, "commit-sha", "", "commit SHA")
	flags.StringVar(&parsed.detailsURL, "details-url", "", "details URL")
	flags.BoolVar(&parsed.refresh, "refresh", false, "refresh installation data")
	flags.StringVar(&parsed.format, "format", parsed.format, "output format")
	if err := flags.Parse(args); err != nil {
		parsed.err = err
	} else if flags.NArg() != 0 {
		parsed.err = fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	parsed.api = strings.TrimRight(strings.TrimSpace(parsed.api), "/")
	parsed.apiKey = strings.TrimSpace(parsed.apiKey)
	parsed.bearerToken = strings.TrimSpace(parsed.bearerToken)
	parsed.command = strings.ToLower(strings.TrimSpace(parsed.command))
	parsed.projectID = strings.TrimSpace(parsed.projectID)
	parsed.kind = strings.ToLower(strings.TrimSpace(parsed.kind))
	parsed.owner = strings.TrimSpace(parsed.owner)
	parsed.repo = strings.TrimSpace(parsed.repo)
	parsed.branch = strings.TrimSpace(parsed.branch)
	parsed.path = strings.TrimSpace(parsed.path)
	parsed.strategy = strings.ToLower(strings.TrimSpace(parsed.strategy))
	parsed.scriptID = strings.TrimSpace(parsed.scriptID)
	parsed.testID = strings.TrimSpace(parsed.testID)
	parsed.commitSHA = strings.TrimSpace(parsed.commitSHA)
	parsed.detailsURL = strings.TrimSpace(parsed.detailsURL)
	parsed.format = strings.ToLower(strings.TrimSpace(parsed.format))
	if err := validateCLIOutputFormat(parsed.format); parsed.err == nil && err != nil {
		parsed.err = err
	} else if err := validateCLIControllerURL(parsed.api); parsed.err == nil && err != nil {
		parsed.err = err
	}
	return parsed
}

func firstNonEmptyCLI(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func githubCmd() {
	args := parseGithubArgs(os.Args[2:])
	if args.help || args.command == "" || args.command == "help" || args.command == "--help" || args.command == "-h" {
		printGithubUsage()
		return
	}
	if args.err != nil {
		fmt.Fprintf(os.Stderr, "github %s configuration error: %v\n", args.command, args.err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if err := validateGithubArgs(args); err != nil {
		fmt.Fprintf(os.Stderr, "github %s configuration error: %v\n", args.command, err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
	client := NewApiClientWithAuth(strings.TrimRight(args.api, "/"), args.apiKey, args.bearerToken)
	var (
		out map[string]any
		err error
	)
	switch args.command {
	case "diagnostics":
		out, err = client.getJSON("/api/v1/integrations/github/diagnostics")
	case "installations":
		path := "/api/v1/integrations/github/installations"
		if args.refresh {
			path += "?refresh=true"
		}
		out, err = client.getJSON(path)
	case "policy":
		out, err = client.getJSON("/api/v1/projects/" + url.PathEscape(args.projectID) + "/github/policy")
	case "sync":
		out, err = client.postJSON("/api/v1/integrations/github/sync-jobs", map[string]any{
			"project_id": args.projectID,
			"kind":       args.kind,
			"owner":      args.owner,
			"repo":       args.repo,
			"branch":     args.branch,
			"path":       args.path,
			"strategy":   args.strategy,
		})
	case "status":
		out, err = client.getJSON("/api/v1/scripts/" + url.PathEscape(args.scriptID) + "/github/status")
	case "check-run":
		out, err = client.postJSON("/api/v1/tests/"+url.PathEscape(args.testID)+"/github/check-run", map[string]any{
			"owner":       args.owner,
			"repo":        args.repo,
			"branch":      args.branch,
			"commit_sha":  args.commitSHA,
			"script_id":   args.scriptID,
			"details_url": args.detailsURL,
		})
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "github %s failed: %v\n", args.command, err)
		os.Exit(exitCodeControllerOrTransport)
	}
	printGithubResult(args, out)
}

func validateGithubArgs(args githubCLIArgs) error {
	if args.api == "" {
		return fmt.Errorf("--api is required")
	}
	switch args.command {
	case "diagnostics", "installations":
		return nil
	case "policy":
		if args.projectID == "" {
			return fmt.Errorf("--project is required")
		}
		return nil
	case "sync":
		if args.projectID == "" {
			return fmt.Errorf("--project is required")
		}
		if args.kind != "import" && args.kind != "push" {
			return fmt.Errorf("--kind must be import or push")
		}
		if args.kind == "push" && args.strategy != "" && args.strategy != "pr" && args.strategy != "commit" {
			return fmt.Errorf("--strategy must be pr or commit for push sync")
		}
		return nil
	case "status":
		if args.scriptID == "" {
			return fmt.Errorf("--script is required")
		}
		return nil
	case "check-run":
		if args.testID == "" {
			return fmt.Errorf("--test is required")
		}
		return nil
	default:
		return fmt.Errorf("unknown github command: %s", args.command)
	}
}

func printGithubUsage() {
	fmt.Println(`apexctl github - GitHub Enterprise integration commands

Usage:
  apexctl github diagnostics [--api URL] [--api-key KEY|--bearer-token TOKEN]
  apexctl github installations [--api URL] [--refresh]
  apexctl github policy --project <id> [--api URL]
  apexctl github sync --project <id> [--kind import|push] [--owner org --repo name --branch main --path apexload/scripts]
  apexctl github status --script <id> [--api URL]
  apexctl github check-run --test <id> [--script <id>] [--owner org --repo name --commit-sha SHA] [--details-url URL]

Defaults:
  --api defaults to APEXLOAD_API or http://localhost:8080.
  --api-key and --bearer-token default to APEXLOAD_API_KEY and APEXLOAD_BEARER_TOKEN.`)
}

func printGithubResult(args githubCLIArgs, out map[string]any) {
	if args.format == "json" {
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
		return
	}
	switch args.command {
	case "diagnostics":
		fmt.Printf("GitHub enabled: %v\n", out["enabled"])
		fmt.Printf("Automation mode: %v\n", out["automation_mode"])
		fmt.Printf("OAuth/App/PAT: %v / %v / %v\n", out["oauth_configured"], out["app_configured"], out["pat_configured"])
		fmt.Printf("Webhook/Checks: %v / %v\n", out["webhook_configured"], out["checks_enabled"])
		if out["ghes_version"] != nil {
			fmt.Printf("GHES version: %v\n", out["ghes_version"])
		}
	case "sync":
		fmt.Printf("GitHub sync job %v %v: processed %v/%v, created %v, updated %v, conflicts %v, errors %v\n",
			out["id"], out["status"], out["processed"], out["total"], out["created"], out["updated"], out["conflicts"], out["errors"])
	case "status":
		fmt.Printf("GitHub status: %v\n", out["status"])
		if out["html_url"] != nil {
			fmt.Printf("URL: %v\n", out["html_url"])
		}
	default:
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
	}
}

func (c *ApiClient) getJSON(path string) (map[string]any, error) {
	resp, err := c.do(http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *ApiClient) postJSON(path string, payload map[string]any) (map[string]any, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(http.MethodPost, path, bytes.NewReader(data), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
