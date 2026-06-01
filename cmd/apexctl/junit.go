package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

// JUnit XML structures. The schema is the de-facto Ant/Surefire dialect that
// GitHub Actions, Azure DevOps (PublishTestResults), and Jenkins (junit step)
// all consume, so a single artifact gates pipelines across every major CI.

type junitTestsuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Errors   int              `xml:"errors,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []junitTestsuite `xml:"testsuite"`
}

type junitTestsuite struct {
	Name       string          `xml:"name,attr"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Errors     int             `xml:"errors,attr"`
	Skipped    int             `xml:"skipped,attr"`
	Time       string          `xml:"time,attr"`
	Timestamp  string          `xml:"timestamp,attr"`
	Properties []junitProperty `xml:"properties>property"`
	Cases      []junitTestcase `xml:"testcase"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitTestcase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Error     *junitFailure `xml:"error,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

// junitInput is the protocol-agnostic view the report is built from, so the
// local-engine and API-driven run paths produce identical XML.
type junitInput struct {
	suiteName    string
	classname    string
	duration     time.Duration
	transactions int64
	errors       int64
	tps          float64
	runErr       error
	sla          *SLASummary
}

func buildJUnitReport(in junitInput) junitTestsuites {
	suiteName := strings.TrimSpace(in.suiteName)
	if suiteName == "" {
		suiteName = "apexload"
	}
	classname := strings.TrimSpace(in.classname)
	if classname == "" {
		classname = suiteName
	}
	seconds := fmt.Sprintf("%.3f", in.duration.Seconds())

	suite := junitTestsuite{
		Name:      suiteName,
		Time:      seconds,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Properties: []junitProperty{
			{Name: "transactions", Value: fmt.Sprintf("%d", in.transactions)},
			{Name: "errors", Value: fmt.Sprintf("%d", in.errors)},
			{Name: "tps", Value: fmt.Sprintf("%.2f", in.tps)},
			{Name: "duration_ms", Value: fmt.Sprintf("%d", in.duration.Milliseconds())},
		},
	}

	// Execution testcase: a transport/runtime error is an "error" (the test
	// could not be evaluated), distinct from an SLA "failure".
	exec := junitTestcase{Name: "execution", Classname: classname, Time: seconds}
	if in.runErr != nil {
		exec.Error = &junitFailure{
			Message: truncateMessage(in.runErr.Error()),
			Type:    "ExecutionError",
			Body:    in.runErr.Error(),
		}
	}
	suite.Cases = append(suite.Cases, exec)

	// SLA testcase: present only when thresholds were evaluated. A breach is a
	// failure with all breached details in the body so CI surfaces them inline.
	if in.sla != nil && in.sla.Evaluated {
		sla := junitTestcase{Name: "sla", Classname: classname, Time: "0"}
		if in.sla.Breached {
			sla.Failure = &junitFailure{
				Message: truncateMessage(strings.Join(in.sla.Details, "; ")),
				Type:    "SLABreach",
				Body:    strings.Join(in.sla.Details, "\n"),
			}
		}
		suite.Cases = append(suite.Cases, sla)
	} else {
		suite.Cases = append(suite.Cases, junitTestcase{
			Name:      "sla",
			Classname: classname,
			Time:      "0",
			Skipped:   &junitSkipped{Message: "no SLA thresholds configured"},
		})
	}

	suite.Tests = len(suite.Cases)
	for _, c := range suite.Cases {
		switch {
		case c.Error != nil:
			suite.Errors++
		case c.Failure != nil:
			suite.Failures++
		case c.Skipped != nil:
			suite.Skipped++
		}
	}

	return junitTestsuites{
		Name:     "apexload",
		Tests:    suite.Tests,
		Failures: suite.Failures,
		Errors:   suite.Errors,
		Time:     seconds,
		Suites:   []junitTestsuite{suite},
	}
}

func marshalJUnit(report junitTestsuites) ([]byte, error) {
	body, err := xml.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

func writeJUnitReport(path string, in junitInput) error {
	report := buildJUnitReport(in)
	body, err := marshalJUnit(report)
	if err != nil {
		return fmt.Errorf("marshal junit: %w", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write junit %s: %w", path, err)
	}
	return nil
}

// writeRunJUnit adapts either run path (local engine result or API result) into
// a single JUnit artifact so pipelines get identical output regardless of how
// the test executed.
func writeRunJUnit(args runArgs, script *dsl.Script, result *engine.RunResult, apiResult *ApiTestResult, sla *SLASummary, runErr error) error {
	in := junitInput{
		suiteName: junitSuiteName(args, script),
		classname: "apexload",
		sla:       sla,
		runErr:    runErr,
	}
	switch {
	case result != nil:
		in.duration = result.Duration
		in.transactions = result.TotalTransactions
		in.errors = result.TotalErrors
		in.tps = result.FinalSnapshot.TPS
	case apiResult != nil:
		in.duration = apiResult.Duration
		in.transactions = apiResult.TotalTransactions
		in.errors = apiResult.TotalErrors
		in.tps = apiResult.FinalSnapshot.TPS
	}
	return writeJUnitReport(args.junit, in)
}

// junitSuiteName prefers the script's declared name, falling back to the script
// file's base name so reports are identifiable in CI test dashboards.
func junitSuiteName(args runArgs, script *dsl.Script) string {
	if script != nil && strings.TrimSpace(script.Name) != "" {
		return "apexload:" + strings.TrimSpace(script.Name)
	}
	if args.script != "" {
		base := filepath.Base(args.script)
		base = strings.TrimSuffix(base, filepath.Ext(base))
		if base != "" {
			return "apexload:" + base
		}
	}
	return "apexload"
}

// truncateMessage keeps failure-message attributes readable in CI summaries.
func truncateMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	const max = 240
	if len(msg) <= max {
		return msg
	}
	return msg[:max-1] + "…"
}
