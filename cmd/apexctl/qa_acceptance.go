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
		if errors.Is(err, errAcceptanceUnsupported) {
			os.Exit(exitCodeTestFailure)
		}
		os.Exit(exitCodeUsageOrConfiguration)
	}
}

// errAcceptanceUnsupported is the --require-supported gate's failure: the
// command worked, but the queried subjects are not all supported.
var errAcceptanceUnsupported = errors.New("acceptance gate: not every matching subject is supported")

// qaActionFlags are the flags each action accepts; any other flag is refused
// rather than silently ignored.
var qaActionFlags = map[string]map[string]bool{
	"acceptance":        {"api": true, "requirement": true, "feature": true, "profile": true, "provider": true, "platform": true, "commit": true, "binary": true, "require-supported": true, "cohort": true},
	"record-acceptance": {"api": true, "requirement": true, "feature": true, "profile": true, "provider": true, "platform": true, "commit": true, "binary": true, "suite": true, "result": true, "notes": true, "artifact": true, "qualification": true},
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
	cohort := fs.String("cohort", "", "acceptance: exact returned browser cohort SHA-256")
	qualification := fs.String("qualification", "", "path of a browser cohort/dependency JSON object (at most 16 KiB)")
	notes := fs.String("notes", "", "short notes (at most 256 bytes)")
	requireSupported := fs.Bool("require-supported", false, "acceptance: fail (exit 1) unless at least one subject matches and every match is supported")
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
	allowed, known := qaActionFlags[action]
	if !known {
		return errors.New(usage)
	}
	var refused []string
	fs.Visit(func(set *flag.Flag) {
		if !allowed[set.Name] {
			refused = append(refused, "--"+set.Name)
		}
	})
	if len(refused) > 0 {
		return fmt.Errorf("qa %s does not accept %s", action, strings.Join(refused, ", "))
	}
	client := NewApiClient(*endpoint)
	switch action {
	case "acceptance":
		query := url.Values{}
		for name, value := range map[string]string{"requirement": *requirement, "feature": *feature, "profile": *profile, "provider": *provider, "platform": *platform, "commit_sha": strings.ToLower(*commit), "cohort_sha256": *cohort} {
			if value != "" {
				query.Set(name, value)
			}
		}
		if *binary != "" {
			digest, err := fileSHA256(*binary)
			if err != nil {
				return fmt.Errorf("hash binary: %w", err)
			}
			query.Set("binary_sha256", digest)
		}
		path := "/api/v1/system/acceptance-status"
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}
		data, err := client.workflowRequest(ctx, http.MethodGet, path, nil, "")
		if err != nil {
			return err
		}
		if _, err := out.Write(append(data, '\n')); err != nil {
			return err
		}
		if !*requireSupported {
			return nil
		}
		var status struct {
			Statuses []struct {
				Supported bool `json:"supported"`
			} `json:"statuses"`
			Truncated bool `json:"truncated"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			return fmt.Errorf("decode acceptance status: %w", err)
		}
		if len(status.Statuses) == 0 || status.Truncated {
			return errAcceptanceUnsupported
		}
		for _, subject := range status.Statuses {
			if !subject.Supported {
				return errAcceptanceUnsupported
			}
		}
		return nil
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
		if *qualification != "" {
			data, err := readBrowserAcceptanceQualification(*qualification)
			if err != nil {
				return err
			}
			request["qualification"] = data
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

// Keep the original object bytes for the server's strict duplicate/unknown-key
// validator. Parsing into a map here would silently collapse duplicate keys.
func readBrowserAcceptanceQualification(path string) (json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open qualification: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return nil, errors.New("qualification must be a regular JSON file at most 16 KiB")
	}
	body, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 16<<10 {
		return nil, errors.New("qualification exceeds 16 KiB")
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || len(body) > 16<<10 || body[0] != '{' || !json.Valid(body) {
		return nil, errors.New("qualification must be a JSON object at most 16 KiB")
	}
	return json.RawMessage(body), nil
}
