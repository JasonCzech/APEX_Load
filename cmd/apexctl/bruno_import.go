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

const (
	defaultCLIImportTimeout               = 30 * time.Minute
	maximumBrunoCLIImportSnapshotBytes    = int64(64 << 20)
	initialBrunoNativeClaimPollDelay      = 100 * time.Millisecond
	maximumBrunoNativeClaimPollDelay      = time.Second
	maximumBrunoNativeAmbiguousRetries    = 3
	maximumBrunoNativeReceiptRetries      = 1
	brunoImportSessionClaimedResponseCode = "bruno_import_session_claimed"
)

type brunoImportArgs struct {
	path           string
	projectID      string
	syncScriptID   string
	name           string
	sandbox        string
	sandboxSet     bool
	output         string
	folderID       string
	ackReview      bool
	api            string
	apiKey         string
	bearerToken    string
	format         string
	timeout        time.Duration
	migrateSecrets bool
	secretBindings map[string]string
	conflicts      map[string]string
	pathIdentity   os.FileInfo
}

type brunoCLIDirectoryFile struct {
	relativePath string
	data         []byte
}

type brunoCLIScript struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	RefKey    string `json:"ref_key"`
	Version   string `json:"version"`
	Protocol  string `json:"protocol"`
	ProjectID string `json:"project_id"`
	FolderID  string `json:"folder_id"`
	Config    struct {
		Bruno struct {
			SourceRevision string `json:"source_revision"`
		} `json:"bruno"`
	} `json:"config"`
}

type brunoCLIDiagnostic struct {
	ID                      string `json:"id"`
	FindingID               string `json:"finding_id"`
	Code                    string `json:"code"`
	Severity                string `json:"severity"`
	Summary                 string `json:"summary"`
	Message                 string `json:"message"`
	ReviewRequired          bool   `json:"review_required"`
	AcknowledgementRequired bool   `json:"acknowledgement_required"`
}

type brunoCLINativeCandidate struct {
	CandidateID        string                      `json:"candidate_id"`
	PlannedScriptID    string                      `json:"planned_script_id"`
	SourcePath         string                      `json:"source_path"`
	SourceUID          string                      `json:"source_uid"`
	Sequence           int                         `json:"sequence"`
	Fidelity           any                         `json:"fidelity"`
	Diagnostics        []brunoCLIDiagnostic        `json:"diagnostics"`
	FindingIDs         []string                    `json:"finding_ids"`
	ReviewRequired     bool                        `json:"review_required"`
	MissingVariables   []string                    `json:"missing_variables"`
	SecretRequirements []brunoCLISecretRequirement `json:"secret_requirements"`
	Script             map[string]any              `json:"script"`
}

type brunoCLISecretRequirement struct {
	ID                   string `json:"id"`
	Path                 string `json:"path"`
	Key                  string `json:"key"`
	SuggestedName        string `json:"suggested_name"`
	Context              string `json:"context"`
	SourceValueAvailable bool   `json:"source_value_available"`
	ExistingReference    bool   `json:"existing_reference"`
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
		Scripts      []brunoCLINativeCandidate   `json:"scripts"`
	} `json:"result"`
	SyncPreview map[string]any `json:"sync_preview,omitempty"`
}

type brunoCLICommitResponse struct {
	BatchID          string           `json:"batch_id,omitempty"`
	Created          bool             `json:"created"`
	IdempotentReplay bool             `json:"idempotent_replay,omitempty"`
	Count            int              `json:"count,omitempty"`
	Script           brunoCLIScript   `json:"script"`
	Scripts          []brunoCLIScript `json:"scripts,omitempty"`
	Revision         struct {
		ID               string `json:"id"`
		ParentRevisionID string `json:"parent_revision_id"`
	} `json:"revision"`
	ChangeSummary map[string]any            `json:"change_summary"`
	Inspected     []brunoCLINativeCandidate `json:"-"`
	RawResponse   json.RawMessage           `json:"-"`
	HTTPStatus    int                       `json:"-"`
	CreatedSet    bool                      `json:"-"`
	ReplaySet     bool                      `json:"-"`
	CreatedValid  bool                      `json:"-"`
	ReplayValid   bool                      `json:"-"`
}

// brunoCLICommitAmbiguousError marks failures where the controller may have
// durably committed the request even though the client could not observe a
// receipt. Native batches are idempotent by import session and can safely retry
// the identical payload; ordinary HTTP error responses are deliberately not
// marked because the controller definitively rejected those requests.
type brunoCLICommitAmbiguousError struct {
	cause error
}

// brunoCLICommitReceiptError marks a syntactically successful HTTP response
// whose body is not a valid native batch receipt. Retrying the identical
// idempotent request once can recover a truncated intermediary response, but a
// stable incompatible response must fail quickly rather than loop until the
// command timeout.
type brunoCLICommitReceiptError struct {
	cause error
}

