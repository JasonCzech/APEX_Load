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
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseBrunoImportArgsSupportsEqualsAndRejectsAmbiguousAssignments(t *testing.T) {
	args, err := parseBrunoImportArgs([]string{
		"--api=https://controller.example.test/", "--path=./collection", "--project=project-1", "--folder=folder-1",
		"--secret-binding=requirement-1=BRUNO_PASSWORD", "--format=json",
	})
	if err != nil || args.api != "https://controller.example.test" || args.secretBindings["requirement-1"] != "BRUNO_PASSWORD" || args.format != "json" {
		t.Fatalf("equals-form Bruno args = %#v, error %v", args, err)
	}
	args, err = parseBrunoImportArgs([]string{
		"--api=https://controller.example.test", "--path=./collection", "--project=project-1", "--folder=folder-1",
		"--migrate-secrets=false", "--ack-review-required=false",
	})
	if err != nil || args.migrateSecrets || args.ackReview {
		t.Fatalf("boolean equals-form Bruno args = %#v, error %v", args, err)
	}
	base := []string{"--api=https://controller.example.test", "--path=./collection", "--project=project-1", "--folder=folder-1"}
	for _, extra := range [][]string{
		{"--secret-binding=a=one", "--secret-binding=a=two"},
		{"--conflict-resolution=a=one", "--conflict-resolution=a=two"},
		{"--migrate-secrets", "--no-migrate-secrets"},
		{"--api=controller-without-scheme"},
	} {
		if _, err := parseBrunoImportArgs(append(append([]string(nil), base...), extra...)); err == nil {
			t.Errorf("parseBrunoImportArgs(%q) accepted ambiguous/malformed input", extra)
		}
	}
}

func TestParseBrunoImportArgsNativeMode(t *testing.T) {
	args, err := parseBrunoImportArgs([]string{
		"--api", "https://controller.example.test",
		"--path", "./collection",
		"--project", "project-1",
		"--output", "native",
		"--folder", "folder-1",
		"--ack-review-required",
	})
	if err != nil {
		t.Fatal(err)
	}
	if args.output != "native" || args.folderID != "folder-1" || !args.ackReview {
		t.Fatalf("parsed args = %#v", args)
	}

	for _, test := range []struct {
		name string
		args []string
	}{
		{
			name: "sync",
			args: []string{"--output", "native", "--folder", "folder-1", "--sync", "script-1"},
		},
		{
			name: "collection name",
			args: []string{"--output", "native", "--folder", "folder-1", "--name", "Combined"},
		},
		{
			name: "sandbox",
			args: []string{"--output", "native", "--folder", "folder-1", "--sandbox", "safe"},
		},
		{
			name: "missing native folder",
			args: []string{"--output", "native"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := []string{
				"--api", "https://controller.example.test",
				"--path", "./collection",
				"--project", "project-1",
			}
			if _, err := parseBrunoImportArgs(append(base, test.args...)); err == nil {
				t.Fatalf("expected incompatible flag error")
			}
		})
	}
}

