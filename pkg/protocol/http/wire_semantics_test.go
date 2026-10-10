package http

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHeaderListIssuesPreserveOrdinaryDuplicatesAndIdentifyUnsupportedOccurrences(t *testing.T) {
	t.Parallel()

	t.Run("ordinary duplicates", func(t *testing.T) {
		t.Parallel()
		headers := protocol.HeaderList{
			{Name: "X-Request-ID", Value: "first"},
			{Name: "x-request-id", Value: "second"},
		}
		if issues := HeaderListIssues(headers); len(issues) != 0 {
			t.Fatalf("HeaderListIssues() = %#v, want no issues", issues)
		}
		if err := ValidateHeaderList(headers); err != nil {
			t.Fatalf("ValidateHeaderList(): %v", err)
		}
	})

	t.Run("duplicate singleton", func(t *testing.T) {
		t.Parallel()
		headers := protocol.HeaderList{
			{Name: "Host", Value: "first.example.test"},
			{Name: "host", Value: "second.example.test"},
			{Name: "User-Agent", Value: "one"},
			{Name: "user-agent", Value: "two"},
		}
		issues := HeaderListIssues(headers)
		if len(issues) != 2 {
			t.Fatalf("HeaderListIssues() = %#v, want two duplicate-singleton issues", issues)
		}
		if issues[0].Code != HeaderListIssueDuplicateSingleton || issues[0].Index != 1 || issues[0].Name != "host" {
			t.Fatalf("Host issue = %#v", issues[0])
		}
		if issues[1].Code != HeaderListIssueDuplicateSingleton || issues[1].Index != 3 || issues[1].Name != "user-agent" {
			t.Fatalf("User-Agent issue = %#v", issues[1])
		}

		var validationErr *HeaderListValidationError
		if err := ValidateHeaderList(headers); !errors.As(err, &validationErr) || len(validationErr.Issues) != 2 {
			t.Fatalf("ValidateHeaderList() = %v, want structured two-issue error", err)
		}
	})

	t.Run("transport owned", func(t *testing.T) {
		t.Parallel()
		headers := protocol.HeaderList{
			{Name: "Content-Length", Value: "4"},
			{Name: "Transfer-Encoding", Value: "chunked"},
			{Name: "Trailer", Value: "X-Checksum"},
		}
		issues := HeaderListIssues(headers)
		if len(issues) != len(headers) {
			t.Fatalf("HeaderListIssues() = %#v", issues)
		}
		for index, issue := range issues {
			if issue.Code != HeaderListIssueTransportOwned || issue.Index != index {
				t.Fatalf("issue[%d] = %#v", index, issue)
			}
		}
	})

	t.Run("proxy authorization is always transport owned", func(t *testing.T) {
		t.Parallel()
		headers := protocol.HeaderList{{Name: "Proxy-Authorization", Value: "Basic opaque"}}
		for _, version := range []string{"", VersionHTTP11, VersionHTTP2} {
			issues := HeaderListIssuesForVersion(headers, version)
			if len(issues) != 1 ||
				issues[0].Code != HeaderListIssueTransportOwned ||
				issues[0].Index != 0 {
				t.Fatalf("version %q issues = %#v", version, issues)
			}
		}
	})

	t.Run("cookies require parsed syntax and preserve valid occurrences", func(t *testing.T) {
		t.Parallel()
		valid := protocol.HeaderList{
			{Name: "Cookie", Value: "first=one; shared=authored"},
			{Name: "cookie", Value: `second="two words"`},
		}
		if issues := HeaderListIssues(valid); len(issues) != 0 {
			t.Fatalf("valid Cookie issues = %#v", issues)
		}

		const canary = "cookie-canary"
		invalid := protocol.HeaderList{{Name: "Cookie", Value: "session=" + canary + "; malformed"}}
		issues := HeaderListIssues(invalid)
		if len(issues) != 1 || issues[0].Code != HeaderListIssueInvalidValue || issues[0].Index != 0 {
			t.Fatalf("invalid Cookie issues = %#v", issues)
		}
		if strings.Contains((&HeaderListValidationError{Issues: issues}).Error(), canary) {
			t.Fatal("Cookie validation error disclosed the authored value")
		}

		templates := protocol.HeaderList{
			{Name: "Cookie", Value: "{{secret.COOKIE}}"},
			{Name: "cookie", Value: "session={{secret.SESSION}}"},
		}
		if issues := HeaderListIssuesForVersionWithUnresolvedTemplates(templates, VersionHTTP11); len(issues) != 0 {
			t.Fatalf("template-aware Cookie issues = %#v", issues)
		}
		if issues := HeaderListIssuesForVersion(templates[:1], VersionHTTP11); len(issues) != 1 ||
			issues[0].Code != HeaderListIssueInvalidValue {
			t.Fatalf("strict whole-template Cookie issues = %#v", issues)
		}
	})

	t.Run("empty Host is never an executable fallback", func(t *testing.T) {
		t.Parallel()
		empty := protocol.HeaderList{{Name: "Host", Value: ""}}
		issues := HeaderListIssuesForVersion(empty, VersionHTTP11)
		if len(issues) != 1 || issues[0].Code != HeaderListIssueInvalidValue {
			t.Fatalf("empty Host issues = %#v", issues)
		}
		unresolved := protocol.HeaderList{{Name: "Host", Value: "{{var.virtual_host}}"}}
		if issues := HeaderListIssuesForVersionWithUnresolvedTemplates(unresolved, VersionHTTP11); len(issues) != 0 {
			t.Fatalf("unresolved Host template issues = %#v", issues)
		}
		if issues := HeaderListIssuesForVersion(
			protocol.HeaderList{{Name: "Host", Value: ""}},
			VersionHTTP11,
		); len(issues) != 1 {
			t.Fatalf("resolved-empty Host issues = %#v", issues)
		}
	})

	t.Run("empty User-Agent is not silently omitted", func(t *testing.T) {
		t.Parallel()
		empty := protocol.HeaderList{{Name: "User-Agent", Value: ""}}
		issues := HeaderListIssuesForVersion(empty, VersionHTTP11)
		if len(issues) != 1 || issues[0].Code != HeaderListIssueInvalidValue {
			t.Fatalf("empty User-Agent issues = %#v", issues)
		}
		unresolved := protocol.HeaderList{{Name: "User-Agent", Value: "{{var.user_agent}}"}}
		if issues := HeaderListIssuesForVersionWithUnresolvedTemplates(unresolved, VersionHTTP11); len(issues) != 0 {
			t.Fatalf("unresolved User-Agent template issues = %#v", issues)
		}
	})

	t.Run("Expect 100-continue is version sensitive across occurrences", func(t *testing.T) {
		t.Parallel()
		lateContinue := protocol.HeaderList{
			{Name: "Expect", Value: "custom-expectation"},
			{Name: "expect", Value: "100-Continue"},
		}
		// auto+h3 may still use HTTP/1.1 over TCP, so the rule applies to it.
		for _, version := range []string{"", VersionHTTP11, VersionAutoH3} {
			issues := HeaderListIssuesForVersion(lateContinue, version)
			if len(issues) != 1 ||
				issues[0].Code != HeaderListIssueExpectContinueUnsupported ||
				issues[0].Index != 1 {
				t.Fatalf("version %q issues = %#v", version, issues)
			}
		}
		for _, version := range []string{VersionHTTP2, VersionHTTP3} {
			if issues := HeaderListIssuesForVersion(lateContinue, version); len(issues) != 0 {
				t.Fatalf("version %q issues = %#v", version, issues)
			}
		}

		firstContinue := protocol.HeaderList{
			{Name: "Expect", Value: "custom, 100-continue"},
			{Name: "Expect", Value: "another-expectation"},
		}
		if issues := HeaderListIssuesForVersion(firstContinue, VersionHTTP11); len(issues) != 0 {
			t.Fatalf("first-occurrence 100-continue issues = %#v", issues)
		}
	})

	t.Run("hop by hop is version sensitive", func(t *testing.T) {
		t.Parallel()
		headers := protocol.HeaderList{
			{Name: "Connection", Value: "keep-alive"},
			{Name: "TE", Value: "trailers"},
			{Name: "Upgrade", Value: "websocket"},
		}
		if issues := HeaderListIssuesForVersion(headers, VersionHTTP11); len(issues) != 0 {
			t.Fatalf("HTTP/1.1 issues = %#v", issues)
		}
		for _, version := range []string{"", VersionHTTP2, VersionHTTP3, VersionAutoH3} {
			issues := HeaderListIssuesForVersion(headers, version)
			if len(issues) != len(headers) {
				t.Fatalf("version %q issues = %#v", version, issues)
			}
			for index, issue := range issues {
				if issue.Code != HeaderListIssueHopByHopUnsupported || issue.Index != index {
					t.Fatalf("version %q issue[%d] = %#v", version, index, issue)
				}
			}
		}
	})

	t.Run("invalid data is value safe", func(t *testing.T) {
		t.Parallel()
		const canary = "do-not-disclose"
		headers := protocol.HeaderList{
			{Name: "Bad " + canary, Value: canary},
			{Name: "X-Invalid", Value: canary + "\n"},
		}
		issues := HeaderListIssues(headers)
		if len(issues) != 2 ||
			issues[0].Code != HeaderListIssueInvalidName ||
			issues[0].Name != "<invalid>" ||
			issues[1].Code != HeaderListIssueInvalidValue {
			t.Fatalf("issues = %#v", issues)
		}
		err := ValidateHeaderList(headers)
		if err == nil || strings.Contains(err.Error(), canary) {
			t.Fatalf("validation error = %q, want disclosure-safe error", err)
		}
	})

	t.Run("unresolved templates are isolated from resolved byte checks", func(t *testing.T) {
		t.Parallel()
		headers := protocol.HeaderList{
			{Name: "Host", Value: "{{var.host}}"},
			{Name: "X-{{var.suffix}}", Value: "prefix-{{secret.VALUE}}"},
		}
		if issues := HeaderListIssuesForVersionWithUnresolvedTemplates(headers, VersionHTTP11); len(issues) != 0 {
			t.Fatalf("template-aware issues = %#v", issues)
		}
		if issues := HeaderListIssuesForVersion(headers, VersionHTTP11); len(issues) == 0 {
			t.Fatal("strict resolved-value validation accepted unresolved templates")
		}

		headers[1].Value += "\r\nInjected: true"
		issues := HeaderListIssuesForVersionWithUnresolvedTemplates(headers, VersionHTTP11)
		if len(issues) != 1 || issues[0].Code != HeaderListIssueInvalidValue || issues[0].Index != 1 {
			t.Fatalf("literal-control issues = %#v", issues)
		}
	})

	t.Run("header maps reject case-insensitive ambiguity", func(t *testing.T) {
		t.Parallel()
		issues := HeaderMapIssuesForVersion(map[string]string{
			"X-Request-ID": "first",
			"x-request-id": "second",
		}, VersionHTTP11)
		if len(issues) != 1 || issues[0].Code != HeaderListIssueAmbiguousMap {
			t.Fatalf("HeaderMapIssuesForVersion() = %#v", issues)
		}
		if issues := HeaderMapIssuesForVersionWithUnresolvedTemplates(
			map[string]string{"Host": "{{var.host}}"},
			VersionHTTP11,
		); len(issues) != 0 {
			t.Fatalf("template-valued HeaderMap issues = %#v", issues)
		}
		if issues := HeaderMapIssuesForVersionWithUnresolvedTemplates(
			map[string]string{"X-{{var.suffix}}": "value"},
			VersionHTTP11,
		); len(issues) != 1 || issues[0].Code != HeaderListIssueInvalidName {
			t.Fatalf("template-keyed HeaderMap issues = %#v", issues)
		}
	})
}

