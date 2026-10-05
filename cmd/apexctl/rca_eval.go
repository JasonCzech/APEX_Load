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
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
)

// rcaEvalPollInterval is how often a replay asks whether an analysis ended.
var rcaEvalPollInterval = 2 * time.Second

const (
	maxRCAEvalManifestBytes   = 16 << 20
	maxRCAEvalAssessmentBytes = 8 << 20
)

// executeRCAEval scores a labelled corpus's assessments:
//
//	apexctl rca eval --corpus DIR [--split calibration|held_out|all]
//	    [--replay --api URL] [--out FILE] [--require-bars]
//
// With --replay it first re-analyzes each trial's frozen run archive with
// the controller's current engine and saves the assessments in the corpus.
// The default split is calibration; held-out labels are scored only when
// asked for.
func executeRCAEval(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("rca eval", flag.ContinueOnError)
	flags.SetOutput(out)
	corpus := flags.String("corpus", "", "corpus directory holding manifest.json")
	split := flags.String("split", rca.SplitCalibration, "calibration, held_out or all")
	replay := flags.Bool("replay", false, "re-analyze each trial's run archive before scoring")
	endpoint := flags.String("api", "http://127.0.0.1:8080", "controller URL for --replay")
	timeout := flags.Duration("timeout", 10*time.Minute, "how long one replayed analysis may take")
	outFile := flags.String("out", "", "also write the scores to this file")
	requireBars := flags.Bool("require-bars", false, "fail unless the corpus is ready and every release bar is met")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *corpus == "" || flags.NArg() != 0 {
		return errors.New("usage: apexctl rca eval --corpus DIR [--split calibration|held_out|all] [--replay --api URL] [--out FILE] [--require-bars]")
	}
	manifest, err := readRCAEvalManifest(*corpus)
	if err != nil {
		return err
	}
	if *split != rca.SplitCalibration && *split != rca.SplitHeldOut && *split != rca.SplitAll {
		return errors.New("--split must be calibration, held_out or all")
	}
	if *requireBars && *split != rca.SplitHeldOut {
		// The release gate is judged on trials the parameters never saw.
		return errors.New("--require-bars needs --split held_out")
	}
	trials := manifest.TrialsIn(*split)
	if *replay {
		client := NewApiClient(*endpoint)
		client.Client.Timeout = time.Minute
		for _, t := range trials {
			if err := replayRCATrial(ctx, client, *corpus, t, *timeout); err != nil {
				return fmt.Errorf("trial %s: %w", t.ID, err)
			}
		}
	}
	assessments := make(map[string]rca.Assessment, len(trials))
	for _, t := range trials {
		a, err := readRCAEvalAssessment(*corpus, t.Assessment)
		if err != nil {
			return fmt.Errorf("trial %s: %w", t.ID, err)
		}
		assessments[t.ID] = a
	}
	scores, err := rca.ScoreEvaluation(manifest, assessments, *split)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(scores, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if _, err := out.Write(body); err != nil {
		return err
	}
	if *outFile != "" {
		if err := os.WriteFile(*outFile, body, 0o644); err != nil {
			return err
		}
	}
	if *requireBars {
		var unmet []string
		for _, bar := range scores.Bars {
			if !bar.Met {
				unmet = append(unmet, bar.Name)
			}
		}
		if !scores.CorpusReady || len(unmet) > 0 {
			return &verdictError{exitCodeTestFailure, fmt.Sprintf("release bars not met: corpus ready %v, unmet %s", scores.CorpusReady, strings.Join(unmet, ", "))}
		}
	}
	return nil
}

func readRCAEvalManifest(corpus string) (rca.EvalManifest, error) {
	var manifest rca.EvalManifest
	data, err := readBounded(filepath.Join(corpus, "manifest.json"), maxRCAEvalManifestBytes)
	if err != nil {
		return manifest, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, fmt.Errorf("manifest.json: %w", err)
	}
	return manifest, manifest.Validate()
}

// corpusPath resolves a trial's file inside the corpus, refusing paths that
// leave it.
func corpusPath(corpus, name string) (string, error) {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("assessment path %q leaves the corpus", name)
	}
	return filepath.Join(corpus, clean), nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return data, nil
}

// readRCAEvalAssessment reads an assessment saved bare or as an analysis or
// bundle response, which carry it under "assessment".
func readRCAEvalAssessment(corpus, name string) (rca.Assessment, error) {
	var a rca.Assessment
	path, err := corpusPath(corpus, name)
	if err != nil {
		return a, err
	}
	data, err := readBounded(path, maxRCAEvalAssessmentBytes)
	if err != nil {
		return a, err
	}
	var wrapped struct {
		Assessment *rca.Assessment `json:"assessment"`
	}
	if json.Unmarshal(data, &wrapped) == nil && wrapped.Assessment != nil {
		return *wrapped.Assessment, nil
	}
	if err := json.Unmarshal(data, &a); err != nil || a.SchemaVersion != rca.AssessmentSchemaVersion {
		return a, fmt.Errorf("%s is not a %s assessment", name, rca.AssessmentSchemaVersion)
	}
	return a, nil
}

// replayRCATrial asks the controller to analyze a trial's run archive with
// its current engine, waits for the analysis to end, and saves the
// assessment where the manifest names it.
func replayRCATrial(ctx context.Context, client *ApiClient, corpus string, t rca.EvalTrial, timeout time.Duration) error {
	path, err := corpusPath(corpus, t.Assessment)
	if err != nil {
		return err
	}
	base := "/api/v1/tests/" + url.PathEscape(t.RunID) + "/archive/root-cause/analyses"
	request, _ := json.Marshal(map[string]string{"baseline_test_id": t.BaselineRunID})
	created, err := client.workflowRequest(ctx, http.MethodPost, base, bytes.NewReader(request), "application/json")
	if err != nil {
		return err
	}
	var analysis struct {
		AnalysisID string `json:"analysis_id"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(created, &analysis); err != nil || analysis.AnalysisID == "" {
		return errors.New("the controller did not return an analysis")
	}
	deadline := time.Now().Add(timeout)
	for {
		status, err := client.workflowRequest(ctx, http.MethodGet, base+"/"+url.PathEscape(analysis.AnalysisID), nil, "")
		if err != nil {
			return err
		}
		if err := json.Unmarshal(status, &analysis); err != nil {
			return err
		}
		switch analysis.State {
		case "completed":
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			return os.WriteFile(path, status, 0o644)
		case "failed", "cancelled":
			return fmt.Errorf("analysis %s %s", analysis.AnalysisID, analysis.State)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("analysis %s did not end within %s", analysis.AnalysisID, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rcaEvalPollInterval):
		}
	}
}