func TestRunBrunoImportCreatesNativeBatch(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"opencollection.yml": "version: 1\n",
		"request.yml":        "info: {name: API, type: http}\nhttp: {url: https://example.test}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var commitBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/scripts/import/bruno/inspect":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			options := ""
			sandbox := ""
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				data, readErr := io.ReadAll(part)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if part.FormName() == "options" {
					options = string(data)
				}
				if part.FormName() == "sandbox" {
					sandbox = string(data)
				}
				_ = part.Close()
			}
			var parsedOptions map[string]any
			if err := json.Unmarshal([]byte(options), &parsedOptions); err != nil {
				t.Fatalf("options = %q: %v", options, err)
			}
			if parsedOptions["output_mode"] != "native_requests" {
				t.Fatalf("options = %#v", parsedOptions)
			}
			if sandbox != "" {
				t.Fatalf("native directory inspection sent canonical sandbox field %q", sandbox)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{
			  "import_session_id":"session-native",
			  "result":{
			    "collection":{"name":"APIs"},
			    "scripts":[{
			      "candidate_id":"candidate-1",
			      "planned_script_id":"planned-1",
			      "source_path":"request.yml",
			      "sequence":1,
			      "diagnostics":[{
			        "id":"finding-1",
			        "code":"bruno_javascript_review_required",
			        "acknowledgement_required":true
			      }],
			      "script":{"name":"API","protocol":"http"}
			    }]
			  }
			}`)
		case "/api/v1/scripts/import/bruno/commit":
			if err := json.NewDecoder(r.Body).Decode(&commitBody); err != nil {
				t.Fatal(err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{
			  "batch_id":"batch-1",
			  "created":true,
			  "idempotent_replay":false,
			  "count":1,
			  "scripts":[{
			    "id":"planned-1",
			    "name":"API",
			    "ref_key":"SCR-1",
			    "protocol":"http",
			    "project_id":"project-1",
			    "folder_id":"folder-1",
			    "actions":[{"type":"POST","target":"https://example.test"}],
			    "metadata":{"import":{"preserved":{"source_path":"request.yml"}}}
			  }]
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := runBrunoImport(t.Context(), brunoImportArgs{
		path:           root,
		projectID:      "project-1",
		output:         "native",
		folderID:       "folder-1",
		ackReview:      true,
		api:            server.URL,
		timeout:        defaultCLIImportTimeout,
		migrateSecrets: false,
		secretBindings: map[string]string{},
		conflicts:      map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.BatchID != "batch-1" || result.Count != 1 || len(result.Scripts) != 1 || result.Scripts[0].ID != "planned-1" {
		t.Fatalf("native result = %#v", result)
	}
	var completeBatch map[string]any
	if err := json.Unmarshal(result.RawResponse, &completeBatch); err != nil {
		t.Fatalf("decode complete native batch: %v", err)
	}
	rawScripts, _ := completeBatch["scripts"].([]any)
	if len(rawScripts) != 1 {
		t.Fatalf("complete native batch scripts = %#v", completeBatch["scripts"])
	}
	rawScript, _ := rawScripts[0].(map[string]any)
	if actions, _ := rawScript["actions"].([]any); len(actions) != 1 || rawScript["metadata"] == nil {
		t.Fatalf("complete native batch lost script fields: %#v", completeBatch)
	}
	if commitBody["mode"] != "create_native_batch" || commitBody["folder_id"] != "folder-1" {
		t.Fatalf("commit body = %#v", commitBody)
	}
	if got, _ := commitBody["candidate_ids"].([]any); len(got) != 1 || got[0] != "candidate-1" {
		t.Fatalf("candidate ids = %#v", commitBody["candidate_ids"])
	}
	if got, _ := commitBody["acknowledged_finding_ids"].([]any); len(got) != 1 || got[0] != "finding-1" {
		t.Fatalf("finding ids = %#v", commitBody["acknowledged_finding_ids"])
	}
}

func TestCommitBrunoNativeBatchRetriesOnlyAmbiguousFailures(t *testing.T) {
	tests := []struct {
		name        string
		failure     string
		wantCalls   int
		wantSuccess bool
	}{
		{name: "transport failure", failure: "transport_once", wantCalls: 2, wantSuccess: true},
		{name: "repeated transport failures", failure: "transport_three", wantCalls: 4, wantSuccess: true},
		{name: "persistent transport failure is bounded", failure: "transport_forever", wantCalls: 4, wantSuccess: false},
		{name: "success body decode failure", failure: "decode_once", wantCalls: 2, wantSuccess: true},
		{name: "persistent decode failure is bounded", failure: "decode_forever", wantCalls: 2, wantSuccess: false},
		{name: "incomplete success receipt", failure: "receipt_once", wantCalls: 2, wantSuccess: true},
		{name: "persistent incomplete receipt is bounded", failure: "receipt_forever", wantCalls: 2, wantSuccess: false},
		{name: "transport then active claim", failure: "transport_claimed", wantCalls: 3, wantSuccess: true},
		{name: "server retained retry-safe session", failure: "retained_once", wantCalls: 2, wantSuccess: true},
		{name: "unstructured retryable status", failure: "unstructured_503", wantCalls: 2, wantSuccess: true},
		{name: "unstructured ordinary status", failure: "unstructured_400", wantCalls: 1, wantSuccess: false},
		{name: "server unretained retry-safe session", failure: "unretained_once", wantCalls: 1, wantSuccess: false},
		{name: "retry-safe session consumed", failure: "unretained_consumed", wantCalls: 1, wantSuccess: false},
		{name: "explicitly unsafe structured response", failure: "explicit_unsafe", wantCalls: 1, wantSuccess: false},
		{name: "retry-safe wrong session", failure: "wrong_session", wantCalls: 1, wantSuccess: false},
		{name: "ordinary HTTP rejection", failure: "http_rejection", wantCalls: 1, wantSuccess: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var payloads [][]byte
			client := NewApiClientWithAuth("http://controller.example.test", "", "")
			client.Client.Transport = brunoRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				payload, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatalf("read commit payload: %v", err)
				}
				payloads = append(payloads, payload)
				call := len(payloads)
				switch {
				case test.failure == "transport_once" && call == 1:
					return nil, errors.New("connection reset after request write")
				case test.failure == "transport_three" && call <= 3:
					return nil, errors.New("connection reset after request write")
				case test.failure == "transport_forever":
					return nil, errors.New("connection reset after request write")
				case test.failure == "transport_claimed" && call == 1:
					return nil, errors.New("connection reset after request write")
				case test.failure == "transport_claimed" && call == 2:
					return &http.Response{
						StatusCode: http.StatusConflict,
						Status:     "409 Conflict",
						Header:     make(http.Header),
						Body: io.NopCloser(strings.NewReader(`{
						  "error":"Bruno import session is already being consumed",
						  "code":"bruno_import_session_claimed"
						}`)),
					}, nil
				case test.failure == "decode_once" && call == 1:
					return &http.Response{
						StatusCode: http.StatusCreated,
						Status:     "201 Created",
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"batch_id":`)),
					}, nil
				case test.failure == "decode_forever":
					return &http.Response{
						StatusCode: http.StatusCreated,
						Status:     "201 Created",
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"batch_id":`)),
					}, nil
				case test.failure == "receipt_once" && call == 1:
					return &http.Response{
						StatusCode: http.StatusCreated,
						Status:     "201 Created",
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"count":1,"scripts":[{"id":"script-1"}]}`)),
					}, nil
				case test.failure == "receipt_forever":
					return &http.Response{
						StatusCode: http.StatusCreated,
						Status:     "201 Created",
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"count":1,"scripts":[{"id":"script-1"}]}`)),
					}, nil
				case test.failure == "retained_once" && call == 1:
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Status:     "503 Service Unavailable",
						Header:     make(http.Header),
						Body: io.NopCloser(strings.NewReader(`{
						  "error":"commit state unknown",
						  "code":"bruno_native_commit_state_unknown",
						  "session_retained":true,
						  "manual_retry_safe":true,
						  "idempotency_session_id":"session-1",
						  "committed_names":["API_PASSWORD"]
						}`)),
					}, nil
				case test.failure == "unstructured_503" && call == 1:
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Status:     "503 Service Unavailable",
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("upstream unavailable")),
					}, nil
				case test.failure == "unstructured_400" && call == 1:
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Status:     "400 Bad Request",
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("invalid request")),
					}, nil
				case (test.failure == "unretained_once" ||
					test.failure == "unretained_consumed") && call == 1:
					consumed := test.failure == "unretained_consumed"
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Status:     "503 Service Unavailable",
						Header:     make(http.Header),
						Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{
						  "error":"commit state unknown",
						  "code":"bruno_native_commit_state_unknown",
						  "session_retained":%t,
						  "manual_retry_safe":true,
						  "import_session_consumed":%t,
						  "idempotency_session_id":"session-1"
						}`, consumed, consumed))),
					}, nil
				case test.failure == "explicit_unsafe" && call == 1:
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Status:     "503 Service Unavailable",
						Header:     make(http.Header),
						Body: io.NopCloser(strings.NewReader(`{
						  "error":"manual retry is unsafe",
						  "manual_retry_safe":false,
						  "session_retained":true,
						  "idempotency_session_id":"session-1"
						}`)),
					}, nil
				case test.failure == "wrong_session" && call == 1:
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Status:     "503 Service Unavailable",
						Header:     make(http.Header),
						Body: io.NopCloser(strings.NewReader(`{
						  "error":"commit state unknown",
						  "code":"bruno_native_commit_state_unknown",
						  "manual_retry_safe":true,
						  "session_retained":true,
						  "idempotency_session_id":"different-session"
						}`)),
					}, nil
				case test.failure == "http_rejection" && call == 1:
					return &http.Response{
						StatusCode: http.StatusConflict,
						Status:     "409 Conflict",
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"error":"review acknowledgement required","code":"bruno_native_review_acknowledgement_required"}`)),
					}, nil
				default:
					return &http.Response{
						StatusCode: http.StatusOK,
						Status:     "200 OK",
						Header:     make(http.Header),
						Body: io.NopCloser(strings.NewReader(`{
						  "batch_id":"batch-1",
						  "created":false,
						  "idempotent_replay":true,
						  "count":1,
						  "scripts":[{"id":"script-1","name":"API","protocol":"http"}]
						}`)),
					}, nil
				}
			})

			request := map[string]any{
				"project_id":        "project-1",
				"import_session_id": "session-1",
				"mode":              "create_native_batch",
				"folder_id":         "folder-1",
				"candidate_ids":     []string{"candidate-1"},
			}
			committed, err := commitBrunoNativeBatchWithRetryAndWait(
				t.Context(),
				client,
				request,
				[]brunoCLINativeCandidate{{CandidateID: "candidate-1", PlannedScriptID: "script-1"}},
				func(context.Context, time.Duration) error { return nil },
			)
			if test.wantSuccess {
				if err != nil {
					t.Fatalf("commit native batch: %v", err)
				}
				if committed == nil || committed.BatchID != "batch-1" || !committed.IdempotentReplay {
					t.Fatalf("commit result = %#v", committed)
				}
			} else if err == nil {
				t.Fatalf("expected commit failure, got %#v", committed)
			}
			if len(payloads) != test.wantCalls {
				t.Fatalf("commit calls = %d, want %d", len(payloads), test.wantCalls)
			}
			for index := 1; index < len(payloads); index++ {
				if !bytes.Equal(payloads[0], payloads[index]) {
					t.Fatalf("retry payload %d differs:\nfirst: %s\nretry: %s", index+1, payloads[0], payloads[index])
				}
			}
		})
	}
}

func TestBrunoNativeUnstructuredRetryStatusAllowlist(t *testing.T) {
	retryable := map[int]bool{
		http.StatusRequestTimeout:      true,
		http.StatusTooEarly:            true,
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
		http.StatusGatewayTimeout:      true,
	}
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusRequestTimeout,
		http.StatusConflict,
		http.StatusTooEarly,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusNotImplemented,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			err := &brunoCLIHTTPError{statusCode: status}
			if got := isAmbiguousBrunoCommitError(err, "session-1"); got != retryable[status] {
				t.Fatalf("status %d retryable = %t, want %t", status, got, retryable[status])
			}
		})
	}
}

func TestCommitBrunoNativeBatchHonorsRetryAfter(t *testing.T) {
	calls := 0
	client := NewApiClientWithAuth("http://controller.example.test", "", "")
	client.Client.Transport = brunoRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			header := make(http.Header)
			header.Set("Retry-After", "7")
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Header:     header,
				Body:       io.NopCloser(strings.NewReader("retry later")),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
			  "batch_id":"batch-1",
			  "created":false,
			  "idempotent_replay":true,
			  "count":1,
			  "scripts":[{"id":"script-1","protocol":"http"}]
			}`)),
		}, nil
	})
	var delays []time.Duration
	committed, err := commitBrunoNativeBatchWithRetryAndWait(
		t.Context(),
		client,
		map[string]any{
			"mode":              "create_native_batch",
			"import_session_id": "session-1",
		},
		[]brunoCLINativeCandidate{{CandidateID: "candidate-1", PlannedScriptID: "script-1"}},
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("commit native batch: %v", err)
	}
	if committed == nil || committed.BatchID != "batch-1" {
		t.Fatalf("commit result = %#v", committed)
	}
	if !reflect.DeepEqual(delays, []time.Duration{7 * time.Second}) {
		t.Fatalf("retry delays = %v, want [7s]", delays)
	}
}