func TestHTTPDiagnosticsDoNotDiscloseRequestURLCredentials(t *testing.T) {
	t.Parallel()

	const canary = "credential-canary-never-disclose"
	runtime := initHTTP(t, map[string]any{"base_url": "https://example.test"})
	result, err := runtime.Execute(context.Background(), &protocol.Action{
		Name:           "invalid credential URL",
		Method:         http.MethodGet,
		Target:         "https://user:" + canary + "@example.test/\x7f",
		RedactedTarget: "https://example.test/[REDACTED]",
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if result.ErrorMessage != "invalid HTTP request target" {
		t.Fatalf("ErrorMessage = %q, want stable value-free diagnostic", result.ErrorMessage)
	}
	if strings.Contains(result.ErrorMessage, canary) ||
		result.Snapshot == nil ||
		strings.Contains(result.Snapshot.RequestURL, canary) {
		t.Fatalf("result disclosed URL credential: %#v", result)
	}

	transportErr := &url.Error{
		Op:  "Get",
		URL: "https://user:" + canary + "@example.test/private?token=" + canary,
		Err: errors.New("dial failed"),
	}
	if got := runtime.transportErrorMessage(transportErr); got != "dial failed" || strings.Contains(got, canary) {
		t.Fatalf("transportErrorMessage() = %q, want safe underlying cause", got)
	}

	deadlineErr := &url.Error{
		Op:  "Get",
		URL: "https://user:" + canary + "@example.test/private?token=" + canary,
		Err: context.DeadlineExceeded,
	}
	if got := runtime.transportErrorMessage(deadlineErr); got != "request timeout: context deadline exceeded" ||
		strings.Contains(got, canary) {
		t.Fatalf("transportErrorMessage() = %q, want stable value-free timeout diagnostic", got)
	}
}

func TestEffectiveAuthoredHeaderListAppliesDefaultAndActionPrecedence(t *testing.T) {
	defaults := map[string]string{
		"User-Agent": "typed-agent",
		"X-Legacy":   "legacy",
	}
	defaultList := protocol.HeaderList{
		{Name: "X-Legacy", Value: "ordered"},
		{Name: "X-Repeat", Value: "one"},
		{Name: "X-Repeat", Value: "two"},
		{Name: "Cookie", Value: "dark=true"},
	}
	actionHeaders := map[string]string{"X-Legacy": "action-map"}
	actionList := protocol.HeaderList{
		{Name: "Cookie", Value: "action=true"},
		{Name: "X-Action", Value: "yes"},
	}

	got, issues := effectiveAuthoredHeaderList(defaults, defaultList, actionHeaders, actionList)
	if len(issues) != 0 {
		t.Fatalf("effectiveAuthoredHeaderList issues = %#v", issues)
	}
	want := protocol.HeaderList{
		{Name: "User-Agent", Value: "typed-agent"},
		{Name: "X-Repeat", Value: "one"},
		{Name: "X-Repeat", Value: "two"},
		{Name: "X-Legacy", Value: "action-map"},
		{Name: "Cookie", Value: "action=true"},
		{Name: "X-Action", Value: "yes"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effective headers = %#v, want %#v", got, want)
	}
}

func TestEffectiveAuthoredHeaderListDisabledOrderedEntrySuppressesCompatibilityMap(t *testing.T) {
	disabled := false
	got, issues := effectiveAuthoredHeaderList(
		map[string]string{"X-Disabled": "stale-default", "X-Keep": "default"},
		protocol.HeaderList{{Name: "x-disabled", Value: "not-sent", Enabled: &disabled}},
		map[string]string{"X-Action-Disabled": "stale-action"},
		protocol.HeaderList{{Name: "x-action-disabled", Value: "not-sent", Enabled: &disabled}},
	)
	if len(issues) != 0 {
		t.Fatalf("effectiveAuthoredHeaderList issues = %#v", issues)
	}
	want := protocol.HeaderList{{Name: "X-Keep", Value: "default"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effective headers = %#v, want %#v", got, want)
	}
}

func TestHTTPFailureSnapshotRedactsScriptLevelTypedAuthorization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		config map[string]any
		canary string
		want   string
	}{
		{
			name: "basic",
			config: map[string]any{
				"auth_type":     AuthTypeBasic,
				"auth_username": "snapshot-user",
				"auth_password": "snapshot-basic-canary",
			},
			canary: "c25hcHNob3QtdXNlcjpzbmFwc2hvdC1iYXNpYy1jYW5hcnk=",
			want:   "Basic [REDACTED]",
		},
		{
			name: "bearer",
			config: map[string]any{
				"auth_type":  AuthTypeBearer,
				"auth_token": "snapshot-bearer-canary",
			},
			canary: "snapshot-bearer-canary",
			want:   "Bearer [REDACTED]",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runtime := initHTTP(t, map[string]any{
				"base_url":      "https://example.test",
				"auth_type":     test.config["auth_type"],
				"auth_username": test.config["auth_username"],
				"auth_password": test.config["auth_password"],
				"auth_token":    test.config["auth_token"],
			})
			runtime.client.Transport = digestRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("failed")),
					Request:    request,
				}, nil
			})
			result, err := runtime.Execute(context.Background(), &protocol.Action{
				Name:   "typed auth failure",
				Method: http.MethodGet,
				Target: "/failed",
			})
			if err != nil {
				t.Fatalf("Execute(): %v", err)
			}
			if result.Snapshot == nil {
				t.Fatalf("result = %#v, want failure snapshot", result)
			}
			if got := result.Snapshot.RequestHeaders["Authorization"]; got != test.want {
				t.Fatalf("snapshot Authorization = %q, want %q", got, test.want)
			}
			if payload := fmt.Sprintf("%#v", result.Snapshot); strings.Contains(payload, test.canary) {
				t.Fatalf("failure snapshot disclosed typed auth canary: %s", payload)
			}
		})
	}
}

