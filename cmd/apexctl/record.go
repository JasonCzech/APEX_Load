package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
)

type recordArgs struct {
	url         string
	out         string
	harOut      string
	name        string
	api         string
	apiKey      string
	bearerToken string
	project     string
	upload      bool
	headless    bool
	noHAR       bool
	maxThinkMs  int
	help        bool
	err         error
}

func parseRecordArgs(args []string) recordArgs {
	r := recordArgs{
		out:         "recording.json",
		api:         firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://localhost:8080"),
		apiKey:      strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY")),
		bearerToken: strings.TrimSpace(os.Getenv("APEXLOAD_BEARER_TOKEN")),
	}
	flags := flag.NewFlagSet("apexctl record browser", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&r.help, "help", false, "show help")
	flags.BoolVar(&r.help, "h", false, "show help")
	flags.StringVar(&r.url, "url", "", "start URL")
	flags.StringVar(&r.url, "u", "", "start URL")
	flags.StringVar(&r.out, "out", r.out, "script output path")
	flags.StringVar(&r.out, "o", r.out, "script output path")
	flags.StringVar(&r.harOut, "har", "", "HAR output path")
	flags.StringVar(&r.name, "name", "", "script name")
	flags.StringVar(&r.name, "n", "", "script name")
	flags.StringVar(&r.api, "api", r.api, "controller URL")
	flags.StringVar(&r.apiKey, "api-key", r.apiKey, "API key")
	flags.StringVar(&r.bearerToken, "bearer-token", r.bearerToken, "bearer token")
	flags.StringVar(&r.project, "project", "", "project ID")
	flags.BoolVar(&r.upload, "upload", false, "upload recorded script")
	flags.BoolVar(&r.headless, "headless", false, "record headlessly")
	flags.BoolVar(&r.noHAR, "no-har", false, "disable HAR capture")
	flags.IntVar(&r.maxThinkMs, "max-think-ms", 0, "maximum think time")
	if err := flags.Parse(args); err != nil {
		r.err = err
	} else if flags.NArg() != 0 {
		r.err = fmt.Errorf("unexpected argument %q", flags.Arg(0))
	} else if r.maxThinkMs < 0 {
		r.err = fmt.Errorf("--max-think-ms cannot be negative")
	} else if r.noHAR && r.harOut != "" {
		r.err = fmt.Errorf("--har and --no-har cannot be used together")
	}
	r.url = strings.TrimSpace(r.url)
	r.out = strings.TrimSpace(r.out)
	r.harOut = strings.TrimSpace(r.harOut)
	r.name = strings.TrimSpace(r.name)
	r.api = strings.TrimRight(strings.TrimSpace(r.api), "/")
	r.apiKey = strings.TrimSpace(r.apiKey)
	r.bearerToken = strings.TrimSpace(r.bearerToken)
	r.project = strings.TrimSpace(r.project)
	if r.err == nil && r.out == "" {
		r.err = fmt.Errorf("--out cannot be empty")
	} else if r.err == nil && r.upload && r.api == "" {
		r.err = fmt.Errorf("--api is required with --upload")
	} else if err := validateCLIControllerURL(r.api); r.err == nil && err != nil {
		r.err = err
	}
	if r.harOut == "" && !r.noHAR {
		r.harOut = strings.TrimSuffix(r.out, ".json") + ".har"
	}
	if r.err == nil && r.url != "" {
		r.err = validateRecordStartURL(r.url)
	}
	if r.err == nil && r.harOut != "" {
		conflict, err := recordOutputPathsConflict(r.out, r.harOut)
		if err != nil {
			r.err = fmt.Errorf("resolve recorder output paths: %w", err)
		} else if conflict {
			r.err = fmt.Errorf("--out and --har must use distinct files")
		}
	}
	return r
}

func validateRecordStartURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
		return fmt.Errorf("--url must be an absolute http or https URL")
	}
	return nil
}

