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
	"time"
)

func dynatraceCmd() {
	if err := executeDynatraceCommand(context.Background(), os.Args[2:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
}

func executeDynatraceCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: apexctl dynatrace <list|create|get|update|delete|test|discover|preview|operation|sources|create-source|update-source|delete-source|bindings|bind|series|evidence> [--project ID] [--id ID] [--file JSON]")
	}
	action := args[0]
	switch action {
	case "sources", "create-source", "update-source", "delete-source", "bindings", "bind", "create-run", "run", "series", "evidence", "dependencies":
		return executeWorkflowCommand(ctx, "monitor", args, out)
	}
	fs := flag.NewFlagSet("dynatrace "+action, flag.ContinueOnError)
	fs.SetOutput(out)
	endpoint := fs.String("api", "http://127.0.0.1:8080", "controller URL")
	project := fs.String("project", "", "project ID")
	id := fs.String("id", "", "connection ID")
	sourceID := fs.String("source-id", "", "approved source ID for an editor-accessible preview; do not combine with --file")
	file := fs.String("file", "", "connection JSON or preview selection JSON file; credentials are project-secret references")
	revision := fs.Int64("revision", 0, "connection revision required for deletion")
	operation := fs.String("operation", "", "inspection operation ID")
	kind := fs.String("kind", "metrics", "metrics, entities, entity_types or smartscape")
	selector := fs.String("selector", "", "discovery metric/entity selector")
	search := fs.String("search", "", "bounded discovery search")
	cursor := fs.String("cursor", "", "discovery pagination cursor")
	limit := fs.Int("limit", 100, "maximum discovery records (1-100)")
	wait := fs.Bool("wait", false, "wait for bounded inspection result")
	timeout := fs.Duration("timeout", time.Minute, "inspection polling timeout (maximum 1 minute)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *project == "" || fs.NArg() != 0 {
		return errors.New("--project is required and positional arguments are not accepted")
	}
	if *sourceID != "" && action != "preview" {
		return errors.New("--source-id is only supported for preview")
	}
	if *wait && (*timeout <= 0 || *timeout > time.Minute) {
		return errors.New("--timeout must be greater than 0 and at most 1 minute")
	}
	base := "/api/v1/projects/" + url.PathEscape(*project) + "/monitoring/dynatrace-connections"
	route, method := base, http.MethodGet
	var body []byte
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		body, err = io.ReadAll(io.LimitReader(f, (64<<10)+1))
		if err != nil {
			return err
		}
		if len(body) > 64<<10 || !json.Valid(body) {
			return errors.New("request must be valid JSON no larger than 64 KiB")
		}
	}
	switch action {
	case "list":
	case "create":
		method = http.MethodPost
		if len(body) == 0 {
			return errors.New("--file is required")
		}
	case "get", "update", "delete", "test", "discover", "preview", "operation":
		if *id == "" {
			return errors.New("--id is required")
		}
		route += "/" + url.PathEscape(*id)
		switch action {
		case "update":
			method = http.MethodPut
			if len(body) == 0 {
				return errors.New("--file is required")
			}
		case "delete":
			method = http.MethodDelete
			if *revision < 1 {
				return errors.New("--revision is required for delete")
			}
			route += "?expected_revision=" + strconv.FormatInt(*revision, 10)
		case "operation":
			if *operation == "" {
				return errors.New("--operation is required")
			}
			route += "/operations/" + url.PathEscape(*operation)
		case "test", "discover", "preview":
			method = http.MethodPost
			route += "/operations"
			request := map[string]any{"kind": "verify"}
			if action == "discover" {
				switch *kind {
				case "metrics", "entities", "entity_types", "smartscape":
				default:
					return errors.New("unsupported --kind")
				}
				if *limit < 1 || *limit > 100 {
					return errors.New("--limit must be between 1 and 100")
				}
				request = map[string]any{"kind": "discover", "options": map[string]any{"kind": *kind, "selector": *selector, "search": *search, "cursor": *cursor, "limit": *limit}}
			}
			if action == "preview" {
				if *sourceID != "" {
					if len(body) != 0 {
						return errors.New("--source-id and --file cannot be combined")
					}
					request = map[string]any{"kind": "preview", "source_id": *sourceID}
				} else {
					if len(body) == 0 {
						return errors.New("--source-id or --file selection JSON is required for preview")
					}
					request = map[string]any{"kind": "preview", "selection": json.RawMessage(body)}
				}
			}
			body, _ = json.Marshal(request)
		}
	default:
		return errors.New("unsupported Dynatrace action")
	}
	client := NewApiClient(*endpoint)
	data, err := client.workflowRequest(ctx, method, route, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	if *wait && (action == "test" || action == "discover" || action == "preview") {
		var op struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if json.Unmarshal(data, &op) != nil || op.ID == "" {
			return errors.New("invalid Dynatrace inspection response")
		}
		pollCtx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		for op.Status == "pending" || op.Status == "queued" || op.Status == "running" {
			timer := time.NewTimer(time.Second)
			select {
			case <-pollCtx.Done():
				timer.Stop()
				return pollCtx.Err()
			case <-timer.C:
			}
			data, err = client.workflowRequest(pollCtx, http.MethodGet, base+"/"+url.PathEscape(*id)+"/operations/"+url.PathEscape(op.ID), nil, "")
			if err != nil {
				return err
			}
			if json.Unmarshal(data, &op) != nil {
				return errors.New("invalid Dynatrace inspection status")
			}
		}
		if op.Status != "complete" {
			fmt.Fprintln(out, string(data))
			return fmt.Errorf("Dynatrace inspection %s", op.Status)
		}
	}
	if len(data) > 0 {
		_, err = fmt.Fprintln(out, string(data))
	}
	return err
}