func TestHTTPExecuteRejectsUnsupportedHeaderWireSemanticsBeforeTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		config     map[string]any
		headers    map[string]string
		headerList protocol.HeaderList
		auth       *protocol.AuthConfig
	}{
		{
			name: "duplicate Host list",
			headerList: protocol.HeaderList{
				{Name: "Host", Value: "first.example.test"},
				{Name: "host", Value: "second.example.test"},
			},
		},
		{
			name:       "explicit empty Host",
			headerList: protocol.HeaderList{{Name: "Host", Value: ""}},
		},
		{
			name:       "template resolved to empty Host",
			headerList: protocol.HeaderList{{Name: "Host", Value: ""}},
		},
		{
			name:       "explicit empty User-Agent",
			headerList: protocol.HeaderList{{Name: "User-Agent", Value: ""}},
		},
		{
			name: "bearer token with a line break",
			auth: &protocol.AuthConfig{
				Type:  AuthTypeBearer,
				Token: "token\r\nInjected: yes",
			},
		},
		{
			name:    "map-only Content-Length",
			headers: map[string]string{"Content-Length": "999"},
		},
		{
			name:   "default Transfer-Encoding",
			config: map[string]any{"default_headers": map[string]string{"Transfer-Encoding": "chunked"}},
		},
		{
			name:       "auto Connection list",
			headerList: protocol.HeaderList{{Name: "Connection", Value: "close"}},
		},
		{
			name: "case-insensitive action map collision",
			headers: map[string]string{
				"X-Request-ID": "first",
				"x-request-id": "second",
			},
		},
		{
			name:       "malformed Cookie",
			headerList: protocol.HeaderList{{Name: "Cookie", Value: "session=one; malformed"}},
		},
		{
			name:   "late 100-continue in auto mode",
			config: map[string]any{},
			headerList: protocol.HeaderList{
				{Name: "Expect", Value: "custom-expectation"},
				{Name: "Expect", Value: "100-continue"},
			},
		},
		{
			name:   "late 100-continue in HTTP1",
			config: map[string]any{"http_version": VersionHTTP11},
			headerList: protocol.HeaderList{
				{Name: "Expect", Value: "custom-expectation"},
				{Name: "Expect", Value: "100-continue"},
			},
		},
		{
			name:       "Proxy-Authorization in auto mode",
			headerList: protocol.HeaderList{{Name: "Proxy-Authorization", Value: "Basic opaque"}},
		},
		{
			name:       "Proxy-Authorization in HTTP1",
			config:     map[string]any{"http_version": VersionHTTP11},
			headerList: protocol.HeaderList{{Name: "Proxy-Authorization", Value: "Basic opaque"}},
		},
		{
			name:       "Proxy-Authorization in HTTP2",
			config:     map[string]any{"http_version": VersionHTTP2},
			headerList: protocol.HeaderList{{Name: "Proxy-Authorization", Value: "Basic opaque"}},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			config := make(map[string]any, len(test.config)+1)
			for key, value := range test.config {
				config[key] = value
			}
			config["base_url"] = server.URL
			runtime := initHTTP(t, config)
			result, err := runtime.Execute(context.Background(), &protocol.Action{
				Name:       "must-not-send",
				Method:     http.MethodGet,
				Target:     "/",
				Headers:    test.headers,
				HeaderList: test.headerList,
				Auth:       test.auth,
			})
			if err != nil {
				t.Fatalf("Execute(): %v", err)
			}
			if result.StatusCode != http.StatusBadRequest || result.ErrorMessage == "" {
				t.Fatalf("result = %#v, want pre-transport bad request", result)
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("server received %d request(s), want zero", got)
			}
		})
	}
}

