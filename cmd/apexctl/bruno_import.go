package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultCLIImportTimeout = 30 * time.Minute

type brunoImportArgs struct {
	path           string
	projectID      string
	syncScriptID   string
	name           string
	sandbox        string
	api            string
	apiKey         string
	bearerToken    string
	format         string
	timeout        time.Duration
	migrateSecrets bool
	secretBindings map[string]string
	conflicts      map[string]string
}

type brunoCLISecretRequirement struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	Key           string `json:"key"`
	SuggestedName string `json:"suggested_name"`
	Context       string `json:"context"`
}

type brunoCLIInspectResponse struct {
	ImportSessionID string `json:"import_session_id"`
	Result          struct {
		SourceFormat string `json:"source_format"`
		Collection   struct {
			Name string `json:"name"`
		} `json:"collection"`
		Capabilities map[string]any              `json:"capabilities"`
		Fidelity     map[string]any              `json:"fidelity"`
		Secrets      []brunoCLISecretRequirement `json:"secret_requirements"`
		Missing      []string                    `json:"missing_variables"`
		Script       map[string]any              `json:"script"`
	} `json:"result"`
	SyncPreview map[string]any `json:"sync_preview,omitempty"`
}

type brunoCLICommitResponse struct {
	Created bool `json:"created"`
	Script  struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		RefKey  string `json:"ref_key"`
		Version string `json:"version"`
		Config  struct {
			Bruno struct {
				SourceRevision string `json:"source_revision"`
			} `json:"bruno"`
		} `json:"config"`
	} `json:"script"`
	Revision struct {
		ID               string `json:"id"`
		ParentRevisionID string `json:"parent_revision_id"`
	} `json:"revision"`
	ChangeSummary map[string]any `json:"change_summary"`
}

