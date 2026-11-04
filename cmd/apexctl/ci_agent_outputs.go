package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// ciAgentOutputTimeout bounds fetching the brief after a finished run.
	// The fetch runs after the gate's outcome is settled and outside
	// --timeout, so it can delay the exit by at most this long.
	ciAgentOutputTimeout = 30 * time.Second
	// githubSummaryMaxBytes is GitHub's job summary limit.
	githubSummaryMaxBytes = 1 << 20
)

// ciAgentOutputs are a finished CI run's agent brief renderings.
type ciAgentOutputs struct {
	brief, markdown, ctrf []byte
}

// fetchCIAgentOutputs reads the run's agent brief from the CI brief route,
// with the credentials the run used, in the forms the flags ask for. A
// failure only warns: these files describe the gate, they never change it.
func fetchCIAgentOutputs(ctx context.Context, args ciArgs, auth *ciAuthSession, client *ApiClient, testID string) ciAgentOutputs {
	var outputs ciAgentOutputs
	if strings.TrimSpace(testID) == "" || client == nil || (args.agentBriefPath == "" && args.ctrfPath == "" && args.githubSummary == "") {
		return outputs
	}
	ctx, cancel := context.WithTimeout(ctx, ciAgentOutputTimeout)
	defer cancel()
	if auth != nil {
		if err := auth.ensure(ctx, client); err != nil {
			fmt.Fprintf(os.Stderr, "agent brief unavailable: %v\n", err)
			return outputs
		}
	}
	path := "/api/v1/ci/runs/" + url.PathEscape(testID) + "/agent-brief"
	fetch := func(format, detail string, target *[]byte) {
		body, err := client.agentBriefRequest(ctx, path, url.Values{"detail": {detail}, "format": {format}})
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent brief (%s) unavailable: %v\n", format, err)
			return
		}
		*target = body
	}
	if args.agentBriefPath != "" {
		fetch("json", "standard", &outputs.brief)
	}
	if args.githubSummary != "" {
		// A job summary can be public: the summary level carries no text
		// from the system under test.
		fetch("md", "summary", &outputs.markdown)
	}
	if args.ctrfPath != "" {
		fetch("ctrf", "standard", &outputs.ctrf)
	}
	return outputs
}

// writeCIAgentOutputs writes the fetched brief and CTRF report. A requested
// file the controller could not serve is not written.
func writeCIAgentOutputs(args ciArgs, outputs ciAgentOutputs) error {
	for _, output := range []struct {
		flag, path string
		body       []byte
	}{
		{"--agent-brief", args.agentBriefPath, outputs.brief},
		{"--ctrf", args.ctrfPath, outputs.ctrf},
	} {
		if output.path == "" {
			continue
		}
		if len(output.body) == 0 {
			// Never leave an earlier run's file to pass for this run's, but
			// never remove a file another output of this run just wrote.
			if ciOutputPathCollides(args, output.path) {
				continue
			}
			if err := os.Remove(output.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove stale %s: %w", output.flag, err)
			}
			continue
		}
		if err := ensureEvidenceParent(output.path); err != nil {
			return err
		}
		if err := os.WriteFile(output.path, output.body, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", output.flag, err)
		}
	}
	return nil
}

// appendCIAgentSummary appends the brief's Markdown to the job summary,
// after the gate summary, truncated on a UTF-8 boundary to GitHub's limit.
func appendCIAgentSummary(path string, markdown []byte) error {
	text := string(markdown)
	if len(text) > githubSummaryMaxBytes {
		text = strings.ToValidUTF8(text[:githubSummaryMaxBytes-64], "") + "\n\n_Summary truncated to GitHub's limit._\n"
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open GitHub summary: %w", err)
	}
	defer file.Close()
	_, err = io.WriteString(file, text)
	return err
}

// ciOutputPathCollides says path is also one of the run's other evidence
// files (validation refuses that, but a configuration error still writes
// evidence with the same arguments).
func ciOutputPathCollides(args ciArgs, path string) bool {
	target, err := canonicalCIOutputPath(path)
	if err != nil {
		return true
	}
	for _, other := range []string{args.jsonPath, args.junitPath, args.githubOutput, args.githubSummary} {
		if strings.TrimSpace(other) == "" {
			continue
		}
		if canonical, err := canonicalCIOutputPath(other); err != nil || canonical == target {
			return true
		}
	}
	return false
}
