package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

const briefUsage = `apexctl brief - print a finished run's agent brief

Usage:
  apexctl brief <test-id> [flags]

Prints the run's verdict-first, size-bounded result (apexload.agent_brief.v1):
the verdict, blocking flag and reason codes, metrics against the newest saved
baseline comparison, and ranked findings with ids that stay the same across runs.

Flags:
  --api URL            Controller URL (or APEXLOAD_API) [default: http://127.0.0.1:8080]
  --api-key KEY        Controller API key (or APEXLOAD_API_KEY)
  --bearer-token TOKEN Controller bearer token (or APEXLOAD_BEARER_TOKEN)
  --detail LEVEL       summary, standard or full [default: standard]
  --format FORMAT      json, md (Markdown) or ctrf [default: json]
  --baseline MODE      latest or none [default: latest]
  --comparison ID      A saved comparison of this run (instead of --baseline)
  --out PATH           Write to a file instead of standard output
  --no-fail            Exit 0 even when the brief is blocking

Exit codes: 0 not blocking, 1 blocking, 2 usage, 3 controller or transport
error (including a controller too old to serve briefs).`

func briefCmd() {
	os.Exit(executeBriefCommand(context.Background(), os.Args[2:], os.Stdout, os.Stderr))
}

// briefOptions is a parsed brief request.
type briefOptions struct {
	detail, format, baseline, comparison string
}

func (o briefOptions) query(format string) url.Values {
	query := url.Values{"detail": {o.detail}, "format": {format}}
	if o.comparison != "" {
		query.Set("comparison_id", o.comparison)
	} else {
		query.Set("baseline", o.baseline)
	}
	return query
}

func executeBriefCommand(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(out, briefUsage)
		if len(args) == 0 {
			return exitCodeUsageOrConfiguration
		}
		return exitCodeSuccess
	}
	testID := strings.TrimSpace(args[0])
	flags := flag.NewFlagSet("apexctl brief", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("api", firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://127.0.0.1:8080"), "controller URL")
	apiKey := flags.String("api-key", os.Getenv("APEXLOAD_API_KEY"), "controller API key")
	bearer := flags.String("bearer-token", os.Getenv("APEXLOAD_BEARER_TOKEN"), "controller bearer token")
	opts := briefOptions{}
	flags.StringVar(&opts.detail, "detail", "standard", "summary, standard or full")
	flags.StringVar(&opts.format, "format", "json", "json, md or ctrf")
	flags.StringVar(&opts.baseline, "baseline", "latest", "latest or none")
	flags.StringVar(&opts.comparison, "comparison", "", "saved comparison ID")
	output := flags.String("out", "", "output file")
	noFail := flags.Bool("no-fail", false, "exit 0 even when blocking")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || testID == "" || strings.HasPrefix(testID, "-") {
		fmt.Fprintln(errOut, "usage: apexctl brief <test-id> [--detail summary|standard|full] [--format json|md|ctrf] [--baseline latest|none | --comparison ID] [--out PATH] [--no-fail]")
		return exitCodeUsageOrConfiguration
	}
	switch {
	case opts.detail != "summary" && opts.detail != "standard" && opts.detail != "full":
		fmt.Fprintln(errOut, "--detail must be summary, standard or full")
		return exitCodeUsageOrConfiguration
	case opts.format != "json" && opts.format != "md" && opts.format != "ctrf":
		fmt.Fprintln(errOut, "--format must be json, md or ctrf")
		return exitCodeUsageOrConfiguration
	case opts.baseline != "latest" && opts.baseline != "none":
		fmt.Fprintln(errOut, "--baseline must be latest or none")
		return exitCodeUsageOrConfiguration
	}
	address := strings.TrimRight(strings.TrimSpace(*endpoint), "/")
	if err := validateCLIControllerURL(address); err != nil {
		fmt.Fprintln(errOut, err)
		return exitCodeUsageOrConfiguration
	}
	client := NewApiClientWithAuth(address, *apiKey, *bearer)
	body, blocking, err := client.fetchAgentBrief(ctx, "/api/v1/tests/"+url.PathEscape(testID)+"/archive/agent-brief", opts)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return exitCodeControllerOrTransport
	}
	// JSON and CTRF stay machine-readable on stdout (a pipe to jq); only the
	// Markdown, which a runner log shows, is wrapped.
	if (*output == "" || *output == "-") && strings.EqualFold(os.Getenv("GITHUB_ACTIONS"), "true") {
		if opts.format == "md" {
			if !writesToFile(out) {
				body = wrapGitHubStopCommands(body)
			}
		} else {
			body = escapeLegacyWorkflowCommands(body)
		}
	}
	if err := writeBriefOutput(*output, out, body); err != nil {
		fmt.Fprintln(errOut, err)
		return exitCodeUsageOrConfiguration
	}
	if blocking && !*noFail {
		return exitCodeTestFailure
	}
	return exitCodeSuccess
}

// agentBriefVerdict is the part of a brief or CTRF report apexctl gates on.
type agentBriefVerdict struct {
	Verdict struct {
		Blocking bool `json:"blocking"`
	} `json:"verdict"`
	Results struct {
		Extra struct {
			APEXLoad struct {
				Blocking bool `json:"blocking"`
			} `json:"apexload"`
		} `json:"extra"`
	} `json:"results"`
}

