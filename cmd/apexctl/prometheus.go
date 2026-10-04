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

func prometheusCmd() {
	if err := executePrometheusCommand(context.Background(), os.Args[2:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
}
func executePrometheusCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: apexctl prometheus <list|create|get|update|delete|test|discover|preview|operation|adopt-dashboard> --project ID [--id ID] [--file JSON]")
	}
	action := args[0]
	fs := flag.NewFlagSet("prometheus "+action, flag.ContinueOnError)
	fs.SetOutput(out)
	endpoint := fs.String("api", "http://127.0.0.1:8080", "controller URL")
	project := fs.String("project", "", "project ID")
	id := fs.String("id", "", "connection ID")
	file := fs.String("file", "", "connection or preview request JSON file with project secret references")
	operation := fs.String("operation", "", "inspection operation ID")
	revision := fs.Int64("revision", 0, "current connection revision required for delete")
	kind := fs.String("kind", "metrics", "metrics, labels, label_values, series, targets or metadata")
	selector := fs.String("selector", "", "bounded PromQL discovery selector")
	label := fs.String("label", "", "label for label_values discovery")
	dashboard := fs.String("dashboard", "", "saved Grafana dashboard ID for a resolved monitor draft")
	wait := fs.Bool("wait", false, "wait for inspection result")
	timeout := fs.Duration("timeout", time.Minute, "inspection polling timeout")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *project == "" {
		return errors.New("--project is required and positional arguments are not accepted")
	}
	if *wait && (*timeout <= 0 || *timeout > time.Minute) {
		return errors.New("--timeout must be greater than 0 and at most 1 minute")
	}
	base := "/api/v1/projects/" + url.PathEscape(*project) + "/monitoring/prometheus-connections"
	method := http.MethodGet
	route := base
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
	case "get", "update", "delete", "test", "discover", "preview", "operation", "adopt-dashboard":
		if *id == "" {
			return errors.New("--id is required")
		}
		route += "/" + url.PathEscape(*id)
		switch action {
		case "adopt-dashboard":
			method = http.MethodPost
			route += "/adopt-dashboard"
			if len(body) == 0 {
				if *dashboard == "" {
					return errors.New("--dashboard or --file is required for adoption")
				}
				body, _ = json.Marshal(map[string]string{"dashboard_id": *dashboard})
			}
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
			route += "?revision=" + strconv.FormatInt(*revision, 10)
		case "test", "preview":
			method = http.MethodPost
			route += "/" + action
			if len(body) == 0 {
				if action == "preview" {
					return errors.New("--file with selection is required for preview")
				}
				body = []byte("{}")
			}
		case "discover":
			switch *kind {
			case "metrics", "labels", "label_values", "series", "targets", "metadata":
			default:
				return errors.New("unsupported --kind")
			}
			if *kind == "label_values" && *label == "" {
				return errors.New("--label is required for label_values discovery")
			}
			method = http.MethodPost
			route += "/discover"
			if len(body) == 0 {
				body, _ = json.Marshal(map[string]string{"kind": *kind, "selector": *selector, "label": *label})
			}
		case "operation":
			if *operation == "" {
				return errors.New("--operation is required")
			}
			route += "/operations/" + url.PathEscape(*operation)
		}
	default:
		return errors.New("unsupported Prometheus action")
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
		if err = json.Unmarshal(data, &op); err != nil || op.ID == "" {
			return errors.New("invalid inspection operation response")
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
				return errors.New("invalid inspection status")
			}
		}
		if op.Status != "complete" {
			fmt.Fprintln(out, string(data))
			return fmt.Errorf("Prometheus inspection %s", op.Status)
		}
	}
	if len(data) > 0 {
		_, err = fmt.Fprintln(out, string(data))
	}
	return err
}
