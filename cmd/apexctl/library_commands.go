package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/api"
)

const maxCLILibraryBundleBytes = 64 << 20

type libraryArgs struct {
	err                 error
	help                bool
	api                 string
	apiKey              string
	bearerToken         string
	kind                string
	id                  string
	projectID           string
	applicationID       string
	releaseID           string
	file                string
	output              string
	format              string
	includeDependencies bool
}

func libraryCmd() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, libraryUsage())
		os.Exit(exitCodeUsageOrConfiguration)
	}
	subcommand := strings.ToLower(strings.TrimSpace(os.Args[2]))
	args := parseLibraryArgs(subcommand, os.Args[3:])
	if args.help && args.err == nil {
		fmt.Println(libraryUsage())
		return
	}
	if args.err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", args.err)
		os.Exit(exitCodeUsageOrConfiguration)
	}
	client := NewApiClientWithAuth(args.api, args.apiKey, args.bearerToken)
	switch subcommand {
	case "export":
		bundle, err := client.ExportLibraryBundle(context.Background(), api.LibraryTransferRequest{
			Kind: args.kind, ID: args.id, ProjectID: args.projectID, IncludeDependencies: args.includeDependencies,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error exporting library bundle: %v\n", err)
			os.Exit(exitCodeControllerOrTransport)
		}
		encoded, err := json.MarshalIndent(bundle, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error encoding library bundle: %v\n", err)
			os.Exit(exitCodeControllerOrTransport)
		}
		encoded = append(encoded, '\n')
		if args.output == "" {
			_, _ = os.Stdout.Write(encoded)
			return
		}
		if err := writeCLILibraryBundle(args.output, encoded); err != nil {
			fmt.Fprintf(os.Stderr, "error writing library bundle: %v\n", err)
			os.Exit(exitCodeControllerOrTransport)
		}
		if args.format == "json" {
			writeCLIJSON(map[string]any{"status": "exported", "bundle_id": bundle.BundleID, "items": len(bundle.Items), "output": args.output})
		} else {
			fmt.Printf("Exported bundle %s (%d item(s)) to %s\n", bundle.BundleID, len(bundle.Items), args.output)
		}
	case "import":
		bundle, err := readCLILibraryBundle(args.file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading library bundle: %v\n", err)
			os.Exit(exitCodeUsageOrConfiguration)
		}
		result, conflicted, err := client.ImportLibraryBundle(context.Background(), api.LibraryImportRequest{
			Bundle: bundle, ProjectID: args.projectID, ApplicationID: args.applicationID, ReleaseID: args.releaseID,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error importing library bundle: %v\n", err)
			os.Exit(exitCodeControllerOrTransport)
		}
		if args.format == "json" {
			writeCLIJSON(result)
		} else {
			fmt.Printf("Imported %d item(s); %d conflict(s)\n", len(result.Items), len(result.Conflicts))
			for _, conflict := range result.Conflicts {
				fmt.Printf("  - %s %s: %s\n", conflict.Kind, conflict.SourceItemID, conflict.Reason)
			}
		}
		if conflicted {
			os.Exit(exitCodeTestFailure)
		}
	default:
		fmt.Fprintf(os.Stderr, "error: unknown library command %q\n", subcommand)
		os.Exit(exitCodeUsageOrConfiguration)
	}
}

func parseLibraryArgs(subcommand string, values []string) libraryArgs {
	args := libraryArgs{
		api: strings.TrimSpace(os.Getenv("APEXLOAD_API")), apiKey: strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY")),
		bearerToken: strings.TrimSpace(os.Getenv("APEXLOAD_BEARER_TOKEN")), format: "text", includeDependencies: true,
	}
	flags := flag.NewFlagSet("apexctl library "+subcommand, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&args.help, "help", false, "show help")
	flags.BoolVar(&args.help, "h", false, "show help")
	flags.StringVar(&args.api, "api", args.api, "controller URL")
	flags.StringVar(&args.apiKey, "api-key", args.apiKey, "API key")
	flags.StringVar(&args.bearerToken, "bearer-token", args.bearerToken, "bearer token")
	flags.StringVar(&args.kind, "kind", "", "library item kind")
	flags.StringVar(&args.id, "id", "", "library item ID")
	flags.StringVar(&args.projectID, "project", "", "project ID")
	flags.StringVar(&args.applicationID, "application", "", "destination application ID")
	flags.StringVar(&args.releaseID, "release", "", "destination release ID")
	flags.StringVar(&args.file, "file", "", "bundle input file")
	flags.StringVar(&args.output, "output", "", "bundle output file")
	flags.StringVar(&args.format, "format", "text", "output format")
	flags.BoolVar(&args.includeDependencies, "include-dependencies", true, "include transitive dependencies")
	if err := flags.Parse(values); err != nil {
		args.err = err
	} else if flags.NArg() != 0 {
		args.err = fmt.Errorf("unexpected argument %q", flags.Arg(0))
	} else if subcommand != "export" && subcommand != "import" {
		args.err = fmt.Errorf("library command must be export or import")
	} else if strings.TrimSpace(args.api) == "" {
		args.err = fmt.Errorf("--api is required")
	} else if err := validateCLIControllerURL(args.api); err != nil {
		args.err = err
	} else if err := validateCLIOutputFormat(args.format); err != nil {
		args.err = err
	} else if subcommand == "export" && !validCLILibraryKind(args.kind) {
		args.err = fmt.Errorf("--kind must be script, scenario, or result")
	} else if subcommand == "export" && strings.TrimSpace(args.id) == "" {
		args.err = fmt.Errorf("--id is required for library export")
	} else if subcommand == "import" && strings.TrimSpace(args.projectID) == "" {
		args.err = fmt.Errorf("--project is required for library import")
	} else if subcommand == "import" && strings.TrimSpace(args.file) == "" {
		args.err = fmt.Errorf("--file is required for library import")
	}
	args.api = strings.TrimRight(strings.TrimSpace(args.api), "/")
	args.apiKey = strings.TrimSpace(args.apiKey)
	args.bearerToken = strings.TrimSpace(args.bearerToken)
	args.kind = strings.ToLower(strings.TrimSpace(args.kind))
	args.id = strings.TrimSpace(args.id)
	args.projectID = strings.TrimSpace(args.projectID)
	args.applicationID = strings.TrimSpace(args.applicationID)
	args.releaseID = strings.TrimSpace(args.releaseID)
	args.file = strings.TrimSpace(args.file)
	args.output = strings.TrimSpace(args.output)
	args.format = strings.ToLower(strings.TrimSpace(args.format))
	return args
}

func validCLILibraryKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "script", "scenario", "result":
		return true
	default:
		return false
	}
}

