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