// fetchAgentBrief reads a brief in the requested format from path (the
// archive route or the CI route) and says whether it blocks. Markdown comes
// with the JSON brief (format=bundle) on the archive route, so the blocking
// flag is read from the same response; the CI route has no bundle, so it
// reads the JSON brief separately.
func (c *ApiClient) fetchAgentBrief(ctx context.Context, path string, opts briefOptions) ([]byte, bool, error) {
	switch opts.format {
	case "md":
		if strings.HasPrefix(path, "/api/v1/ci/") {
			markdown, err := c.agentBriefRequest(ctx, path, opts.query("md"))
			if err != nil {
				return nil, false, err
			}
			brief, err := c.agentBriefRequest(ctx, path, opts.query("json"))
			if err != nil {
				return nil, false, err
			}
			var verdict agentBriefVerdict
			if err := json.Unmarshal(brief, &verdict); err != nil {
				return nil, false, fmt.Errorf("controller returned an unreadable brief: %w", err)
			}
			return markdown, verdict.Verdict.Blocking, nil
		}
		body, err := c.agentBriefRequest(ctx, path, opts.query("bundle"))
		if err != nil {
			return nil, false, err
		}
		var bundle struct {
			Brief    agentBriefVerdict `json:"brief"`
			Markdown string            `json:"markdown"`
		}
		if err := json.Unmarshal(body, &bundle); err != nil {
			return nil, false, fmt.Errorf("controller returned an unreadable brief: %w", err)
		}
		return []byte(bundle.Markdown), bundle.Brief.Verdict.Blocking, nil
	default:
		body, err := c.agentBriefRequest(ctx, path, opts.query(opts.format))
		if err != nil {
			return nil, false, err
		}
		var verdict agentBriefVerdict
		if err := json.Unmarshal(body, &verdict); err != nil {
			return nil, false, fmt.Errorf("controller returned an unreadable brief: %w", err)
		}
		return body, verdict.Verdict.Blocking || verdict.Results.Extra.APEXLoad.Blocking, nil
	}
}

// agentBriefRequest GETs a brief, turning the controller's coded errors into
// messages an operator can act on.
func (c *ApiClient) agentBriefRequest(ctx context.Context, path string, query url.Values) ([]byte, error) {
	// The context sets the deadline, not the client's shorter default: a
	// full brief against a baseline reads two archives. The request still
	// goes through doContextWithHeaders, whose redirect policy keeps the
	// credentials on the controller's origin.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	unbounded := *c
	httpClient := http.Client{}
	if c.Client != nil {
		httpClient = *c.Client
	}
	httpClient.Timeout = 0
	unbounded.Client = &httpClient
	res, err := unbounded.doContextWithHeaders(ctx, http.MethodGet, path+"?"+query.Encode(), nil, "", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 8<<20 {
		return nil, errors.New("controller response exceeds 8 MiB")
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return data, nil
	}
	var coded struct {
		Error       string `json:"error"`
		Code        string `json:"code"`
		Remediation string `json:"remediation"`
	}
	_ = json.Unmarshal(data, &coded)
	if coded.Code == "" {
		if res.StatusCode == http.StatusNotFound {
			return nil, errors.New("the controller does not serve agent briefs; it needs an APEX Load release with the agent-v1 brief routes")
		}
		return nil, fmt.Errorf("controller returned %s: %s", res.Status, briefErrorExcerpt(data))
	}
	message := fmt.Sprintf("controller returned %s (%s): %s", res.Status, coded.Code, coded.Error)
	if coded.Remediation != "" {
		message += "; " + coded.Remediation
	}
	return nil, errors.New(message)
}

func writeBriefOutput(path string, fallback io.Writer, body []byte) error {
	if !strings.HasSuffix(string(body), "\n") {
		body = append(body, '\n')
	}
	if path == "" || path == "-" {
		_, err := fallback.Write(body)
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

// briefErrorExcerpt is an uncoded error body cut for a log line: at most 200
// bytes, control characters removed, since CI logs can be public.
func briefErrorExcerpt(body []byte) string {
	text := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(string(body), ""))
	text = strings.Join(strings.Fields(text), " ")
	text = strings.ReplaceAll(text, "##[", "# #[")
	if strings.HasPrefix(text, "::") {
		text = ": :" + strings.TrimPrefix(text, "::")
	}
	if len(text) > 200 {
		text = strings.ToValidUTF8(text[:200], "") + "…"
	}
	return text
}

// wrapGitHubStopCommands brackets stdout in GitHub Actions' stop-commands
// markers, with a random token, so nothing in a brief (which can quote the
// system under test) runs as a workflow command.
func wrapGitHubStopCommands(body []byte) []byte {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return body
	}
	marker := "apexload-" + hex.EncodeToString(token)
	wrapped := "::stop-commands::" + marker + "\n" + string(body)
	if !strings.HasSuffix(wrapped, "\n") {
		wrapped += "\n"
	}
	return []byte(wrapped + "::" + marker + "::\n")
}

// escapeLegacyWorkflowCommands rewrites "##[" in a JSON body as "#\u0023[".
// '#' only occurs inside JSON strings and \u0023 decodes back to it, so the
// value is unchanged for jq while a runner no longer sees the legacy
// "##[command]" prefix, which it honours anywhere in a line.
func escapeLegacyWorkflowCommands(body []byte) []byte {
	return []byte(strings.ReplaceAll(string(body), "##[", "#\\u0023["))
}

// writesToFile says out is a regular file (stdout redirected to a job
// summary, for example), which the runner does not parse for commands.
func writesToFile(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode().IsRegular()
}