func importCmd() {
	if len(os.Args) >= 3 && strings.TrimSpace(os.Args[2]) == "grafana" {
		grafanaImportCmd()
		return
	}
	if len(os.Args) < 3 || strings.TrimSpace(os.Args[2]) != "bruno" {
		fmt.Fprintln(os.Stderr, "error: import subcommand is required (bruno or grafana)")
		fmt.Fprintln(os.Stderr, brunoImportUsage())
		os.Exit(2)
	}
	args, err := parseBrunoImportArgs(os.Args[3:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n\n%s\n", err, brunoImportUsage())
		os.Exit(2)
	}
	result, err := runBrunoImport(context.Background(), args)
	if err != nil {
		if args.format == "json" {
			writeJSON(map[string]any{"status": "error", "error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "Bruno import failed: %v\n", err)
		}
		os.Exit(1)
	}
	if args.format == "json" {
		writeJSON(map[string]any{
			"status":         "success",
			"script":         result.Script,
			"revision":       result.Revision,
			"created":        result.Created,
			"change_summary": result.ChangeSummary,
		})
		return
	}
	operation := "Imported"
	if args.syncScriptID != "" {
		operation = "Synchronized"
	}
	fmt.Printf("%s Bruno collection %q\n", operation, result.Script.Name)
	fmt.Printf("  Script:   %s\n", result.Script.ID)
	if result.Script.RefKey != "" {
		fmt.Printf("  Ref key:  %s\n", result.Script.RefKey)
	}
	fmt.Printf("  Revision: %s\n", result.Revision.ID)
}

func parseBrunoImportArgs(raw []string) (brunoImportArgs, error) {
	args := brunoImportArgs{
		api:            strings.TrimSpace(os.Getenv("APEXLOAD_API")),
		apiKey:         strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY")),
		bearerToken:    strings.TrimSpace(os.Getenv("APEXLOAD_BEARER_TOKEN")),
		format:         "text",
		timeout:        defaultCLIImportTimeout,
		sandbox:        "safe",
		migrateSecrets: true,
		secretBindings: map[string]string{},
		conflicts:      map[string]string{},
	}
	next := func(index *int, flag string) (string, error) {
		if *index+1 >= len(raw) {
			return "", fmt.Errorf("%s requires a value", flag)
		}
		(*index)++
		return raw[*index], nil
	}
	for index := 0; index < len(raw); index++ {
		switch raw[index] {
		case "--path", "-p":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.path = value
		case "--project":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.projectID = value
		case "--sync":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.syncScriptID = value
		case "--name":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.name = value
		case "--sandbox":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.sandbox = strings.ToLower(strings.TrimSpace(value))
		case "--api":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.api = value
		case "--api-key":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.apiKey = value
		case "--bearer-token":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.bearerToken = value
		case "--format":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.format = strings.ToLower(strings.TrimSpace(value))
		case "--timeout":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.timeout, err = time.ParseDuration(value)
			if err != nil || args.timeout <= 0 {
				return args, fmt.Errorf("--timeout must be a positive duration")
			}
		case "--secret-binding":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			key, secretName, found := strings.Cut(value, "=")
			key, secretName = strings.TrimSpace(key), strings.TrimSpace(secretName)
			if !found || key == "" || secretName == "" {
				return args, fmt.Errorf("--secret-binding must use <requirement-id-or-name>=<logical-secret-name>")
			}
			args.secretBindings[key] = secretName
		case "--conflict-resolution":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			conflictID, resolution, found := strings.Cut(value, "=")
			conflictID, resolution = strings.TrimSpace(conflictID), strings.TrimSpace(resolution)
			if !found || conflictID == "" || resolution == "" {
				return args, fmt.Errorf("--conflict-resolution must use <conflict-id>=<resolution>")
			}
			args.conflicts[conflictID] = resolution
		case "--migrate-secrets":
			args.migrateSecrets = true
		case "--no-migrate-secrets":
			args.migrateSecrets = false
		default:
			return args, fmt.Errorf("unknown Bruno import flag %q", raw[index])
		}
	}
	args.path = strings.TrimSpace(args.path)
	args.projectID = strings.TrimSpace(args.projectID)
	args.syncScriptID = strings.TrimSpace(args.syncScriptID)
	args.name = strings.TrimSpace(args.name)
	args.api = strings.TrimRight(strings.TrimSpace(args.api), "/")
	if args.path == "" {
		return args, errors.New("--path is required")
	}
	if args.projectID == "" {
		return args, errors.New("--project is required")
	}
	if args.api == "" {
		return args, errors.New("--api is required (or set APEXLOAD_API)")
	}
	if args.sandbox != "safe" && args.sandbox != "developer" {
		return args, errors.New("--sandbox must be safe or developer")
	}
	if args.format != "text" && args.format != "json" {
		return args, errors.New("--format must be text or json")
	}
	return args, nil
}

func runBrunoImport(ctx context.Context, args brunoImportArgs) (*brunoCLICommitResponse, error) {
	info, err := os.Stat(args.path)
	if err != nil {
		return nil, fmt.Errorf("inspect import path: %w", err)
	}
	client := NewApiClientWithAuth(args.api, args.apiKey, args.bearerToken)
	client.Client.Timeout = args.timeout
	var inspected *brunoCLIInspectResponse
	if info.IsDir() {
		inspected, err = client.InspectBrunoDirectory(ctx, args)
	} else if info.Mode().IsRegular() {
		inspected, err = client.InspectBrunoFile(ctx, args)
	} else {
		return nil, errors.New("Bruno import path must be a directory or regular file")
	}
	if err != nil {
		return nil, err
	}
	bindings := resolveBrunoCLISecretBindings(inspected.Result.Secrets, args.secretBindings)
	commit := map[string]any{
		"project_id":           args.projectID,
		"import_session_id":    inspected.ImportSessionID,
		"mode":                 "create",
		"migrate_secrets":      args.migrateSecrets,
		"secret_bindings":      bindings,
		"conflict_resolutions": args.conflicts,
	}
	if args.name != "" {
		commit["name"] = args.name
	}
	if args.syncScriptID != "" {
		commit["mode"] = "sync"
		commit["sync_script_id"] = args.syncScriptID
		if updatedAt, ok := inspected.SyncPreview["expected_updated_at"].(string); ok && updatedAt != "" {
			commit["expected_updated_at"] = updatedAt
		}
		if revision, ok := inspected.SyncPreview["active_revision"].(string); ok && revision != "" {
			commit["expected_active_revision_id"] = revision
		}
	}
	return client.CommitBrunoImport(ctx, commit)
}

func (c *ApiClient) InspectBrunoFile(ctx context.Context, args brunoImportArgs) (*brunoCLIInspectResponse, error) {
	file, err := os.Open(args.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	query := url.Values{
		"project_id": {args.projectID},
		"filename":   {filepath.Base(args.path)},
		"sandbox":    {args.sandbox},
	}
	if args.name != "" {
		query.Set("name", args.name)
	}
	if args.syncScriptID != "" {
		query.Set("sync_script_id", args.syncScriptID)
	}
	response, err := c.doContext(ctx, http.MethodPost, "/api/v1/scripts/import/bruno/inspect?"+query.Encode(), file, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, brunoCLIResponseError("inspect Bruno source", response)
	}
	var inspected brunoCLIInspectResponse
	if err := json.NewDecoder(response.Body).Decode(&inspected); err != nil {
		return nil, fmt.Errorf("decode Bruno inspection: %w", err)
	}
	if inspected.ImportSessionID == "" {
		return nil, errors.New("Bruno inspection response is missing import_session_id")
	}
	return &inspected, nil
}

func (c *ApiClient) InspectBrunoDirectory(ctx context.Context, args brunoImportArgs) (*brunoCLIInspectResponse, error) {
	files, err := deterministicBrunoDirectoryFiles(args.path)
	if err != nil {
		return nil, err
	}
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	writeErr := make(chan error, 1)
	go func() {
		err := writeBrunoDirectoryMultipart(multipartWriter, args, files)
		if closeErr := multipartWriter.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = writer.CloseWithError(err)
		} else {
			_ = writer.Close()
		}
		writeErr <- err
		close(writeErr)
	}()
	query := url.Values{"project_id": []string{args.projectID}}
	response, err := c.doContext(ctx, http.MethodPost, "/api/v1/scripts/import/bruno/inspect?"+query.Encode(), reader, multipartWriter.FormDataContentType())
	if err != nil {
		_ = reader.CloseWithError(err)
		<-writeErr
		return nil, err
	}
	defer response.Body.Close()
	_ = reader.Close()
	if response.StatusCode != http.StatusOK {
		<-writeErr
		return nil, brunoCLIResponseError("inspect Bruno directory", response)
	}
	if err := <-writeErr; err != nil {
		return nil, err
	}
	var inspected brunoCLIInspectResponse
	if err := json.NewDecoder(response.Body).Decode(&inspected); err != nil {
		return nil, fmt.Errorf("decode Bruno inspection: %w", err)
	}
	if inspected.ImportSessionID == "" {
		return nil, errors.New("Bruno inspection response is missing import_session_id")
	}
	return &inspected, nil
}

func (c *ApiClient) CommitBrunoImport(ctx context.Context, request map[string]any) (*brunoCLICommitResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	response, err := c.doContext(ctx, http.MethodPost, "/api/v1/scripts/import/bruno/commit", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return nil, brunoCLIResponseError("commit Bruno import", response)
	}
	var committed brunoCLICommitResponse
	if err := json.NewDecoder(response.Body).Decode(&committed); err != nil {
		return nil, fmt.Errorf("decode Bruno commit: %w", err)
	}
	if committed.Script.ID == "" || committed.Revision.ID == "" {
		return nil, errors.New("Bruno commit response is missing script or revision identity")
	}
	return &committed, nil
}

func deterministicBrunoDirectoryFiles(root string) ([]string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.WalkDir(absolute, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Bruno directory contains symlink %q", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Bruno directory contains non-regular file %q", path)
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("Bruno directory contains no files")
	}
	sort.Slice(files, func(i, j int) bool {
		left, _ := filepath.Rel(absolute, files[i])
		right, _ := filepath.Rel(absolute, files[j])
		return filepath.ToSlash(left) < filepath.ToSlash(right)
	})
	return files, nil
}

func writeBrunoDirectoryMultipart(writer *multipart.Writer, args brunoImportArgs, files []string) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "project_id", value: args.projectID},
		{name: "name", value: args.name},
		{name: "sandbox", value: args.sandbox},
		{name: "sync_script_id", value: args.syncScriptID},
	} {
		if field.value != "" {
			if err := writer.WriteField(field.name, field.value); err != nil {
				return err
			}
		}
	}
	absolute, err := filepath.Abs(args.path)
	if err != nil {
		return err
	}
	for _, filename := range files {
		relative, err := filepath.Rel(absolute, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file:%s"; filename="%s"`, url.QueryEscape(relative), escapeMIMEQuoted(filepath.Base(relative))))
		header.Set("Content-Type", "application/octet-stream")
		part, err := writer.CreatePart(header)
		if err != nil {
			return err
		}
		file, err := os.Open(filename)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(part, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func resolveBrunoCLISecretBindings(requirements []brunoCLISecretRequirement, supplied map[string]string) map[string]string {
	bindings := make(map[string]string)
	for _, requirement := range requirements {
		value := strings.TrimSpace(supplied[requirement.ID])
		if value == "" {
			value = strings.TrimSpace(supplied[requirement.SuggestedName])
		}
		if value != "" {
			bindings[requirement.ID] = value
		}
	}
	return bindings
}

func escapeMIMEQuoted(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, `\"`, "\r", "", "\n", "").Replace(value)
}

func brunoCLIResponseError(operation string, response *http.Response) error {
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var decoded struct {
		Error        string                      `json:"error"`
		Code         string                      `json:"code"`
		Requirements []brunoCLISecretRequirement `json:"requirements"`
	}
	if json.Unmarshal(payload, &decoded) == nil && decoded.Error != "" {
		if len(decoded.Requirements) > 0 {
			var missing []string
			for _, requirement := range decoded.Requirements {
				missing = append(missing, requirement.ID+"="+requirement.SuggestedName)
			}
			return fmt.Errorf("%s: %s; bind with --secret-binding <%s>=<logical-secret-name>", operation, decoded.Error, strings.Join(missing, "|"))
		}
		if decoded.Code != "" {
			return fmt.Errorf("%s: %s (%s)", operation, decoded.Error, decoded.Code)
		}
		return fmt.Errorf("%s: %s", operation, decoded.Error)
	}
	return fmt.Errorf("%s returned %s: %s", operation, response.Status, strings.TrimSpace(string(payload)))
}

func brunoImportUsage() string {
	return `Usage:
  apexctl import bruno --api <controller> --path <directory|archive|file> --project <project-id> [options]

Options:
  --sync <script-id>              Synchronize an existing canonical Bruno script
  --name <script-name>            Override the imported collection name
  --sandbox <safe|developer>      Requested JavaScript sandbox (default: safe)
  --migrate-secrets               Migrate imported values when authorized and writable (default)
  --no-migrate-secrets            Require binding-only behavior; never write imported values
  --secret-binding <id>=<name>    Bind an import requirement to an APEX logical secret (repeatable)
  --conflict-resolution <id>=<resolution>
                                   Resolve a synchronization conflict from the inspect preview (repeatable)
  --api-key <key>                 Controller API key (or APEXLOAD_API_KEY)
  --bearer-token <token>          Controller bearer token (or APEXLOAD_BEARER_TOKEN)
  --timeout <duration>            Upload/inspection timeout (default: 30m)
  --format <text|json>            Output format (default: text)`
}
