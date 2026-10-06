package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
)

func qaCmd() {
	if err := executeQACommand(context.Background(), os.Args[2:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
}

type stringListFlag []string

func (f *stringListFlag) String() string     { return strings.Join(*f, ",") }
func (f *stringListFlag) Set(v string) error { *f = append(*f, v); return nil }

// executeQACommand records and reads feature acceptance evidence. Artifact
// and binary hashes are computed here from local files, so the controller
// never receives the evidence bytes themselves.
func executeQACommand(ctx context.Context, args []string, out io.Writer) error {
	usage := "usage: apexctl qa <record-acceptance|acceptance> --api URL [flags]"
	if len(args) == 0 {
		return errors.New(usage)
	}
	action := args[0]
	fs := flag.NewFlagSet("qa "+action, flag.ContinueOnError)
	fs.SetOutput(out)
	endpoint := fs.String("api", "http://127.0.0.1:8080", "controller URL")
	requirement := fs.String("requirement", "", "requirement ID, for example BE-4")
	feature := fs.String("feature", "", "feature slug")
	profile := fs.String("profile", "", "profile slug")
	provider := fs.String("provider", "", "provider slug")
	platform := fs.String("platform", "", "platform slug, for example linux-amd64")
	commit := fs.String("commit", "", "commit SHA the suite ran against")
	binary := fs.String("binary", "", "path of the tested binary; its SHA-256 is recorded")
	suite := fs.String("suite", "", "suite name")
	result := fs.String("result", "", "passed, failed, pending or revoked")
	notes := fs.String("notes", "", "short notes")
	var artifacts stringListFlag
	fs.Var(&artifacts, "artifact", "path of a sanitized evidence file; repeatable")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("positional arguments are not accepted")
	}
	client := NewApiClient(*endpoint)
	switch action {
	case "acceptance":
		query := url.Values{}
		if *requirement != "" {
			query.Set("requirement", *requirement)
		}
		if *feature != "" {
			query.Set("feature", *feature)
		}
		path := "/api/v1/system/acceptance-status"
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}
		data, err := client.workflowRequest(ctx, http.MethodGet, path, nil, "")
		if err != nil {
			return err
		}
		_, err = out.Write(append(data, '\n'))
		return err
	case "record-acceptance":
		request := map[string]any{
			"requirement_id": *requirement, "feature": *feature, "profile": *profile, "provider": *provider, "platform": *platform,
			"commit_sha": strings.ToLower(*commit), "suite": *suite, "result": *result, "notes": *notes,
		}
		if *binary != "" {
			digest, err := fileSHA256(*binary)
			if err != nil {
				return fmt.Errorf("hash binary: %w", err)
			}
			request["binary_sha256"] = digest
		}
		recorded := []map[string]string{}
		for _, path := range artifacts {
			digest, err := fileSHA256(path)
			if err != nil {
				return fmt.Errorf("hash artifact %s: %w", path, err)
			}
			recorded = append(recorded, map[string]string{"name": filepath.Base(path), "sha256": digest})
		}
		request["artifacts"] = recorded
		body, err := json.Marshal(request)
		if err != nil {
			return err
		}
		data, err := client.workflowRequest(ctx, http.MethodPost, "/api/v1/system/acceptance-records", bytes.NewReader(body), "application/json")
		if err != nil {
			return err
		}
		_, err = out.Write(append(data, '\n'))
		return err
	default:
		return errors.New(usage)
	}
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