func TestHTTPExecuteMergesAuthoredCookiesBeforeJarInjection(t *testing.T) {
	t.Parallel()

	seen := make(chan []string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/seed":
			http.SetCookie(w, &http.Cookie{Name: "jar", Value: "three", Path: "/"})
			w.WriteHeader(http.StatusNoContent)
		case "/check":
			seen <- append([]string(nil), request.Header.Values("Cookie")...)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	runtime := initHTTP(t, map[string]any{
		"base_url":     server.URL,
		"http_version": VersionHTTP11,
	})
	defer runtime.Teardown(context.Background())

	seed, err := runtime.Execute(context.Background(), &protocol.Action{
		Name: "seed jar", Method: http.MethodGet, Target: "/seed",
	})
	if err != nil {
		t.Fatalf("seed Execute(): %v", err)
	}
	if !seed.Success {
		t.Fatalf("seed result = %#v", seed)
	}

	result, err := runtime.Execute(context.Background(), &protocol.Action{
		Name: "authored cookies", Method: http.MethodGet, Target: "/check",
		HeaderList: protocol.HeaderList{
			{Name: "Cookie", Value: "first=one"},
			{Name: "cookie", Value: "second=two"},
		},
	})
	if err != nil {
		t.Fatalf("check Execute(): %v", err)
	}
	if !result.Success {
		t.Fatalf("check result = %#v", result)
	}

	if got, want := <-seen, []string{"first=one; second=two; jar=three"}; !slices.Equal(got, want) {
		t.Fatalf("Cookie fields = %#v, want %#v", got, want)
	}
}

func TestHTTP11ExpectContinueWaitsForInterimResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(): %v", err)
	}
	defer listener.Close()

	body := bytes.Repeat([]byte("request-body-"), 64)
	serverErr := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- fmt.Errorf("accept: %w", acceptErr)
			return
		}
		defer connection.Close()

		reader := bufio.NewReader(connection)
		expectSeen := false
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				serverErr <- fmt.Errorf("read request headers: %w", readErr)
				return
			}
			if strings.EqualFold(strings.TrimSpace(line), "Expect: 100-continue") {
				expectSeen = true
			}
			if line == "\r\n" {
				break
			}
		}
		if !expectSeen {
			serverErr <- errors.New("request did not contain Expect: 100-continue")
			return
		}

		if deadlineErr := connection.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); deadlineErr != nil {
			serverErr <- fmt.Errorf("set pre-continue deadline: %w", deadlineErr)
			return
		}
		if _, readErr := reader.Peek(1); readErr == nil {
			serverErr <- errors.New("request body arrived before 100 Continue")
			return
		} else if netErr, ok := readErr.(net.Error); !ok || !netErr.Timeout() {
			serverErr <- fmt.Errorf("pre-continue body read: %w", readErr)
			return
		}
		if deadlineErr := connection.SetReadDeadline(time.Time{}); deadlineErr != nil {
			serverErr <- fmt.Errorf("clear pre-continue deadline: %w", deadlineErr)
			return
		}
		if _, writeErr := io.WriteString(connection, "HTTP/1.1 100 Continue\r\n\r\n"); writeErr != nil {
			serverErr <- fmt.Errorf("write 100 Continue: %w", writeErr)
			return
		}

		gotBody := make([]byte, len(body))
		if _, readErr := io.ReadFull(reader, gotBody); readErr != nil {
			serverErr <- fmt.Errorf("read request body: %w", readErr)
			return
		}
		if !bytes.Equal(gotBody, body) {
			serverErr <- errors.New("request body changed after 100 Continue")
			return
		}
		if _, writeErr := io.WriteString(connection, "HTTP/1.1 204 No Content\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"); writeErr != nil {
			serverErr <- fmt.Errorf("write final response: %w", writeErr)
			return
		}
		serverErr <- nil
	}()

	runtime := initHTTP(t, map[string]any{
		"base_url":           "http://" + listener.Addr().String(),
		"http_version":       VersionHTTP11,
		"request_timeout_ms": float64(5_000),
	})
	defer runtime.Teardown(context.Background())
	if got := runtime.transport.ExpectContinueTimeout; got != defaultHTTPExpectContinueTimeout || got <= 0 {
		t.Fatalf("ExpectContinueTimeout = %v, want bounded default %v", got, defaultHTTPExpectContinueTimeout)
	}

	result, err := runtime.Execute(context.Background(), &protocol.Action{
		Name: "expect continue", Method: http.MethodPost, Target: "/upload",
		Body:       body,
		HeaderList: protocol.HeaderList{{Name: "Expect", Value: "100-continue"}},
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %#v", result)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("raw HTTP server did not finish")
	}
}