func TestCommitBrunoNativeBatchPollsClaimBeyondSingleLease(t *testing.T) {
	calls := 0
	client := NewApiClientWithAuth("http://controller.example.test", "", "")
	client.Client.Transport = brunoRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls > 100 {
			t.Fatalf("claim polling exceeded hard test ceiling")
		}
		if calls > 40 {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
				  "batch_id":"batch-1",
				  "created":false,
				  "idempotent_replay":true,
				  "count":1,
				  "scripts":[{"id":"script-1","protocol":"http"}]
				}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusConflict,
			Status:     "409 Conflict",
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
			  "error":"Bruno import session is already being consumed",
			  "code":"bruno_import_session_claimed"
			}`)),
		}, nil
	})
	var waited time.Duration
	wait := func(_ context.Context, delay time.Duration) error {
		waited += delay
		return nil
	}
	request := map[string]any{
		"project_id":        "project-1",
		"import_session_id": "session-1",
		"mode":              "create_native_batch",
	}
	committed, err := commitBrunoNativeBatchWithRetryAndWait(
		t.Context(),
		client,
		request,
		[]brunoCLINativeCandidate{{CandidateID: "candidate-1", PlannedScriptID: "script-1"}},
		wait,
	)
	if err != nil {
		t.Fatalf("claim polling: %v", err)
	}
	if committed == nil || committed.BatchID != "batch-1" {
		t.Fatalf("claim polling result = %#v", committed)
	}
	if waited <= 35*time.Second {
		t.Fatalf("claim polling wait = %s, want polling beyond a single fixed lease window", waited)
	}
	if calls != 41 {
		t.Fatalf("claim polling calls = %d, want 41", calls)
	}
}

func TestCommitBrunoNativeBatchClaimPollRespectsContextCancellation(t *testing.T) {
	client := NewApiClientWithAuth("http://controller.example.test", "", "")
	client.Client.Transport = brunoRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusConflict,
			Status:     "409 Conflict",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"code":"bruno_import_session_claimed"}`)),
		}, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	wait := func(context.Context, time.Duration) error {
		cancel()
		return ctx.Err()
	}
	_, err := commitBrunoNativeBatchWithRetryAndWait(
		ctx,
		client,
		map[string]any{"mode": "create_native_batch"},
		[]brunoCLINativeCandidate{{CandidateID: "candidate-1", PlannedScriptID: "script-1"}},
		wait,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("claim cancellation error = %v", err)
	}
}

