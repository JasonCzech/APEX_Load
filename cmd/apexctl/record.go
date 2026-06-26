package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
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
}

func parseRecordArgs(args []string) recordArgs {
	r := recordArgs{
		out: "recording.json",
		api: firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://localhost:8080"),
	}
	for i := 0; i < len(args); i++ {
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch args[i] {
		case "--url", "-u":
			r.url = next()
		case "--out", "-o":
			r.out = next()
		case "--har":
			r.harOut = next()
		case "--name", "-n":
			r.name = next()
		case "--api":
			r.api = next()
		case "--api-key":
			r.apiKey = next()
		case "--bearer-token":
			r.bearerToken = next()
		case "--project":
			r.project = next()
		case "--upload":
			r.upload = true
		case "--headless":
			r.headless = true
		case "--no-har":
			r.noHAR = true
		case "--max-think-ms":
			if value, err := strconv.Atoi(next()); err == nil && value > 0 {
				r.maxThinkMs = value
			}
		}
	}
	if r.harOut == "" && !r.noHAR {
		r.harOut = strings.TrimSuffix(r.out, ".json") + ".har"
	}
	return r
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
		os.Exit(1)
	}
	args := parseRecordArgs(os.Args[3:])
	if args.url == "" {
		fmt.Fprintln(os.Stderr, "error: --url is required")
		recordUsage()
		os.Exit(1)
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
		os.Exit(1)
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
		os.Exit(1)
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
		os.Exit(1)
	}
	if err := os.WriteFile(args.out, scriptJSON, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: write %s: %v\n", args.out, err)
		os.Exit(1)
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
			os.Exit(1)
		}
		fmt.Printf("Uploaded script %s to %s\n", scriptID, args.api)
	}
}