func TestHTTP11DoesNotInjectCompressionOrUserAgentDefaults(t *testing.T) {
	payload := []byte("decoded-response")
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	if _, err := gzipWriter.Write(payload); err != nil {
		t.Fatalf("compress response: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close compressed response: %v", err)
	}

	tests := []struct {
		name               string
		headers            protocol.HeaderList
		wantUserAgent      string
		wantUserAgentField bool
		wantEncoding       string
		wantEncodingField  bool
	}{
		{name: "absent fields stay absent"},
		{
			name: "explicit empty Accept-Encoding remains a field",
			headers: protocol.HeaderList{
				{Name: "Accept-Encoding", Value: ""},
			},
			wantEncodingField: true,
		},
		{
			name: "explicit values remain exact",
			headers: protocol.HeaderList{
				{Name: "User-Agent", Value: "Imported-Agent/1.0"},
				{Name: "Accept-Encoding", Value: "gzip"},
			},
			wantUserAgent:      "Imported-Agent/1.0",
			wantUserAgentField: true,
			wantEncoding:       "gzip",
			wantEncodingField:  true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("Listen(): %v", err)
			}
			defer listener.Close()

			rawHeaders := make(chan http.Header, 1)
			serverErr := make(chan error, 1)
			go func() {
				connection, acceptErr := listener.Accept()
				if acceptErr != nil {
					serverErr <- fmt.Errorf("accept: %w", acceptErr)
					return
				}
				defer connection.Close()

				reader := bufio.NewReader(connection)
				if _, readErr := reader.ReadString('\n'); readErr != nil {
					serverErr <- fmt.Errorf("read request line: %w", readErr)
					return
				}
				headers := make(http.Header)
				for {
					line, readErr := reader.ReadString('\n')
					if readErr != nil {
						serverErr <- fmt.Errorf("read request headers: %w", readErr)
						return
					}
					if line == "\r\n" {
						break
					}
					name, value, found := strings.Cut(strings.TrimSuffix(line, "\r\n"), ":")
					if !found {
						serverErr <- fmt.Errorf("malformed raw request header")
						return
					}
					headers.Add(name, strings.TrimSpace(value))
				}
				rawHeaders <- headers

				response := "HTTP/1.1 200 OK\r\n" +
					"Content-Encoding: gzip\r\n" +
					"Content-Length: " + strconv.Itoa(compressed.Len()) + "\r\n" +
					"Connection: close\r\n\r\n"
				if _, writeErr := io.WriteString(connection, response); writeErr != nil {
					serverErr <- fmt.Errorf("write response headers: %w", writeErr)
					return
				}
				if _, writeErr := connection.Write(compressed.Bytes()); writeErr != nil {
					serverErr <- fmt.Errorf("write response body: %w", writeErr)
					return
				}
				serverErr <- nil
			}()

			runtime := initHTTP(t, map[string]any{
				"base_url":            "http://" + listener.Addr().String(),
				"http_version":        VersionHTTP11,
				"traceparent_enabled": false,
			})
			defer runtime.Teardown(context.Background())
			if !runtime.transport.DisableCompression {
				t.Fatal("transport implicit compression remains enabled")
			}

			result, err := runtime.Execute(context.Background(), &protocol.Action{
				Name: "raw defaults", Method: http.MethodGet, Target: "/", HeaderList: test.headers,
			})
			if err != nil {
				t.Fatalf("Execute(): %v", err)
			}
			if !result.Success || result.ResponseBody != string(payload) {
				t.Fatalf("result = %#v, want decoded response", result)
			}

			headers := <-rawHeaders
			gotUserAgent, hasUserAgent := headers["User-Agent"]
			if hasUserAgent != test.wantUserAgentField ||
				hasUserAgent && (len(gotUserAgent) != 1 || gotUserAgent[0] != test.wantUserAgent) {
				t.Fatalf("raw User-Agent = %#v (present %t)", gotUserAgent, hasUserAgent)
			}
			gotEncoding, hasEncoding := headers["Accept-Encoding"]
			if hasEncoding != test.wantEncodingField ||
				hasEncoding && (len(gotEncoding) != 1 || gotEncoding[0] != test.wantEncoding) {
				t.Fatalf("raw Accept-Encoding = %#v (present %t)", gotEncoding, hasEncoding)
			}
			if _, present := headers["Traceparent"]; present {
				t.Fatalf("raw Traceparent was generated despite explicit false: %#v", headers["Traceparent"])
			}
			if err := <-serverErr; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHTTPVersionCapabilityAndHTTP10PreTransportRejection(t *testing.T) {
	t.Parallel()

	// The HTTP/3 policies follow APEXLOAD_HTTP3 (TestHTTP3VersionVocabulary);
	// the TCP versions always lead the list.
	if got, want := SupportedVersions(), []string{VersionHTTP11, VersionHTTP2}; len(got) < 2 || !slices.Equal(got[:2], want) {
		t.Fatalf("SupportedVersions() = %#v, want it to start with %#v", got, want)
	}
	for _, version := range []string{"", " 1.1 ", "HTTP/1.1", "2", "HTTP/2", "HTTP/2.0"} {
		if !VersionExecutable(version) {
			t.Fatalf("VersionExecutable(%q) = false", version)
		}
		if err := ValidateVersion(version); err != nil {
			t.Fatalf("ValidateVersion(%q): %v", version, err)
		}
	}
	if VersionExecutable("1.0") {
		t.Fatal("VersionExecutable(1.0) = true")
	}
	if err := ValidateVersion("1.0"); !errors.Is(err, ErrHTTP10Unsupported) {
		t.Fatalf("ValidateVersion(1.0) = %v", err)
	}
	if err := ValidateVersion("HTTP/1.0"); !errors.Is(err, ErrHTTP10Unsupported) {
		t.Fatalf("ValidateVersion(HTTP/1.0) = %v", err)
	}
	var unsupported *UnsupportedVersionError
	// 3 is now policy vocabulary; a draft token stays unsupported.
	if err := ValidateVersion("h3-29"); !errors.Is(err, ErrUnsupportedVersion) || !errors.As(err, &unsupported) {
		t.Fatalf("ValidateVersion(h3-29) = %v, want structured unsupported-version error", err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	runtime := new(HTTP)
	err := runtime.Init(context.Background(), map[string]any{
		"base_url":     server.URL,
		"http_version": "1.0",
	})
	if !errors.Is(err, ErrHTTP10Unsupported) {
		t.Fatalf("Init() = %v, want ErrHTTP10Unsupported", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("server received %d request(s), want zero", got)
	}
}

func TestHTTPVersion2RequiresTLSHTTP2WithoutFallback(t *testing.T) {
	t.Parallel()

	t.Run("negotiates TLS HTTP2", func(t *testing.T) {
		t.Parallel()
		var protocolSeen atomic.Value
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			protocolSeen.Store(request.Proto)
			w.WriteHeader(http.StatusNoContent)
		}))
		server.EnableHTTP2 = true
		server.StartTLS()
		defer server.Close()

		runtime := initHTTP(t, map[string]any{
			"base_url":                 server.URL,
			"http_version":             VersionHTTP2,
			"tls_insecure_skip_verify": true,
		})
		result, err := runtime.Execute(context.Background(), &protocol.Action{
			Name:   "tls-h2",
			Type:   http.MethodGet,
			Target: "/",
		})
		if err != nil {
			t.Fatalf("Execute(): %v", err)
		}
		if !result.Success {
			t.Fatalf("result = %#v", result)
		}
		if got, _ := protocolSeen.Load().(string); got != "HTTP/2.0" {
			t.Fatalf("server protocol = %q, want HTTP/2.0", got)
		}
	})

	t.Run("does not fall back to TLS HTTP1", func(t *testing.T) {
		t.Parallel()
		var requests atomic.Int32
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}))
		server.Config.ErrorLog = log.New(io.Discard, "", 0)
		server.StartTLS()
		defer server.Close()

		runtime := initHTTP(t, map[string]any{
			"base_url":                 server.URL,
			"http_version":             VersionHTTP2,
			"tls_insecure_skip_verify": true,
		})
		result, err := runtime.Execute(context.Background(), &protocol.Action{
			Name:   "tls-h2-required",
			Type:   http.MethodGet,
			Target: "/?token=do-not-disclose",
		})
		if err != nil {
			t.Fatalf("Execute(): %v", err)
		}
		if want := ErrHTTP2Required.Error() + ": transport request failed"; result.Success || result.ErrorMessage != want {
			t.Fatalf("result = %#v, want stable HTTP/2-required failure", result)
		}
		if strings.Contains(result.ErrorMessage, "do-not-disclose") {
			t.Fatalf("HTTP/2 failure disclosed request data: %q", result.ErrorMessage)
		}
		if got := requests.Load(); got != 0 {
			t.Fatalf("HTTP/1 handler received %d request(s), want zero", got)
		}
	})
}