func recordOutputPathsConflict(scriptPath, harPath string) (bool, error) {
	scriptPath, err := canonicalCIOutputPath(scriptPath)
	if err != nil {
		return false, err
	}
	harPath, err = canonicalCIOutputPath(harPath)
	if err != nil {
		return false, err
	}
	if scriptPath == harPath {
		return true, nil
	}
	scriptInfo, scriptErr := os.Stat(scriptPath)
	harInfo, harErr := os.Stat(harPath)
	return scriptErr == nil && harErr == nil && os.SameFile(scriptInfo, harInfo), nil
}

func recordUsage() {
	fmt.Fprintln(os.Stderr, `Usage: apexctl record browser --url <start-url> [options]

Records a browser session with the embedded APEX Load recorder: a Chromium
window opens on the start URL, your interactions are captured with ranked
selector candidates, think times, and inferred sync policies, and the session
network traffic is written to a side-car HAR. Close the browser window (or
press Ctrl-C) to finish.

Options:
  --url, -u        Start URL to record against                      [required]
  --out, -o        Output script path (default recording.json)
  --har            Side-car HAR path (default <out>.har)
  --no-har         Disable network/HAR capture
  --name, -n       Script name (default derived from output file)
  --max-think-ms   Cap recorded think time per action
  --upload         Upload the finished script to the controller
  --api            Controller base URL (default APEXLOAD_API or http://localhost:8080)
  --api-key        API key for secured upload (or APEXLOAD_API_KEY)
  --bearer-token   Bearer token for secured upload (or APEXLOAD_BEARER_TOKEN)
  --project        Project ID for --upload`)
}

func recordCmd() {
	if len(os.Args) < 3 || os.Args[2] != "browser" {
		recordUsage()
		os.Exit(exitCodeUsageOrConfiguration)
	}
	args := parseRecordArgs(os.Args[3:])
	if args.err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", args.err)
		recordUsage()
		os.Exit(exitCodeUsageOrConfiguration)
	}
	if args.help {
		recordUsage()
		return
	}
	if args.url == "" {
		fmt.Fprintln(os.Stderr, "error: --url is required")
		recordUsage()
		os.Exit(exitCodeUsageOrConfiguration)
	}

	recorder := recbrowser.New(recbrowser.Options{
		StartURL:   args.url,
		Headless:   args.headless,
		CaptureHAR: !args.noHAR,
		// The CLI flow has no dashboard panel, so the in-page advanced sidebar
		// (sections/transactions/notes/recent-steps) is the only control surface.
		Sidebar: true,
	})
	if err := recorder.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error: start recorder: %v\n", err)
		os.Exit(exitCodeControllerOrTransport)
	}
	fmt.Printf("Recording %s — interact in the browser window, then close it (or press Ctrl-C) to finish.\n", args.url)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	select {
	case <-recorder.Done():
	case <-interrupt:
	}

	session, err := recorder.Stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: stop recorder: %v\n", err)
		os.Exit(exitCodeControllerOrTransport)
	}

	name := args.name
	if name == "" {
		name = strings.TrimSuffix(strings.TrimSuffix(args.out, ".json"), "/")
	}
	result := recbrowser.Convert(session, recbrowser.ConvertOptions{
		ScriptName:     name,
		MaxThinkTimeMs: args.maxThinkMs,
	})

	scriptJSON, err := json.MarshalIndent(result.Script, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: encode script: %v\n", err)
		os.Exit(exitCodeControllerOrTransport)
	}
	if err := os.WriteFile(args.out, scriptJSON, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: write %s: %v\n", args.out, err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
	fmt.Printf("Recorded %d step(s) → %s (%d action(s))\n", len(session.Steps), args.out, result.Stats.ConvertedActions)

	if len(session.HAR) > 0 && args.harOut != "" {
		if err := os.WriteFile(args.harOut, session.HAR, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "warning: write side-car HAR %s: %v\n", args.harOut, err)
		} else {
			fmt.Printf("Side-car HAR → %s\n", args.harOut)
		}
	}
	for _, warning := range result.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}

	if args.upload {
		client := NewApiClientWithAuth(args.api, args.apiKey, args.bearerToken)
		scriptID, err := client.UploadScriptWithProject(args.out, scriptJSON, args.project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: upload script: %v\n", err)
			os.Exit(exitCodeControllerOrTransport)
		}
		fmt.Printf("Uploaded script %s to %s\n", scriptID, args.api)
	}
}