type brunoCLIHTTPError struct {
	operation                string
	status                   string
	statusCode               int
	message                  string
	code                     string
	requirements             []brunoCLISecretRequirement
	sessionRetained          bool
	sessionRetainedSet       bool
	manualRetrySafe          bool
	manualRetrySafeSet       bool
	importSessionConsumed    bool
	importSessionConsumedSet bool
	hasRetryMetadata         bool
	idempotencySessionID     string
	committedSecretNames     []string
	retryAfter               time.Duration
}

func (e *brunoCLIHTTPError) Error() string {
	if e == nil {
		return "Bruno request failed"
	}
	message := strings.TrimSpace(e.message)
	if message == "" {
		message = strings.TrimSpace(e.status)
	}
	if e.code != "" {
		message += " (" + e.code + ")"
	}
	if e.sessionRetained {
		message += "; import session retained"
	}
	if e.idempotencySessionID != "" {
		message += "; retry session " + e.idempotencySessionID
	}
	if len(e.committedSecretNames) > 0 {
		message += "; retained secrets: " + strings.Join(e.committedSecretNames, ", ")
	}
	if len(e.requirements) > 0 {
		var missing []string
		for _, requirement := range e.requirements {
			missing = append(missing, requirement.ID+"="+requirement.SuggestedName)
		}
		message += "; bind with --secret-binding <" + strings.Join(missing, "|") + ">=<logical-secret-name>"
	}
	return e.operation + ": " + message
}

type brunoCLIReviewRequiredError struct {
	candidates []brunoCLINativeCandidate
	findings   []string
}

func (e *brunoCLIReviewRequiredError) Error() string {
	if e == nil {
		return "native Bruno findings require acknowledgement"
	}
	var lines []string
	lines = append(lines, fmt.Sprintf(
		"%d native Bruno finding%s require acknowledgement:",
		len(e.findings), pluralSuffix(len(e.findings)),
	))
	printed := make(map[string]struct{}, len(e.findings))
	for _, candidate := range e.candidates {
		for _, diagnostic := range candidate.Diagnostics {
			if !diagnostic.AcknowledgementRequired && !diagnostic.ReviewRequired {
				continue
			}
			id := firstNonEmptyBrunoCLI(diagnostic.FindingID, diagnostic.ID)
			if id != "" {
				printed[id] = struct{}{}
			}
			lines = append(lines, fmt.Sprintf(
				"  %s · %s · %s · %s",
				candidate.SourcePath, id, diagnostic.Code,
				firstNonEmptyBrunoCLI(diagnostic.Message, diagnostic.Summary),
			))
		}
		for _, id := range candidate.FindingIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, exists := printed[id]; exists {
				continue
			}
			printed[id] = struct{}{}
			lines = append(lines, fmt.Sprintf(
				"  %s · %s · review required",
				candidate.SourcePath,
				id,
			))
		}
	}
	for _, id := range e.findings {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := printed[id]; exists {
			continue
		}
		printed[id] = struct{}{}
		lines = append(lines, "  "+id+" · review required")
	}
	lines = append(lines, "Rerun with --ack-review-required after reviewing these findings.")
	return strings.Join(lines, "\n")
}