func TestHTTPVersion2UsesPriorKnowledgeH2C(t *testing.T) {
	t.Parallel()

	var protocolSeen atomic.Value
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		protocolSeen.Store(request.Proto)
		w.WriteHeader(http.StatusNoContent)
	}))
	serverProtocols := new(http.Protocols)
	serverProtocols.SetHTTP1(true)
	serverProtocols.SetUnencryptedHTTP2(true)
	server.Config.Protocols = serverProtocols
	server.Start()
	defer server.Close()

	runtime := initHTTP(t, map[string]any{
		"base_url":     server.URL,
		"http_version": VersionHTTP2,
	})
	result, err := runtime.Execute(context.Background(), &protocol.Action{
		Name:   "h2c",
		Type:   http.MethodGet,
		Target: "/",
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %#v", result)
	}
	if got, _ := protocolSeen.Load().(string); got != "HTTP/2.0" {
		t.Fatalf("server protocol = %q, want HTTP/2.0", got)
	}
}

func TestHTTPVersion11RemainsExactAgainstHTTP2Server(t *testing.T) {
	t.Parallel()

	var protocolSeen atomic.Value
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		protocolSeen.Store(request.Proto)
		w.WriteHeader(http.StatusNoContent)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	runtime := initHTTP(t, map[string]any{
		"base_url":                 server.URL,
		"http_version":             VersionHTTP11,
		"tls_insecure_skip_verify": true,
	})
	result, err := runtime.Execute(context.Background(), &protocol.Action{
		Name:   "tls-h1",
		Type:   http.MethodGet,
		Target: "/",
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %#v", result)
	}
	if got, _ := protocolSeen.Load().(string); got != "HTTP/1.1" {
		t.Fatalf("server protocol = %q, want HTTP/1.1", got)
	}
}

func TestHTTPVersion11PreservesConnectionSpecificHeaders(t *testing.T) {
	t.Parallel()

	seen := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seen <- request.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	runtime := initHTTP(t, map[string]any{
		"base_url":     server.URL,
		"http_version": VersionHTTP11,
	})
	result, err := runtime.Execute(context.Background(), &protocol.Action{
		Name:   "h1-connection-fields",
		Type:   http.MethodGet,
		Target: "/",
		HeaderList: protocol.HeaderList{
			{Name: "Connection", Value: "keep-alive"},
			{Name: "Keep-Alive", Value: "timeout=5"},
			{Name: "Proxy-Connection", Value: "keep-alive"},
			{Name: "TE", Value: "trailers"},
			{Name: "Upgrade", Value: "example/1"},
		},
	})
	if err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if !result.Success {
		t.Fatalf("result = %#v", result)
	}
	headers := <-seen
	for name, want := range map[string]string{
		"Connection":       "keep-alive",
		"Keep-Alive":       "timeout=5",
		"Proxy-Connection": "keep-alive",
		"Te":               "trailers",
		"Upgrade":          "example/1",
	} {
		if got := headers.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}
