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
	"time"
)

func elasticCmd() {
	if err := executeElasticCommand(context.Background(), os.Args[2:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
}

// executeElasticCommand uses the same project-scoped contracts as the dashboard.
// Files contain logical secret references; responses never disclose credentials.
func executeElasticCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: apexctl elastic <list|create|get|update|delete|test|inspect|operation|cancel> --project ID [--id ID] [--file JSON]")
	}
	action := args[0]
	fs := flag.NewFlagSet("elastic "+action, flag.ContinueOnError)
	fs.SetOutput(out)
	endpoint := fs.String("api", "http://127.0.0.1:8080", "controller URL")
	project := fs.String("project", "", "project ID")
	id := fs.String("id", "", "connection ID")
	file := fs.String("file", "", "connection or inspection request JSON file")
	opID := fs.String("operation", "", "inspection operation ID")
	kind := fs.String("kind", "verify", "verify, discover_indices, discover_fields, or preview")
	revision := fs.Int64("revision", 0, "saved connection revision for inspection")
	wait := fs.Bool("wait", false, "wait for inspection completion")
	timeout := fs.Duration("timeout", 2*time.Minute, "inspection polling timeout, at most 15 minutes")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *project == "" || fs.NArg() != 0 {
		return errors.New("--project is required; positional arguments are not accepted")
	}
	if *wait && (action != "inspect" || *timeout <= 0 || *timeout > 15*time.Minute) {
		return errors.New("--wait requires inspect and a timeout greater than zero and at most 15 minutes")
	}
	base := "/api/v1/projects/" + url.PathEscape(*project) + "/monitoring/elk-connections"
	method, route := http.MethodGet, base
	var body []byte
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		body, err = io.ReadAll(io.LimitReader(f, (96<<10)+1))
		if err != nil {
			return err
		}
		if len(body) > 96<<10 || !json.Valid(body) {
			return errors.New("request must be valid JSON no larger than 96 KiB")
		}
	}
	switch action {
	case "list":
	case "create":
		method = http.MethodPost
		if len(body) == 0 {
			return errors.New("--file is required")
		}
	case "get", "update", "delete", "test", "inspect", "operation", "cancel":
		if *id == "" {
			return errors.New("--id is required")
		}
		route += "/" + url.PathEscape(*id)
		switch action {
		case "update":
			method = http.MethodPut
			if len(body) == 0 {
				return errors.New("--file is required, including updated_at or revision")
			}
		case "delete":
			method = http.MethodDelete
		case "test":
			method = http.MethodPost
			route += "/test"
		case "inspect":
			method = http.MethodPost
			route += "/inspections"
			if len(body) == 0 {
				if *revision < 1 {
					return errors.New("--revision is required")
				}
				switch *kind {
				case "verify", "discover_indices":
				case "discover_fields", "preview":
					return errors.New("--file is required for field discovery or preview")
				default:
					return errors.New("unsupported inspection --kind")
				}
				body, _ = json.Marshal(map[string]any{"kind": *kind, "expected_revision": *revision})
			}
		case "operation", "cancel":
			if *opID == "" {
				return errors.New("--operation is required")
			}
			route += "/inspections/" + url.PathEscape(*opID)
			if action == "cancel" {
				method = http.MethodDelete
			}
		}
	default:
		return errors.New("unsupported Elastic action")
	}
	client := NewApiClient(*endpoint)
	data, err := client.workflowRequest(ctx, method, route, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	if *wait {
		var op struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if json.Unmarshal(data, &op) != nil || op.ID == "" {
			return errors.New("invalid Elastic inspection response")
		}
		pollCtx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		for op.Status == "pending" || op.Status == "running" {
			timer := time.NewTimer(time.Second)
			select {
			case <-pollCtx.Done():
				timer.Stop()
				return pollCtx.Err()
			case <-timer.C:
			}
			data, err = client.workflowRequest(pollCtx, http.MethodGet, base+"/"+url.PathEscape(*id)+"/inspections/"+url.PathEscape(op.ID), nil, "")
			if err != nil {
				return err
			}
			if json.Unmarshal(data, &op) != nil {
				return errors.New("invalid inspection status")
			}
		}
		if op.Status != "complete" {
			fmt.Fprintln(out, string(data))
			return fmt.Errorf("Elastic inspection %s", op.Status)
		}
	}
	if len(data) > 0 {
		_, err = fmt.Fprintln(out, string(data))
	}
	return err
}