func TestCommitBrunoNativeBatchAmbiguousRetryRespectsContextCancellation(t *testing.T) {
	var payloads [][]byte
	client := NewApiClientWithAuth("http://controller.example.test", "", "")
	client.Client.Transport = brunoRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read retry payload: %v", err)
		}
		payloads = append(payloads, payload)
		return nil, errors.New("connection reset after request write")
	})
	ctx, cancel := context.WithCancel(t.Context())
	waitCalls := 0
	wait := func(context.Context, time.Duration) error {
		waitCalls++
		if waitCalls == 2 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	_, err := commitBrunoNativeBatchWithRetryAndWait(
		ctx,
		client,
		map[string]any{
			"mode":              "create_native_batch",
			"import_session_id": "session-1",
		},
		[]brunoCLINativeCandidate{{
			CandidateID:     "candidate-1",
			PlannedScriptID: "script-1",
		}},
		wait,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ambiguous retry error = %v", err)
	}
	if len(payloads) != 2 {
		t.Fatalf("ambiguous retry calls = %d, want 2", len(payloads))
	}
	for index := 1; index < len(payloads); index++ {
		if !bytes.Equal(payloads[0], payloads[index]) {
			t.Fatalf("ambiguous retry payload %d differs", index+1)
		}
	}
}

