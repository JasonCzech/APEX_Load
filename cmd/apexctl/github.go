package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
}

func parseGithubArgs(args []string) githubCLIArgs {
	parsed := githubCLIArgs{
		api:    firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://localhost:8080"),
		kind:   "import",
		format: "text",
	}
	if len(args) > 0 {
		parsed.command = args[0]
		args = args[1:]
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--api":
			if i+1 < len(args) {
				i++
				parsed.api = args[i]
			}
		case "--api-key":
			if i+1 < len(args) {
				i++
				parsed.apiKey = args[i]
			}
		case "--bearer-token":
			if i+1 < len(args) {
				i++
				parsed.bearerToken = args[i]
			}
		case "--project", "--project-id":
			if i+1 < len(args) {
				i++
				parsed.projectID = args[i]
			}
		case "--kind":
			if i+1 < len(args) {
				i++
				parsed.kind = args[i]
			}
		case "--owner":
			if i+1 < len(args) {
				i++
				parsed.owner = args[i]
			}
		case "--repo":
			if i+1 < len(args) {
				i++
				parsed.repo = args[i]
			}
		case "--branch":
			if i+1 < len(args) {
				i++
				parsed.branch = args[i]
			}
		case "--path":
			if i+1 < len(args) {
				i++
				parsed.path = args[i]
			}
		case "--strategy":
			if i+1 < len(args) {
				i++
				parsed.strategy = args[i]
			}
		case "--script", "--script-id":
			if i+1 < len(args) {
				i++
				parsed.scriptID = args[i]
			}
		case "--test", "--test-id":
			if i+1 < len(args) {
				i++
				parsed.testID = args[i]
			}
		case "--commit", "--commit-sha":
			if i+1 < len(args) {
				i++
				parsed.commitSHA = args[i]
			}
		case "--details-url":
			if i+1 < len(args) {
				i++
				parsed.detailsURL = args[i]
			}
		case "--refresh":
			parsed.refresh = true
		case "--format":
			if i+1 < len(args) {
				i++
				parsed.format = args[i]
			}
		}
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
	if args.command == "" || args.command == "help" || args.command == "--help" || args.command == "-h" {
		printGithubUsage()
		return
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
		if args.projectID == "" {
			err = fmt.Errorf("--project is required")
			break
		}
		out, err = client.getJSON("/api/v1/projects/" + args.projectID + "/github/policy")
	case "sync":
		if args.projectID == "" {
			err = fmt.Errorf("--project is required")
			break
		}
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
		if args.scriptID == "" {
			err = fmt.Errorf("--script is required")
			break
		}
		out, err = client.getJSON("/api/v1/scripts/" + args.scriptID + "/github/status")
	case "check-run":
		if args.testID == "" {
			err = fmt.Errorf("--test is required")
			break
		}
		out, err = client.postJSON("/api/v1/tests/"+args.testID+"/github/check-run", map[string]any{
			"owner":       args.owner,
			"repo":        args.repo,
			"branch":      args.branch,
			"commit_sha":  args.commitSHA,
			"script_id":   args.scriptID,
			"details_url": args.detailsURL,
		})
	default:
		err = fmt.Errorf("unknown github command: %s", args.command)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "github %s failed: %v\n", args.command, err)
		os.Exit(1)
	}
	printGithubResult(args, out)
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
