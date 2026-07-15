package dsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBodyRefsResolvesMultilineFileBody(t *testing.T) {
	dir := t.TempDir()
	body := "{\n  \"account\": \"acct-123\",\n  \"amount\": 42\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "payload.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	script := &Script{
		Actions: []Action{{
			Name:     "create",
			Type:     "POST",
			Target:   "/orders",
			BodyFile: "payload.json",
		}},
	}

	if err := ResolveBodyRefs(script, dir); err != nil {
		t.Fatalf("ResolveBodyRefs: %v", err)
	}
	if got := script.Actions[0].Body; got != body {
		t.Fatalf("body = %q, want %q", got, body)
	}
	if script.Actions[0].BodyFile != "" {
		t.Fatalf("body_file = %q, want cleared", script.Actions[0].BodyFile)
	}
}

func TestResolveBodyRefsRejectsEscapingBaseDir(t *testing.T) {
	dir := t.TempDir()
	parentPayload := filepath.Join(filepath.Dir(dir), "outside-payload.txt")
	if err := os.WriteFile(parentPayload, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	script := &Script{Actions: []Action{{
		Name:     "escape",
		Type:     "POST",
		Target:   "/",
		BodyFile: filepath.Join("..", filepath.Base(parentPayload)),
	}}}

	err := ResolveBodyRefs(script, dir)
	if err == nil {
		t.Fatal("expected escaping body_file error")
	}
	if !strings.Contains(err.Error(), "escapes script directory") {
		t.Fatalf("error = %q, want escape message", err.Error())
	}
}

func TestResolveBodyRefsRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-payload.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "payload.txt")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	script := &Script{Actions: []Action{{Name: "escape", Type: "POST", Target: "/", BodyFile: "payload.txt"}}}

	err := ResolveBodyRefs(script, dir)
	if err == nil || !strings.Contains(err.Error(), "script directory") {
		t.Fatalf("symlink escape error = %v, want confined-root rejection", err)
	}
}

func TestResolveBodyRefsRejectsOversizedBodyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oversized.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create body file: %v", err)
	}
	if err := file.Truncate(MaxBodyRefBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("truncate body file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close body file: %v", err)
	}
	script := &Script{Actions: []Action{{Name: "large", Type: "POST", Target: "/", BodyFile: "oversized.bin"}}}

	err = ResolveBodyRefs(script, dir)
	if err == nil || !strings.Contains(err.Error(), "exceeds 16777216 byte limit") {
		t.Fatalf("oversized body error = %v, want bounded rejection", err)
	}
}

func TestReadConfinedBodyRefEnforcesAggregateBudgetBeforeRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "payload.txt"), []byte("12345"), 0o600); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	remaining := int64(4)
	if _, err := readConfinedBodyRef(dir, "payload.txt", &remaining); err == nil || !strings.Contains(err.Error(), "aggregate limit") {
		t.Fatalf("aggregate budget error = %v", err)
	}
}

func TestResolveBodyRefsRejectsBodyAndBodyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "payload.txt"), []byte("external"), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	script := &Script{Actions: []Action{{
		Name:     "both",
		Type:     "POST",
		Target:   "/",
		Body:     "inline",
		BodyFile: "payload.txt",
	}}}

	err := ResolveBodyRefs(script, dir)
	if err == nil {
		t.Fatal("expected body and body_file error")
	}
	if !strings.Contains(err.Error(), "body and body_file cannot both be set") {
		t.Fatalf("error = %q, want both-set message", err.Error())
	}
}