func TestValidateBrunoCLINativeCommitResponseRejectsInvalidReceipts(t *testing.T) {
	candidates := []brunoCLINativeCandidate{
		{SourcePath: "one.yml", PlannedScriptID: "script-1"},
		{SourcePath: "two.yml", PlannedScriptID: "script-2"},
	}
	script := func(id string) brunoCLIScript {
		return brunoCLIScript{ID: id, Protocol: "http"}
	}
	createdReceipt := func(count int, scripts ...brunoCLIScript) *brunoCLICommitResponse {
		return &brunoCLICommitResponse{
			BatchID:      "batch-1",
			Created:      true,
			Count:        count,
			Scripts:      scripts,
			HTTPStatus:   http.StatusCreated,
			CreatedSet:   true,
			ReplaySet:    true,
			CreatedValid: true,
			ReplayValid:  true,
		}
	}
	tests := []struct {
		name     string
		response *brunoCLICommitResponse
		want     string
	}{
		{
			name: "missing batch identity",
			response: func() *brunoCLICommitResponse {
				response := createdReceipt(2, script("script-1"), script("script-2"))
				response.BatchID = ""
				return response
			}(),
			want: "missing batch_id",
		},
		{
			name:     "count mismatch",
			response: createdReceipt(1, script("script-1"), script("script-2")),
			want:     "count 1 does not match",
		},
		{
			name:     "partial receipt",
			response: createdReceipt(1, script("script-1")),
			want:     "1 of 2 selected scripts",
		},
		{
			name:     "unknown script identity",
			response: createdReceipt(2, script("script-1"), script("unknown")),
			want:     "unknown script identity",
		},
		{
			name:     "duplicate script identity",
			response: createdReceipt(2, script("script-1"), script("script-1")),
			want:     "duplicate script identity",
		},
		{
			name:     "missing outcome fields",
			response: &brunoCLICommitResponse{BatchID: "batch-1"},
			want:     "must include boolean created and idempotent_replay",
		},
		{
			name: "both outcomes true",
			response: func() *brunoCLICommitResponse {
				response := createdReceipt(2, script("script-1"), script("script-2"))
				response.IdempotentReplay = true
				return response
			}(),
			want: "exactly one of creation or idempotent replay",
		},
		{
			name: "created outcome on HTTP 200",
			response: func() *brunoCLICommitResponse {
				response := createdReceipt(2, script("script-1"), script("script-2"))
				response.HTTPStatus = http.StatusOK
				return response
			}(),
			want: "HTTP 200 response must identify an idempotent replay",
		},
		{
			name: "replay outcome on HTTP 201",
			response: func() *brunoCLICommitResponse {
				response := createdReceipt(2, script("script-1"), script("script-2"))
				response.Created = false
				response.IdempotentReplay = true
				return response
			}(),
			want: "HTTP 201 response must identify a newly created batch",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateBrunoCLINativeCommitResponse(test.response, candidates)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
	if err := validateBrunoCLINativeCommitResponse(
		createdReceipt(2, script("script-2"), script("script-1")),
		candidates,
	); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("reordered receipt validation error = %v, want order mismatch", err)
	}
}

func TestValidateBrunoCLINativeCandidateIdentities(t *testing.T) {
	tests := []struct {
		name       string
		candidates []brunoCLINativeCandidate
		want       string
	}{
		{
			name: "missing planned identity",
			candidates: []brunoCLINativeCandidate{{
				CandidateID: "candidate-1",
				SourcePath:  "request.yml",
			}},
			want: "missing planned_script_id",
		},
		{
			name: "duplicate planned identity",
			candidates: []brunoCLINativeCandidate{
				{CandidateID: "candidate-1", SourcePath: "one.yml", PlannedScriptID: "script-1"},
				{CandidateID: "candidate-2", SourcePath: "two.yml", PlannedScriptID: "script-1"},
			},
			want: "duplicate planned_script_id",
		},
		{
			name: "missing candidate identity",
			candidates: []brunoCLINativeCandidate{{
				SourcePath:      "request.yml",
				PlannedScriptID: "script-1",
			}},
			want: "missing candidate_id",
		},
		{
			name: "duplicate normalized candidate identity",
			candidates: []brunoCLINativeCandidate{
				{CandidateID: " candidate-1 ", SourcePath: "one.yml", PlannedScriptID: "script-1"},
				{CandidateID: "candidate-1", SourcePath: "two.yml", PlannedScriptID: "script-2"},
			},
			want: "duplicate candidate_id",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateBrunoCLINativeCandidateIdentities(test.candidates)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBrunoCLINativeSecretReadinessUsesSourceAvailability(t *testing.T) {
	args := brunoImportArgs{migrateSecrets: true, secretBindings: map[string]string{}}
	candidate := brunoCLINativeCandidate{SecretRequirements: []brunoCLISecretRequirement{{
		ID:                   "requirement-1",
		SuggestedName:        "API_PASSWORD",
		SourceValueAvailable: false,
	}}}
	if got := brunoCLINativeSecretReadiness(candidate, args); got != "secret configuration required" {
		t.Fatalf("readiness = %q", got)
	}
	args.secretBindings["requirement-1"] = "PROJECT_PASSWORD"
	if got := brunoCLINativeSecretReadiness(candidate, args); got != "secret configuration required" {
		t.Fatalf("bound readiness = %q", got)
	}
	candidate.SecretRequirements[0].SourceValueAvailable = true
	if got := brunoCLINativeSecretReadiness(candidate, args); got != "" {
		t.Fatalf("migratable readiness = %q", got)
	}
}

func TestBrunoCLIReviewRequiredErrorIncludesInspectableFindings(t *testing.T) {
	err := (&brunoCLIReviewRequiredError{
		findings: []string{"finding-1"},
		candidates: []brunoCLINativeCandidate{{
			SourcePath: "api/request.yml",
			Diagnostics: []brunoCLIDiagnostic{{
				FindingID:               "finding-1",
				Code:                    "bruno_javascript_review_required",
				Message:                 "Translate the hook.",
				AcknowledgementRequired: true,
			}},
		}},
	}).Error()
	for _, expected := range []string{"finding-1", "api/request.yml", "bruno_javascript_review_required", "Translate the hook."} {
		if !strings.Contains(err, expected) {
			t.Fatalf("review error %q does not contain %q", err, expected)
		}
	}
}

func TestBrunoCLIReviewRequiredErrorIncludesCandidateFindingWithoutDiagnostic(t *testing.T) {
	err := (&brunoCLIReviewRequiredError{
		findings: []string{"finding-candidate-only"},
		candidates: []brunoCLINativeCandidate{{
			SourcePath: "api/request.yml",
			FindingIDs: []string{"finding-candidate-only"},
		}},
	}).Error()
	for _, expected := range []string{"finding-candidate-only", "api/request.yml", "review required"} {
		if !strings.Contains(err, expected) {
			t.Fatalf("review error %q does not contain %q", err, expected)
		}
	}
}

func TestValidateBrunoCLINativeSuppliedBindingsRejectsUnknownKey(t *testing.T) {
	requirements := []brunoCLISecretRequirement{{ID: "requirement-1", SuggestedName: "API_PASSWORD"}}
	if err := validateBrunoCLINativeSuppliedBindings(requirements, map[string]string{
		"unknown": "PROJECT_SECRET",
	}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unknown native binding error = %v", err)
	}
	if err := validateBrunoCLINativeSuppliedBindings(requirements, map[string]string{
		"API_PASSWORD": "PROJECT_SECRET",
	}); err != nil {
		t.Fatalf("suggested-name native binding: %v", err)
	}
}

type brunoRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn brunoRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestRunBrunoImportRequiresReviewAcknowledgement(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "request.yml")
	if err := os.WriteFile(sourcePath, []byte("info: {name: API, type: http}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scripts/import/bruno/inspect" {
			t.Fatalf("unexpected commit request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
		  "import_session_id":"session-native",
		  "result":{"scripts":[{
		    "candidate_id":"candidate-1",
		    "planned_script_id":"planned-1",
		    "source_path":"request.yml",
		    "diagnostics":[{"id":"finding-1","acknowledgement_required":true}]
		  }]}
		}`)
	}))
	defer server.Close()

	_, err := runBrunoImport(t.Context(), brunoImportArgs{
		path:           sourcePath,
		projectID:      "project-1",
		output:         "native",
		folderID:       "folder-1",
		api:            server.URL,
		timeout:        defaultCLIImportTimeout,
		secretBindings: map[string]string{},
	})
	if err == nil || !strings.Contains(err.Error(), "--ack-review-required") {
		t.Fatalf("review acknowledgement error = %v", err)
	}
}

func TestDeterministicBrunoDirectoryFilesRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "request.yml")
	if err := os.WriteFile(target, []byte("request"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "linked.yml")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := deterministicBrunoDirectoryFiles(root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestWriteBrunoDirectoryMultipartUsesSnapshotAfterSymlinkSwap(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "request.bru")
	if err := os.WriteFile(path, []byte("safe request"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := deterministicBrunoDirectoryFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-secret.txt")
	const canary = "directory-symlink-swap-canary-5c8d"
	if err := os.WriteFile(outside, []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	err = writeBrunoDirectoryMultipart(writer, brunoImportArgs{
		path:      root,
		projectID: "project-1",
		output:    "native",
	}, files)
	_ = writer.Close()
	if err != nil {
		t.Fatalf("write snapshotted multipart: %v", err)
	}
	if strings.Contains(payload.String(), canary) {
		t.Fatalf("symlink-swap uploaded outside content: %q", payload.String())
	}
	if !strings.Contains(payload.String(), "safe request") {
		t.Fatalf("multipart omitted snapshotted source: %q", payload.String())
	}
}

func TestWriteBrunoDirectoryMultipartRejectsEscapingSnapshotPath(t *testing.T) {
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	err := writeBrunoDirectoryMultipart(writer, brunoImportArgs{
		projectID: "project-1",
		output:    "native",
	}, []brunoCLIDirectoryFile{{
		relativePath: "../outside.bru",
		data:         []byte("outside"),
	}})
	_ = writer.Close()
	if err == nil || !strings.Contains(err.Error(), "outside the selected root") {
		t.Fatalf("escaping snapshot path error = %v", err)
	}
	if strings.Contains(payload.String(), "outside.bru") ||
		strings.Contains(payload.String(), "outside") {
		t.Fatalf("escaping snapshot path reached multipart payload: %q", payload.String())
	}
}

func TestReadStableBrunoSourceFileRejectsRegularReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "request.bru")
	if err := os.WriteFile(path, []byte("safe request"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "replacement.bru")
	const canary = "regular-file-replacement-canary-110d"
	if err := os.WriteFile(replacement, []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}

	data, err := readStableBrunoSourceFile(path, expected)
	defer clear(data)
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("regular replacement error = %v", err)
	}
	if bytes.Contains(data, []byte(canary)) {
		t.Fatalf("regular replacement returned outside snapshot: %q", data)
	}
}

func TestDeterministicBrunoDirectoryFilesRejectsRootReplacement(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "collection")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "safe.bru"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(parent, "original")
	if err := os.Rename(root, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	const canary = "directory-root-replacement-canary-4b1f"
	if err := os.WriteFile(filepath.Join(root, "outside.bru"), []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := deterministicBrunoDirectoryFiles(root, expected)
	defer clearBrunoCLIDirectoryFiles(files)
	if err == nil || !strings.Contains(err.Error(), "changed before source snapshot") {
		t.Fatalf("directory root replacement error = %v", err)
	}
	for _, file := range files {
		if bytes.Contains(file.data, []byte(canary)) {
			t.Fatalf("directory root replacement returned outside source: %q", file.data)
		}
	}
}

func TestRunBrunoImportRejectsTopLevelSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "request.bru")
	if err := os.WriteFile(target, []byte("request"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.bru")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := runBrunoImport(t.Context(), brunoImportArgs{
		path:           link,
		projectID:      "project-1",
		api:            "http://127.0.0.1:1",
		timeout:        time.Second,
		secretBindings: map[string]string{},
		conflicts:      map[string]string{},
	})
	if err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("top-level symlink error = %v", err)
	}
}

func TestBrunoCLIResponseErrorExplainsMissingBindings(t *testing.T) {
	response := &http.Response{
		Status:     "409 Conflict",
		StatusCode: http.StatusConflict,
		Body: io.NopCloser(strings.NewReader(`{
		  "error":"Bruno secret bindings are required before commit",
		  "requirements":[{"id":"req-1","suggested_name":"PASSWORD"}]
		}`)),
	}
	err := brunoCLIResponseError("commit Bruno import", response)
	if err == nil || !strings.Contains(err.Error(), "--secret-binding") || !strings.Contains(err.Error(), "req-1") {
		t.Fatalf("binding error = %v", err)
	}
}
