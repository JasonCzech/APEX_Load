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

func appDynamicsCmd() {
	if err := executeAppDynamicsCommand(context.Background(), os.Args[2:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
}
func executeAppDynamicsCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: apexctl appdynamics <list|create|get|update|delete|test|discover|operation> --project ID [--id ID] [--file JSON]")
	}
	action := args[0]
	fs := flag.NewFlagSet("appdynamics "+action, flag.ContinueOnError)
	fs.SetOutput(out)
	endpoint := fs.String("api", "http://127.0.0.1:8080", "controller URL")
	project := fs.String("project", "", "project ID")
	id := fs.String("id", "", "connection ID")
	file := fs.String("file", "", "connection request JSON file")
	operation := fs.String("operation", "", "inspection operation ID")
	revision := fs.Int64("revision", 0, "current connection revision required for delete")
	kind := fs.String("kind", "applications", "applications, tiers, nodes, backends, business-transactions or metrics")
	application := fs.String("application-id", "", "AppDynamics application numeric ID")
	path := fs.String("metric-path", "", "metric folder path")
	mode := fs.String("collection-mode", "", "direct or collector")
	collector := fs.String("collector-id", "", "assigned collector ID")
	key := fs.String("collector-public-key", "", "enrolled collector X25519 public key")
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
	if *mode != "" && *mode != "direct" && *mode != "collector" {
		return errors.New("--collection-mode must be direct or collector")
	}
	if *wait && (*timeout <= 0 || *timeout > time.Minute) {
		return errors.New("--timeout must be greater than 0 and at most 1 minute")
	}
	base := "/api/v1/projects/" + url.PathEscape(*project) + "/monitoring/appdynamics-connections"
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
	case "get", "update", "delete", "test", "discover", "operation":
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
			route += "?revision=" + strconv.FormatInt(*revision, 10)
		case "test":
			method = http.MethodPost
			route += "/test"
			if len(body) == 0 {
				body, _ = json.Marshal(map[string]string{"collection_mode": *mode, "collector_agent_id": *collector, "collector_public_key": *key})
			}
		case "discover":
			switch *kind {
			case "applications", "tiers", "nodes", "backends", "business-transactions", "metrics":
			default:
				return errors.New("unsupported --kind")
			}
			if *kind != "applications" && *application == "" {
				return errors.New("--application-id is required for this discovery kind")
			}
			route += "/" + *kind
			q := url.Values{}
			for k, v := range map[string]string{"application_id": *application, "metric_path": *path, "collection_mode": *mode, "collector_agent_id": *collector, "collector_public_key": *key} {
				if v != "" {
					q.Set(k, v)
				}
			}
			if len(q) > 0 {
				route += "?" + q.Encode()
			}
		case "operation":
			if *operation == "" {
				return errors.New("--operation is required")
			}
			route += "/operations/" + url.PathEscape(*operation)
		}
	default:
		return errors.New("unsupported AppDynamics action")
	}
	client := NewApiClient(*endpoint)
	data, err := client.workflowRequest(ctx, method, route, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	if *wait && (action == "test" || action == "discover") {
		var op struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err = json.Unmarshal(data, &op); err != nil || op.ID == "" {
			return errors.New("invalid inspection operation response")
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
			return fmt.Errorf("AppDynamics inspection %s", op.Status)
		}
	}
	if len(data) > 0 {
		_, err = fmt.Fprintln(out, string(data))
	}
	return err
}