func TestResolveBodyRefsResolvesSequenceChildBodyFile(t *testing.T) {
	dir := t.TempDir()
	body := "{\"step\":\"checkout\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "child.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	tests := []struct {
		name     string
		requests any
		child    func(any) map[string]any
	}{
		{
			name: "requests as []any",
			requests: []any{
				map[string]any{
					"name": "create", "type": "POST", "method": "POST", "target": "/orders",
					"body_file": "child.json",
				},
			},
			child: func(requests any) map[string]any {
				return requests.([]any)[0].(map[string]any)
			},
		},
		{
			name: "requests as []map[string]any",
			requests: []map[string]any{
				{
					"name": "create", "type": "POST", "method": "POST", "target": "/orders",
					"body_file": "child.json",
				},
			},
			child: func(requests any) map[string]any {
				return requests.([]map[string]any)[0]
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			script := &Script{Actions: []Action{{
				Name:   "seq",
				Type:   "http_sequence",
				Params: map[string]any{"requests": tc.requests},
			}}}

			if err := ResolveBodyRefs(script, dir); err != nil {
				t.Fatalf("ResolveBodyRefs: %v", err)
			}
			child := tc.child(script.Actions[0].Params["requests"])
			if got, _ := child["body"].(string); got != body {
				t.Fatalf("child body = %q, want %q", got, body)
			}
			if _, present := child["body_file"]; present {
				t.Fatalf("child body_file = %v, want removed", child["body_file"])
			}
		})
	}
}

func TestResolveBodyRefsSequenceChildLeavesUntouchedWithoutBodyFile(t *testing.T) {
	dir := t.TempDir()
	script := &Script{Actions: []Action{{
		Name: "seq",
		Type: "http_sequence",
		Params: map[string]any{"requests": []any{
			map[string]any{"name": "get", "type": "GET", "method": "GET", "target": "/"},
		}},
	}}}

	if err := ResolveBodyRefs(script, dir); err != nil {
		t.Fatalf("ResolveBodyRefs: %v", err)
	}
	child := script.Actions[0].Params["requests"].([]any)[0].(map[string]any)
	if _, present := child["body"]; present {
		t.Fatalf("child body should remain absent, got %v", child["body"])
	}
}

func TestResolveBodyRefsRejectsSequenceChildBodyAndBodyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "child.json"), []byte("external"), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	script := &Script{Actions: []Action{{
		Name: "seq",
		Type: "http_sequence",
		Params: map[string]any{"requests": []any{
			map[string]any{
				"name": "both", "type": "POST", "method": "POST", "target": "/",
				"body": "inline", "body_file": "child.json",
			},
		}},
	}}}

	err := ResolveBodyRefs(script, dir)
	if err == nil {
		t.Fatal("expected body and body_file error for sequence child")
	}
	if !strings.Contains(err.Error(), "body and body_file cannot both be set") {
		t.Fatalf("error = %q, want both-set message", err.Error())
	}
}

func TestResolveBodyRefsRejectsSequenceChildEscapingBaseDir(t *testing.T) {
	dir := t.TempDir()
	parentPayload := filepath.Join(filepath.Dir(dir), "outside-child.txt")
	if err := os.WriteFile(parentPayload, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	script := &Script{Actions: []Action{{
		Name: "seq",
		Type: "http_sequence",
		Params: map[string]any{"requests": []any{
			map[string]any{
				"name": "escape", "type": "POST", "method": "POST", "target": "/",
				"body_file": filepath.Join("..", filepath.Base(parentPayload)),
			},
		}},
	}}}

	err := ResolveBodyRefs(script, dir)
	if err == nil {
		t.Fatal("expected escaping body_file error for sequence child")
	}
	if !strings.Contains(err.Error(), "escapes script directory") {
		t.Fatalf("error = %q, want escape message", err.Error())
	}
}

func TestResolveBodyRefsRecursesIntoControlBlocks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "payload.xml"), []byte("<Envelope>\n  <Body/>\n</Envelope>\n"), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	script := &Script{Actions: []Action{{
		Name: "branch",
		Type: "control_if",
		Control: &ControlFlow{
			Condition: &Condition{Left: "yes", Op: "eq", Right: "yes"},
			Steps: []Action{{
				Name:     "soap",
				Type:     "POST",
				Target:   "/soap",
				BodyFile: "payload.xml",
			}},
		},
	}}}

	if err := ResolveBodyRefs(script, dir); err != nil {
		t.Fatalf("ResolveBodyRefs: %v", err)
	}
	if got := script.Actions[0].Control.Steps[0].Body; !strings.Contains(got, "<Envelope>") {
		t.Fatalf("nested body = %q, want XML payload", got)
	}
	if script.Actions[0].Control.Steps[0].BodyFile != "" {
		t.Fatalf("nested body_file = %q, want cleared", script.Actions[0].Control.Steps[0].BodyFile)
	}
}