func firstNonEmptyBrunoCLI(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (e *brunoCLICommitAmbiguousError) Error() string {
	if e == nil || e.cause == nil {
		return "Bruno commit result is ambiguous"
	}
	return e.cause.Error()
}

func (e *brunoCLICommitAmbiguousError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *brunoCLICommitReceiptError) Error() string {
	if e == nil || e.cause == nil {
		return "Bruno commit returned an invalid receipt"
	}
	return e.cause.Error()
}

func (e *brunoCLICommitReceiptError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (response *brunoCLICommitResponse) UnmarshalJSON(data []byte) error {
	type wireResponse brunoCLICommitResponse
	var decoded wireResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*response = brunoCLICommitResponse(decoded)
	response.RawResponse = append(response.RawResponse[:0], data...)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, present := fields["created"]; present {
		response.CreatedSet = true
		response.CreatedValid = string(raw) != "null" && json.Unmarshal(raw, &response.Created) == nil
	}
	if raw, present := fields["idempotent_replay"]; present {
		response.ReplaySet = true
		response.ReplayValid = string(raw) != "null" && json.Unmarshal(raw, &response.IdempotentReplay) == nil
	}
	return nil
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
			payload := map[string]any{"status": "error", "error": err.Error()}
			var reviewErr *brunoCLIReviewRequiredError
			if errors.As(err, &reviewErr) {
				payload["finding_ids"] = reviewErr.findings
				payload["sources"] = brunoCLINativeSourceSummaries(reviewErr.candidates)
				payload["scripts"] = reviewErr.candidates
			}
			writeJSON(payload)
		} else {
			fmt.Fprintf(os.Stderr, "Bruno import failed: %v\n", err)
		}
		os.Exit(1)
	}
	if args.format == "json" {
		if args.output == "native" {
			payload := make(map[string]any)
			if len(result.RawResponse) > 0 {
				_ = json.Unmarshal(result.RawResponse, &payload)
			}
			payload["status"] = "success"
			payload["sources"] = brunoCLINativeSourceSummaries(result.Inspected)
			writeJSON(payload)
			return
		}
		writeJSON(map[string]any{
			"status":         "success",
			"script":         result.Script,
			"revision":       result.Revision,
			"created":        result.Created,
			"change_summary": result.ChangeSummary,
		})
		return
	}
	if args.output == "native" {
		fmt.Printf("Imported %d native Bruno API script%s", result.Count, pluralSuffix(result.Count))
		if result.IdempotentReplay {
			fmt.Print(" (idempotent replay)")
		}
		fmt.Println()
		for index, script := range result.Scripts {
			source := ""
			status := "ready"
			if index < len(result.Inspected) {
				source = result.Inspected[index].SourcePath
				if len(brunoCLIReviewFindingIDs([]brunoCLINativeCandidate{result.Inspected[index]})) > 0 {
					status = "review required"
				} else if len(result.Inspected[index].MissingVariables) > 0 {
					status = "execution blocked: missing variables"
				} else if readiness := brunoCLINativeSecretReadiness(result.Inspected[index], args); readiness != "" {
					status = readiness
				} else if len(result.Inspected[index].Diagnostics) > 0 {
					status = "warning"
				}
			}
			fmt.Printf("  %-28s %s", script.Name, script.ID)
			if script.RefKey != "" {
				fmt.Printf(" (%s)", script.RefKey)
			}
			if source != "" {
				fmt.Printf(" · %s", source)
			}
			fmt.Printf(" · %s\n", status)
		}
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
		output:         "canonical",
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
			args.sandboxSet = true
		case "--output":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.output = strings.ToLower(strings.TrimSpace(value))
		case "--folder":
			value, err := next(&index, raw[index])
			if err != nil {
				return args, err
			}
			args.folderID = strings.TrimSpace(value)
		case "--ack-review-required":
			args.ackReview = true
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
	if args.output != "canonical" && args.output != "native" {
		return args, errors.New("--output must be canonical or native")
	}
	if args.output == "native" {
		if args.folderID == "" {
			return args, errors.New("--folder is required with --output native")
		}
		if args.syncScriptID != "" {
			return args, errors.New("--sync is incompatible with --output native")
		}
		if args.name != "" {
			return args, errors.New("--name is incompatible with --output native; native script names are derived per request")
		}
		if args.sandboxSet {
			return args, errors.New("--sandbox is incompatible with --output native")
		}
		if len(args.conflicts) > 0 {
			return args, errors.New("--conflict-resolution is incompatible with --output native")
		}
	} else {
		if args.folderID != "" {
			return args, errors.New("--folder requires --output native")
		}
		if args.ackReview {
			return args, errors.New("--ack-review-required requires --output native")
		}
	}
	return args, nil
}

func runBrunoImport(ctx context.Context, args brunoImportArgs) (*brunoCLICommitResponse, error) {
	if args.output == "" {
		args.output = "canonical"
	}
	info, err := os.Lstat(args.path)
	if err != nil {
		return nil, fmt.Errorf("inspect import path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Bruno import path must not be a symlink")
	}
	args.pathIdentity = info
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
	requirements := inspected.Result.Secrets
	if args.output == "native" {
		requirements = append(requirements[:0:0], requirements...)
		for _, candidate := range inspected.Result.Scripts {
			requirements = append(requirements, candidate.SecretRequirements...)
		}
		requirements = uniqueBrunoCLISecretRequirements(requirements)
	}
	bindings := resolveBrunoCLISecretBindings(requirements, args.secretBindings)
	if args.output == "native" {
		if err := validateBrunoCLINativeSuppliedBindings(requirements, args.secretBindings); err != nil {
			return nil, err
		}
		candidateIDs := make([]string, 0, len(inspected.Result.Scripts))
		for _, candidate := range inspected.Result.Scripts {
			if strings.TrimSpace(candidate.CandidateID) == "" {
				return nil, fmt.Errorf("native Bruno candidate for %q is missing candidate_id", candidate.SourcePath)
			}
			candidateIDs = append(candidateIDs, candidate.CandidateID)
		}
		if len(candidateIDs) == 0 {
			return nil, errors.New("Bruno inspection returned no native API candidates")
		}
		if err := validateBrunoCLINativeCandidateIdentities(inspected.Result.Scripts); err != nil {
			return nil, err
		}
		requiredFindings := brunoCLIReviewFindingIDs(inspected.Result.Scripts)
		if len(requiredFindings) > 0 && !args.ackReview {
			return nil, &brunoCLIReviewRequiredError{
				candidates: inspected.Result.Scripts,
				findings:   requiredFindings,
			}
		}
		commit := map[string]any{
			"project_id":               args.projectID,
			"import_session_id":        inspected.ImportSessionID,
			"mode":                     "create_native_batch",
			"folder_id":                args.folderID,
			"candidate_ids":            candidateIDs,
			"name_overrides":           map[string]string{},
			"migrate_secrets":          args.migrateSecrets,
			"secret_bindings":          bindings,
			"acknowledged_finding_ids": requiredFindings,
		}
		commitCtx, cancelCommit := context.WithTimeout(ctx, args.timeout)
		defer cancelCommit()
		committed, err := commitBrunoNativeBatchWithRetry(
			commitCtx,
			client,
			commit,
			inspected.Result.Scripts,
		)
		if committed != nil {
			committed.Inspected = orderBrunoCLICandidatesByCommittedScripts(
				inspected.Result.Scripts,
				committed.Scripts,
			)
		}
		return committed, err
	}
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
	if args.output == "native" {
		return c.inspectBrunoNativeFile(ctx, args)
	}
	data, err := readStableBrunoSourceFile(args.path, args.pathIdentity)
	if err != nil {
		return nil, err
	}
	defer clear(data)
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
	response, err := c.doContext(
		ctx,
		http.MethodPost,
		"/api/v1/scripts/import/bruno/inspect?"+query.Encode(),
		bytes.NewReader(data),
		"application/octet-stream",
	)
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

func (c *ApiClient) inspectBrunoNativeFile(ctx context.Context, args brunoImportArgs) (*brunoCLIInspectResponse, error) {
	data, err := readStableBrunoSourceFile(args.path, args.pathIdentity)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	writeErr := make(chan error, 1)
	go func() {
		options, _ := json.Marshal(map[string]string{"output_mode": "native_requests"})
		err := multipartWriter.WriteField("project_id", args.projectID)
		if err == nil {
			err = multipartWriter.WriteField("options", string(options))
		}
		if err == nil {
			var part io.Writer
			part, err = multipartWriter.CreateFormFile("file", filepath.Base(args.path))
			if err == nil {
				_, err = io.Copy(part, bytes.NewReader(data))
			}
		}
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
		return nil, brunoCLIResponseError("inspect Bruno source", response)
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

func (c *ApiClient) InspectBrunoDirectory(ctx context.Context, args brunoImportArgs) (*brunoCLIInspectResponse, error) {
	files, err := deterministicBrunoDirectoryFiles(args.path, args.pathIdentity)
	if err != nil {
		return nil, err
	}
	defer clearBrunoCLIDirectoryFiles(files)
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
		return nil, &brunoCLICommitAmbiguousError{cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return nil, brunoCLIResponseError("commit Bruno import", response)
	}
	var committed brunoCLICommitResponse
	if err := json.NewDecoder(response.Body).Decode(&committed); err != nil {
		return nil, &brunoCLICommitReceiptError{cause: fmt.Errorf("decode Bruno commit receipt: %w", err)}
	}
	committed.HTTPStatus = response.StatusCode
	if mode, _ := request["mode"].(string); mode == "create_native_batch" {
		return &committed, nil
	}
	if committed.Script.ID == "" || committed.Revision.ID == "" {
		return nil, errors.New("Bruno commit response is missing script or revision identity")
	}
	return &committed, nil
}

func commitBrunoNativeBatchWithRetry(
	ctx context.Context,
	client *ApiClient,
	request map[string]any,
	candidates []brunoCLINativeCandidate,
) (*brunoCLICommitResponse, error) {
	return commitBrunoNativeBatchWithRetryAndWait(
		ctx,
		client,
		request,
		candidates,
		waitForBrunoNativeClaimPoll,
	)
}

func commitBrunoNativeBatchWithRetryAndWait(
	ctx context.Context,
	client *ApiClient,
	request map[string]any,
	candidates []brunoCLINativeCandidate,
	wait func(context.Context, time.Duration) error,
) (*brunoCLICommitResponse, error) {
	var committed *brunoCLICommitResponse
	var err error
	ambiguousAttempts := 0
	receiptAttempts := 0
	claimPollAttempts := 0
	expectedSessionID, _ := request["import_session_id"].(string)
	expectedSessionID = strings.TrimSpace(expectedSessionID)
	for {
		committed, err = client.CommitBrunoImport(ctx, request)
		if err == nil {
			if validationErr := validateBrunoCLINativeCommitResponse(committed, candidates); validationErr != nil {
				err = &brunoCLICommitReceiptError{
					cause: fmt.Errorf("validate native Bruno commit receipt: %w", validationErr),
				}
			}
		}
		if err == nil || ctx.Err() != nil {
			return committed, err
		}
		if isBrunoImportSessionClaimedError(err) {
			delay := brunoNativeClaimPollDelay(claimPollAttempts)
			claimPollAttempts++
			if waitErr := wait(ctx, delay); waitErr != nil {
				return committed, waitErr
			}
			continue
		}
		if !isAmbiguousBrunoCommitError(err, expectedSessionID) {
			var receiptErr *brunoCLICommitReceiptError
			if !errors.As(err, &receiptErr) {
				return committed, err
			}
			if receiptAttempts >= maximumBrunoNativeReceiptRetries {
				return committed, err
			}
			delay := brunoNativeClaimPollDelay(receiptAttempts)
			receiptAttempts++
			if waitErr := wait(ctx, delay); waitErr != nil {
				return committed, waitErr
			}
			continue
		}
		if ambiguousAttempts >= maximumBrunoNativeAmbiguousRetries {
			return committed, err
		}
		delay := brunoNativeAmbiguousRetryDelay(err, ambiguousAttempts)
		ambiguousAttempts++
		if waitErr := wait(ctx, delay); waitErr != nil {
			return committed, waitErr
		}
	}
}

func brunoNativeClaimPollDelay(attempt int) time.Duration {
	delay := initialBrunoNativeClaimPollDelay
	for step := 0; step < attempt && delay < maximumBrunoNativeClaimPollDelay; step++ {
		if delay > maximumBrunoNativeClaimPollDelay/2 {
			return maximumBrunoNativeClaimPollDelay
		}
		delay *= 2
	}
	if delay <= 0 || delay > maximumBrunoNativeClaimPollDelay {
		return maximumBrunoNativeClaimPollDelay
	}
	return delay
}

func brunoNativeAmbiguousRetryDelay(err error, attempt int) time.Duration {
	delay := brunoNativeClaimPollDelay(attempt)
	var responseErr *brunoCLIHTTPError
	if errors.As(err, &responseErr) && responseErr.retryAfter > delay {
		return responseErr.retryAfter
	}
	return delay
}

func waitForBrunoNativeClaimPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func validateBrunoCLINativeCandidateIdentities(candidates []brunoCLINativeCandidate) error {
	candidateIDs := make(map[string]struct{}, len(candidates))
	plannedIDs := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateID := strings.TrimSpace(candidate.CandidateID)
		if candidateID == "" {
			return fmt.Errorf("native Bruno candidate for %q is missing candidate_id", candidate.SourcePath)
		}
		if _, exists := candidateIDs[candidateID]; exists {
			return fmt.Errorf("native Bruno inspection contains duplicate candidate_id %q", candidateID)
		}
		candidateIDs[candidateID] = struct{}{}
		plannedID := strings.TrimSpace(candidate.PlannedScriptID)
		if plannedID == "" {
			return fmt.Errorf("native Bruno candidate for %q is missing planned_script_id", candidate.SourcePath)
		}
		if _, exists := plannedIDs[plannedID]; exists {
			return fmt.Errorf("native Bruno inspection contains duplicate planned_script_id %q", plannedID)
		}
		plannedIDs[plannedID] = struct{}{}
	}
	return nil
}

func validateBrunoCLINativeCommitResponse(
	committed *brunoCLICommitResponse,
	candidates []brunoCLINativeCandidate,
) error {
	if committed == nil {
		return errors.New("response is empty")
	}
	if strings.TrimSpace(committed.BatchID) == "" {
		return errors.New("response is missing batch_id")
	}
	if !committed.CreatedSet || !committed.ReplaySet || !committed.CreatedValid || !committed.ReplayValid {
		return errors.New("response must include boolean created and idempotent_replay fields")
	}
	if committed.Created == committed.IdempotentReplay {
		return errors.New("response must identify exactly one of creation or idempotent replay")
	}
	switch committed.HTTPStatus {
	case http.StatusCreated:
		if !committed.Created {
			return errors.New("HTTP 201 response must identify a newly created batch")
		}
	case http.StatusOK:
		if !committed.IdempotentReplay {
			return errors.New("HTTP 200 response must identify an idempotent replay")
		}
	default:
		return fmt.Errorf("response has unsupported HTTP status %d", committed.HTTPStatus)
	}
	if committed.Count != len(committed.Scripts) {
		return fmt.Errorf(
			"response count %d does not match its %d scripts",
			committed.Count,
			len(committed.Scripts),
		)
	}
	if len(committed.Scripts) != len(candidates) {
		return fmt.Errorf(
			"response contains %d of %d selected scripts",
			len(committed.Scripts),
			len(candidates),
		)
	}
	expectedIDs := make(map[string]struct{}, len(candidates))
	expectedOrder := make([]string, len(candidates))
	for index, candidate := range candidates {
		plannedID := strings.TrimSpace(candidate.PlannedScriptID)
		if plannedID == "" {
			return fmt.Errorf("candidate for %q is missing planned_script_id", candidate.SourcePath)
		}
		if _, exists := expectedIDs[plannedID]; exists {
			return fmt.Errorf("inspection contains duplicate planned_script_id %q", plannedID)
		}
		expectedIDs[plannedID] = struct{}{}
		expectedOrder[index] = plannedID
	}
	receivedIDs := make(map[string]struct{}, len(committed.Scripts))
	for index, script := range committed.Scripts {
		scriptID := strings.TrimSpace(script.ID)
		if scriptID == "" {
			return errors.New("response contains a script without an identity")
		}
		if _, exists := receivedIDs[scriptID]; exists {
			return fmt.Errorf("response contains duplicate script identity %q", scriptID)
		}
		if _, exists := expectedIDs[scriptID]; !exists {
			return fmt.Errorf("response contains unknown script identity %q", scriptID)
		}
		if scriptID != expectedOrder[index] {
			return fmt.Errorf(
				"response script identity %q is out of order at position %d; expected %q",
				scriptID,
				index,
				expectedOrder[index],
			)
		}
		receivedIDs[scriptID] = struct{}{}
	}
	return nil
}

func orderBrunoCLICandidatesByCommittedScripts(
	candidates []brunoCLINativeCandidate,
	scripts []brunoCLIScript,
) []brunoCLINativeCandidate {
	byPlannedID := make(map[string]brunoCLINativeCandidate, len(candidates))
	for _, candidate := range candidates {
		byPlannedID[strings.TrimSpace(candidate.PlannedScriptID)] = candidate
	}
	ordered := make([]brunoCLINativeCandidate, 0, len(scripts))
	for _, script := range scripts {
		if candidate, ok := byPlannedID[strings.TrimSpace(script.ID)]; ok {
			ordered = append(ordered, candidate)
		}
	}
	return ordered
}

func isAmbiguousBrunoCommitError(err error, expectedSessionID string) bool {
	var ambiguous *brunoCLICommitAmbiguousError
	if errors.As(err, &ambiguous) {
		return true
	}
	var responseErr *brunoCLIHTTPError
	if !errors.As(err, &responseErr) {
		return false
	}
	if responseErr.code == brunoImportSessionClaimedResponseCode {
		return false
	}
	if responseErr.hasRetryMetadata {
		return responseErr.manualRetrySafeSet &&
			responseErr.manualRetrySafe &&
			responseErr.sessionRetainedSet &&
			responseErr.sessionRetained &&
			(!responseErr.importSessionConsumedSet || !responseErr.importSessionConsumed) &&
			strings.TrimSpace(responseErr.idempotencySessionID) != "" &&
			strings.TrimSpace(responseErr.idempotencySessionID) == strings.TrimSpace(expectedSessionID)
	}
	switch responseErr.statusCode {
	case http.StatusRequestTimeout,
		http.StatusTooEarly,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isBrunoImportSessionClaimedError(err error) bool {
	var responseErr *brunoCLIHTTPError
	return errors.As(err, &responseErr) &&
		responseErr.code == brunoImportSessionClaimedResponseCode
}

func brunoCLINativeSecretReadiness(
	candidate brunoCLINativeCandidate,
	args brunoImportArgs,
) string {
	for _, requirement := range candidate.SecretRequirements {
		// Inspection cannot prove that a configure-later or existing-reference
		// logical name is populated in the provider. Report readiness
		// conservatively instead of treating a binding name as a configured
		// secret.
		if requirement.ExistingReference || !requirement.SourceValueAvailable || !args.migrateSecrets {
			return "secret configuration required"
		}
	}
	return ""
}

func deterministicBrunoDirectoryFiles(
	root string,
	expectedRoot ...os.FileInfo,
) ([]brunoCLIDirectoryFile, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	rootInfo, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, fmt.Errorf("Bruno directory root %q changed or is not a directory", root)
	}
	if len(expectedRoot) > 0 &&
		expectedRoot[0] != nil &&
		!os.SameFile(expectedRoot[0], rootInfo) {
		return nil, fmt.Errorf("Bruno directory root %q changed before source snapshot", root)
	}
	directoryIdentities := map[string]os.FileInfo{absolute: rootInfo}
	var files []brunoCLIDirectoryFile
	var totalBytes int64
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
			if path != absolute {
				if err := validateBrunoDirectoryAncestors(
					filepath.Dir(path),
					absolute,
					directoryIdentities,
				); err != nil {
					return err
				}
				directoryIdentities[path] = info
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Bruno directory contains non-regular file %q", path)
		}
		relative, err := filepath.Rel(absolute, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == ".." || strings.HasPrefix(relative, "../") || filepath.IsAbs(relative) {
			return fmt.Errorf("Bruno directory file %q is outside the selected root", path)
		}
		if err := validateBrunoDirectoryAncestors(
			filepath.Dir(path),
			absolute,
			directoryIdentities,
		); err != nil {
			return err
		}
		remaining := maximumBrunoCLIImportSnapshotBytes - totalBytes
		data, err := readStableBrunoSourceFileBounded(path, info, remaining)
		if err != nil {
			return err
		}
		if err := validateBrunoDirectoryAncestors(
			filepath.Dir(path),
			absolute,
			directoryIdentities,
		); err != nil {
			clear(data)
			return err
		}
		totalBytes += int64(len(data))
		files = append(files, brunoCLIDirectoryFile{
			relativePath: relative,
			data:         data,
		})
		return nil
	})
	if err != nil {
		clearBrunoCLIDirectoryFiles(files)
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("Bruno directory contains no files")
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].relativePath < files[j].relativePath
	})
	return files, nil
}

func validateBrunoDirectoryAncestors(
	directory,
	root string,
	expected map[string]os.FileInfo,
) error {
	for {
		identity, ok := expected[directory]
		if !ok {
			return fmt.Errorf("Bruno directory ancestor %q was not inspected", directory)
		}
		current, err := os.Lstat(directory)
		if err != nil {
			return err
		}
		if current.Mode()&os.ModeSymlink != 0 ||
			!current.IsDir() ||
			!os.SameFile(identity, current) {
			return fmt.Errorf("Bruno directory ancestor %q changed during source snapshot", directory)
		}
		if directory == root {
			return nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return fmt.Errorf("Bruno directory ancestor %q escaped selected root", directory)
		}
		directory = parent
	}
}

func writeBrunoDirectoryMultipart(writer *multipart.Writer, args brunoImportArgs, files []brunoCLIDirectoryFile) error {
	type multipartField struct {
		name  string
		value string
	}
	fields := []multipartField{{name: "project_id", value: args.projectID}}
	if args.output != "native" {
		fields = append(fields,
			multipartField{name: "name", value: args.name},
			multipartField{name: "sandbox", value: args.sandbox},
			multipartField{name: "sync_script_id", value: args.syncScriptID},
		)
	}
	for _, field := range fields {
		if field.value != "" {
			if err := writer.WriteField(field.name, field.value); err != nil {
				return err
			}
		}
	}
	if args.output == "native" {
		options, err := json.Marshal(map[string]string{"output_mode": "native_requests"})
		if err != nil {
			return err
		}
		if err := writer.WriteField("options", string(options)); err != nil {
			return err
		}
	}
	for _, file := range files {
		relative := filepath.ToSlash(filepath.Clean(file.relativePath))
		if relative == "." ||
			relative == ".." ||
			strings.HasPrefix(relative, "../") ||
			filepath.IsAbs(relative) {
			return fmt.Errorf("Bruno directory snapshot path %q is outside the selected root", file.relativePath)
		}
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file:%s"; filename="%s"`, url.QueryEscape(relative), escapeMIMEQuoted(filepath.Base(relative))))
		header.Set("Content-Type", "application/octet-stream")
		part, err := writer.CreatePart(header)
		if err != nil {
			return err
		}
		if _, err := io.Copy(part, bytes.NewReader(file.data)); err != nil {
			return err
		}
	}
	return nil
}

func readStableBrunoSourceFile(filename string, expected os.FileInfo) ([]byte, error) {
	return readStableBrunoSourceFileBounded(
		filename,
		expected,
		maximumBrunoCLIImportSnapshotBytes,
	)
}

func readStableBrunoSourceFileBounded(
	filename string,
	expected os.FileInfo,
	maxBytes int64,
) ([]byte, error) {
	if maxBytes < 0 {
		return nil, errors.New("Bruno import exceeds the 64 MiB source snapshot limit")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	closeWithError := func(openErr error) ([]byte, error) {
		return nil, errors.Join(openErr, file.Close())
	}
	openedInfo, err := file.Stat()
	if err != nil {
		return closeWithError(err)
	}
	pathInfo, err := os.Lstat(filename)
	if err != nil {
		return closeWithError(err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 ||
		!pathInfo.Mode().IsRegular() ||
		!openedInfo.Mode().IsRegular() ||
		!os.SameFile(openedInfo, pathInfo) ||
		(expected != nil && !os.SameFile(expected, openedInfo)) {
		return closeWithError(fmt.Errorf(
			"Bruno source file %q changed or is no longer a regular file",
			filename,
		))
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	afterInfo, statErr := file.Stat()
	pathAfter, pathErr := os.Lstat(filename)
	closeErr := file.Close()
	if readErr != nil || statErr != nil || pathErr != nil || closeErr != nil {
		clear(data)
		return nil, errors.Join(readErr, statErr, pathErr, closeErr)
	}
	if int64(len(data)) > maxBytes {
		clear(data)
		return nil, errors.New("Bruno import exceeds the 64 MiB source snapshot limit")
	}
	if pathAfter.Mode()&os.ModeSymlink != 0 ||
		!pathAfter.Mode().IsRegular() ||
		!afterInfo.Mode().IsRegular() ||
		!os.SameFile(openedInfo, afterInfo) ||
		!os.SameFile(openedInfo, pathAfter) ||
		openedInfo.Size() != afterInfo.Size() ||
		!openedInfo.ModTime().Equal(afterInfo.ModTime()) {
		clear(data)
		return nil, fmt.Errorf(
			"Bruno source file %q changed while it was being snapshotted",
			filename,
		)
	}
	return data, nil
}

func clearBrunoCLIDirectoryFiles(files []brunoCLIDirectoryFile) {
	for index := range files {
		clear(files[index].data)
		files[index].data = nil
	}
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

func validateBrunoCLINativeSuppliedBindings(requirements []brunoCLISecretRequirement, supplied map[string]string) error {
	known := make(map[string]struct{}, len(requirements)*2)
	for _, requirement := range requirements {
		if id := strings.TrimSpace(requirement.ID); id != "" {
			known[id] = struct{}{}
		}
		if name := strings.TrimSpace(requirement.SuggestedName); name != "" {
			known[name] = struct{}{}
		}
	}
	for rawKey := range supplied {
		key := strings.TrimSpace(rawKey)
		if _, exists := known[key]; !exists {
			return fmt.Errorf("native Bruno secret binding %q does not match a selected requirement id or suggested name", rawKey)
		}
	}
	return nil
}

func uniqueBrunoCLISecretRequirements(requirements []brunoCLISecretRequirement) []brunoCLISecretRequirement {
	seen := make(map[string]struct{}, len(requirements))
	result := make([]brunoCLISecretRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		if requirement.ID == "" {
			continue
		}
		if _, exists := seen[requirement.ID]; exists {
			continue
		}
		seen[requirement.ID] = struct{}{}
		result = append(result, requirement)
	}
	return result
}

func brunoCLIReviewFindingIDs(candidates []brunoCLINativeCandidate) []string {
	seen := make(map[string]struct{})
	var ids []string
	appendID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, candidate := range candidates {
		for _, id := range candidate.FindingIDs {
			appendID(id)
		}
		for _, diagnostic := range candidate.Diagnostics {
			if !candidate.ReviewRequired && !diagnostic.ReviewRequired && !diagnostic.AcknowledgementRequired {
				continue
			}
			if diagnostic.ID != "" {
				appendID(diagnostic.ID)
			} else {
				appendID(diagnostic.FindingID)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func brunoCLINativeSourceSummaries(candidates []brunoCLINativeCandidate) []map[string]any {
	summaries := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		summary := map[string]any{
			"candidate_id":      candidate.CandidateID,
			"source_path":       candidate.SourcePath,
			"sequence":          candidate.Sequence,
			"fidelity":          candidate.Fidelity,
			"diagnostics":       candidate.Diagnostics,
			"missing_variables": candidate.MissingVariables,
		}
		if candidate.SourceUID != "" {
			summary["source_uid"] = candidate.SourceUID
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func escapeMIMEQuoted(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, `\"`, "\r", "", "\n", "").Replace(value)
}

func brunoCLIResponseError(operation string, response *http.Response) error {
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var decoded struct {
		Error                 string                      `json:"error"`
		Code                  string                      `json:"code"`
		Requirements          []brunoCLISecretRequirement `json:"requirements"`
		SessionRetained       *bool                       `json:"session_retained"`
		ManualRetrySafe       *bool                       `json:"manual_retry_safe"`
		ImportSessionConsumed *bool                       `json:"import_session_consumed"`
		IdempotencySessionID  string                      `json:"idempotency_session_id"`
		CommittedSecretNames  []string                    `json:"committed_secret_names"`
		CommittedNames        []string                    `json:"committed_names"`
		MigratedSecretNames   []string                    `json:"migrated_secret_names"`
	}
	var fields map[string]json.RawMessage
	decodedJSON := json.Unmarshal(payload, &fields) == nil
	_ = json.Unmarshal(payload, &decoded)
	committedNames := append([]string(nil), decoded.CommittedSecretNames...)
	committedNames = append(committedNames, decoded.CommittedNames...)
	committedNames = append(committedNames, decoded.MigratedSecretNames...)
	sort.Strings(committedNames)
	message := strings.TrimSpace(decoded.Error)
	if message == "" && !decodedJSON {
		message = strings.TrimSpace(string(payload))
	}
	_, sessionRetainedSet := fields["session_retained"]
	_, manualRetrySafeSet := fields["manual_retry_safe"]
	_, importSessionConsumedSet := fields["import_session_consumed"]
	_, idempotencySessionIDSet := fields["idempotency_session_id"]
	return &brunoCLIHTTPError{
		operation:                operation,
		status:                   response.Status,
		statusCode:               response.StatusCode,
		message:                  message,
		code:                     decoded.Code,
		requirements:             decoded.Requirements,
		sessionRetained:          decoded.SessionRetained != nil && *decoded.SessionRetained,
		sessionRetainedSet:       sessionRetainedSet,
		manualRetrySafe:          decoded.ManualRetrySafe != nil && *decoded.ManualRetrySafe,
		manualRetrySafeSet:       manualRetrySafeSet,
		importSessionConsumed:    decoded.ImportSessionConsumed != nil && *decoded.ImportSessionConsumed,
		importSessionConsumedSet: importSessionConsumedSet,
		hasRetryMetadata:         sessionRetainedSet || manualRetrySafeSet || importSessionConsumedSet || idempotencySessionIDSet,
		idempotencySessionID:     decoded.IdempotencySessionID,
		committedSecretNames:     committedNames,
		retryAfter:               parseCIRetryAfter(response.Header.Get("Retry-After"), time.Now()),
	}
}

func brunoImportUsage() string {
	return `Usage:
  apexctl import bruno --api <controller> --path <directory|archive|file> --project <project-id> [options]

Options:
  --output <canonical|native>     Import one canonical Bruno script (default) or one native script per API
  --folder <folder-id>           Required destination script folder for native imports
  --ack-review-required          Acknowledge review-gated native conversion findings
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