func readCLILibraryBundle(path string) (api.LibraryBundle, error) {
	var bundle api.LibraryBundle
	file, err := os.Open(path)
	if err != nil {
		return bundle, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCLILibraryBundleBytes+1))
	if err != nil {
		return bundle, err
	}
	if len(data) > maxCLILibraryBundleBytes {
		return bundle, fmt.Errorf("bundle exceeds %d bytes", maxCLILibraryBundleBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return bundle, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return bundle, fmt.Errorf("bundle JSON contains trailing values")
		}
		return bundle, fmt.Errorf("decode trailing bundle JSON: %w", err)
	}
	return bundle, nil
}

func writeCLILibraryBundle(path string, data []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	// OpenFile's mode applies only when a file is created. Explicitly tighten an
	// existing output before truncating it because portable library bundles may
	// contain governed configuration or secret references.
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func (c *ApiClient) ExportLibraryBundle(ctx context.Context, request api.LibraryTransferRequest) (*api.LibraryBundle, error) {
	var bundle api.LibraryBundle
	if err := c.libraryJSONRequest(ctx, "/api/v1/library/export", request, http.StatusOK, &bundle); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func (c *ApiClient) ImportLibraryBundle(ctx context.Context, request api.LibraryImportRequest) (*api.LibraryImportResponse, bool, error) {
	var result api.LibraryImportResponse
	status, err := c.libraryJSONRequestStatuses(ctx, "/api/v1/library/import", request, map[int]bool{http.StatusCreated: true, http.StatusConflict: true}, &result)
	if err != nil {
		return nil, false, err
	}
	return &result, status == http.StatusConflict, nil
}

func (c *ApiClient) libraryJSONRequest(ctx context.Context, path string, request any, wantStatus int, response any) error {
	_, err := c.libraryJSONRequestStatuses(ctx, path, request, map[int]bool{wantStatus: true}, response)
	return err
}

func (c *ApiClient) libraryJSONRequestStatuses(ctx context.Context, path string, request any, statuses map[int]bool, response any) (int, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return 0, err
	}
	resp, err := c.doContext(ctx, http.MethodPost, path, bytes.NewReader(body), "application/json")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if !statuses[resp.StatusCode] {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, &controllerHTTPStatusError{operation: "library request", status: resp.Status, statusCode: resp.StatusCode, detail: strings.TrimSpace(string(detail))}
	}
	if err := decodeBoundedCLIJSON(resp.Body, maxCLILibraryBundleBytes, response); err != nil {
		return resp.StatusCode, fmt.Errorf("decode library response: %w", err)
	}
	return resp.StatusCode, nil
}

func decodeBoundedCLIJSON(reader io.Reader, maxBytes int64, destination any) error {
	if maxBytes <= 0 {
		return fmt.Errorf("JSON response limit must be positive")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("JSON response exceeds %d bytes", maxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON response contains trailing values")
		}
		return fmt.Errorf("decode trailing JSON response: %w", err)
	}
	return nil
}

func writeCLIJSON(value any) {
	encoded, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(encoded))
}

func libraryUsage() string {
	return `Usage:
  apexctl library export --api <url> --kind script|scenario|result --id <id> [--project <id>] [--output bundle.json]
  apexctl library import --api <url> --project <id> --file bundle.json [--application <id>] [--release <id>]

Both commands accept --api-key, --bearer-token, and --format text|json.`
}
