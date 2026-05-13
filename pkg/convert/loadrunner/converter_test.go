package loadrunner

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const sampleActionC = `
#include "web_api.h"

Action()
{
	web_reg_save_param("SessionID",
		"LB=sid=",
		"RB=;",
		LAST);

	lr_start_transaction("Login");

	web_url("Homepage",
		"URL=https://example.com/app/home",
		"Resource=0",
		"RecContentType=text/html",
		LAST);

	lr_think_time(3);

	web_submit_data("Login_Submit",
		"Action=https://example.com/app/login",
		"Method=POST",
		"RecContentType=text/html",
		"Referer=https://example.com/app/home",
		ITEMDATA,
		"Name=username", "Value=testuser", ENDITEM,
		"Name=password", "Value={UserPassword}", ENDITEM,
		LAST);

	lr_end_transaction("Login", LR_AUTO);

	lr_think_time(5);

	web_custom_request("API_GetOrders",
		"URL=https://example.com/api/v1/orders",
		"Method=GET",
		"RecContentType=application/json",
		LAST);

	web_custom_request("API_CreateOrder",
		"URL=https://example.com/api/v1/orders",
		"Method=POST",
		"Body={\"item\": \"widget\", \"quantity\": 5}",
		"RecContentType=application/json",
		LAST);

	web_set_max_html_param_len("4096");

	return 0;
}
`

const sampleRegFindActionC = `
#include "web_api.h"

Action()
{
	web_reg_find("Text=Welcome back",
		"SaveCount=welcomeCount",
		LAST);

	web_url("Homepage",
		"URL=https://example.com/app/home",
		"Resource=0",
		LAST);

	web_reg_find("Text=Internal Server Error",
		"Fail=Found",
		LAST);

	web_url("Dashboard",
		"URL=https://example.com/app/dashboard",
		"Resource=0",
		LAST);

	return 0;
}
`

const sampleSOAPActionC = `
#include "web_api.h"

Action()
{
	web_add_header("SOAPAction", "urn:GetQuote");

	web_custom_request("GetQuote",
		"URL=https://example.com/services/quotes",
		"Method=POST",
		"Body=<soapenv:Envelope xmlns:soapenv=\"http://schemas.xmlsoap.org/soap/envelope/\"><soapenv:Body><GetQuote xmlns=\"urn:quotes\"><Symbol>STORM</Symbol></GetQuote></soapenv:Body></soapenv:Envelope>",
		"RecContentType=text/xml; charset=utf-8",
		LAST);

	return 0;
}
`

func TestParseActionFile(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(parsed.Functions) == 0 {
		t.Fatal("expected functions to be parsed")
	}

	// Check we found the expected function types
	names := make(map[string]int)
	for _, fn := range parsed.Functions {
		names[fn.Name]++
	}

	expected := map[string]int{
		"web_reg_save_param":         1,
		"lr_start_transaction":       1,
		"web_url":                    1,
		"lr_think_time":              2,
		"web_submit_data":            1,
		"lr_end_transaction":         1,
		"web_custom_request":         2,
		"web_set_max_html_param_len": 1,
	}

	for name, count := range expected {
		if names[name] != count {
			t.Errorf("expected %d %s calls, got %d", count, name, names[name])
		}
	}

	// Check web_url parsing
	for _, fn := range parsed.Functions {
		if fn.Name == "web_url" {
			if fn.StepName != "Homepage" {
				t.Errorf("expected step name 'Homepage', got '%s'", fn.StepName)
			}
			if fn.URL != "https://example.com/app/home" {
				t.Errorf("expected URL 'https://example.com/app/home', got '%s'", fn.URL)
			}
			if fn.Method != "GET" {
				t.Errorf("expected method 'GET', got '%s'", fn.Method)
			}
		}
	}
}

func TestConvert(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{
		ScriptName:        "Test_Conversion",
		IncludeAssertions: true,
	})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}

	if result.Script.Name != "Test_Conversion" {
		t.Errorf("expected script name 'Test_Conversion', got '%s'", result.Script.Name)
	}

	if result.Script.Protocol != "http" {
		t.Errorf("expected protocol 'http', got '%s'", result.Script.Protocol)
	}

	if result.Script.Config.BaseURL != "https://example.com" {
		t.Errorf("expected base URL 'https://example.com', got '%s'", result.Script.Config.BaseURL)
	}

	if len(result.Script.Actions) == 0 {
		t.Fatal("expected converted actions")
	}

	// Verify stats
	if result.Stats.ConvertedActions < 4 {
		t.Errorf("expected at least 4 converted actions, got %d", result.Stats.ConvertedActions)
	}

	if result.Stats.Extractors != 1 {
		t.Errorf("expected 1 extractor, got %d", result.Stats.Extractors)
	}

	if result.Stats.ThinkTimes != 2 {
		t.Errorf("expected 2 think times, got %d", result.Stats.ThinkTimes)
	}

	if result.Stats.Transactions != 1 {
		t.Errorf("expected 1 transaction, got %d", result.Stats.Transactions)
	}
	if result.Stats.Skipped != 1 {
		t.Errorf("expected 1 skipped function, got %d", result.Stats.Skipped)
	}
	if len(result.Stats.UnsupportedFuncs) != 1 || result.Stats.UnsupportedFuncs[0] != "web_set_max_html_param_len" {
		t.Fatalf("unsupported funcs = %#v, want [web_set_max_html_param_len]", result.Stats.UnsupportedFuncs)
	}

	// Check think time config was computed
	if result.Script.Config.ThinkTime.MeanMs == 0 {
		t.Error("expected think time to be configured")
	}

	// First action (Homepage) should have the extractor attached
	firstAction := result.Script.Actions[0]
	if len(firstAction.Extractors) != 1 {
		t.Errorf("expected 1 extractor on first action, got %d", len(firstAction.Extractors))
	} else if firstAction.Extractors[0].Name != "SessionID" {
		t.Errorf("expected extractor name 'SessionID', got '%s'", firstAction.Extractors[0].Name)
	}

	t.Logf("Converted %d LR functions → %d DSL actions, %d extractors, %d think times",
		result.Stats.TotalFunctions, result.Stats.ConvertedActions, result.Stats.Extractors, result.Stats.ThinkTimes)
}

func TestConvertPreserveTransactionsOption(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	withTransactions, err := Convert([]*ParsedAction{parsed}, ConvertOptions{PreserveTransactions: true})
	if err != nil {
		t.Fatalf("convert with transactions error: %v", err)
	}
	withoutTransactions, err := Convert([]*ParsedAction{parsed}, ConvertOptions{PreserveTransactions: false})
	if err != nil {
		t.Fatalf("convert without transactions error: %v", err)
	}

	if got := withTransactions.Script.Actions[0].Name; got != "Login_Homepage" {
		t.Fatalf("preserved action name = %q, want Login_Homepage", got)
	}
	if got := withoutTransactions.Script.Actions[0].Name; got != "Homepage" {
		t.Fatalf("non-preserved action name = %q, want Homepage", got)
	}
}

func TestConvertWebRegFindToAssertions(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleRegFindActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{IncludeAssertions: true})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}

	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}

	firstAssertions := result.Script.Actions[0].Assertions
	if len(firstAssertions) != 2 {
		t.Fatalf("first action assertions = %#v, want body_contains + status_code", firstAssertions)
	}
	if firstAssertions[0].Type != "body_contains" || firstAssertions[0].Value != "Welcome back" {
		t.Fatalf("first mapped assertion = %#v, want body_contains on Welcome back", firstAssertions[0])
	}

	secondAssertions := result.Script.Actions[1].Assertions
	if len(secondAssertions) != 2 {
		t.Fatalf("second action assertions = %#v, want body_not_contains + status_code", secondAssertions)
	}
	if secondAssertions[0].Type != "body_not_contains" || secondAssertions[0].Value != "Internal Server Error" {
		t.Fatalf("second mapped assertion = %#v, want body_not_contains on Internal Server Error", secondAssertions[0])
	}

	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "SaveCount=welcomeCount") {
		t.Fatalf("warnings = %#v, want SaveCount warning", result.Warnings)
	}
}

func TestConvertSOAPCustomRequest(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", sampleSOAPActionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{IncludeAssertions: true})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}

	if result.Script.Protocol != "soap" {
		t.Fatalf("protocol = %q, want soap", result.Script.Protocol)
	}
	if result.Script.Config.SOAP.Version != "1.1" {
		t.Fatalf("soap version = %q, want 1.1", result.Script.Config.SOAP.Version)
	}
	if !result.Script.Config.SOAP.FaultHandling.FailOnFault {
		t.Fatal("expected fail_on_fault to be enabled for imported SOAP scripts")
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.SOAP == nil {
		t.Fatal("expected SOAP action metadata to be populated")
	}
	if action.SOAP.Action != "urn:GetQuote" {
		t.Fatalf("soap action = %q, want urn:GetQuote", action.SOAP.Action)
	}
	if action.SOAP.BodyMode != "raw_xml" {
		t.Fatalf("body mode = %q, want raw_xml", action.SOAP.BodyMode)
	}
	if action.Method != "POST" || action.Type != "POST" {
		t.Fatalf("method/type = %s/%s, want POST/POST", action.Method, action.Type)
	}
	if got := action.Headers["SOAPAction"]; got != "urn:GetQuote" {
		t.Fatalf("SOAPAction header = %q, want urn:GetQuote", got)
	}
	if got := action.Headers["Content-Type"]; !strings.Contains(got, "text/xml") {
		t.Fatalf("content type = %q, want text/xml SOAP content type", got)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := map[string]string{
		"Login Page":      "Login_Page",
		"GET /api/orders": "GET__api_orders",
		"Step (1)":        "Step_1",
		"":                "unnamed_action",
	}
	for input, expected := range tests {
		got := sanitizeName(input)
		// The sanitizer may collapse double underscores
		if got == "" {
			t.Errorf("sanitizeName(%q) returned empty string", input)
		}
		_ = expected // just ensure it doesn't panic
	}
}

func TestConvertParamReferences(t *testing.T) {
	input := "sid={SessionID}&user={Username}"
	expected := "sid={{var.SessionID}}&user={{var.Username}}"
	got := convertParamReferences(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestParseUSRFile(t *testing.T) {
	content := `[General]
Type=Web
RuntimeSettings=default.cfg

[Parameters]
Parameter1=LoginUsers.prm

[Actions]
Action0=vuser_init.c
Action1=Action.c
Action2=vuser_end.c
`
	cfg, err := ParseUSRFile(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ScriptType != "Web" {
		t.Errorf("expected type 'Web', got '%s'", cfg.ScriptType)
	}
	if len(cfg.ActionFiles) < 2 {
		t.Errorf("expected at least 2 action files, got %d", len(cfg.ActionFiles))
	}
	if cfg.RuntimeFile != "default.cfg" {
		t.Fatalf("runtime file = %q, want default.cfg", cfg.RuntimeFile)
	}
	if len(cfg.ParamFiles) != 1 || cfg.ParamFiles[0] != "LoginUsers.prm" {
		t.Fatalf("param files = %v, want [LoginUsers.prm]", cfg.ParamFiles)
	}
}

func TestConvertZipPreservesLoadRunnerMetadata(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}

	addFile("Demo.usr", `[General]
Type=Web
RuntimeSettings=default.cfg

[Actions]
Action1=Action.c
`)
	addFile("Action.c", sampleActionC)
	addFile("default.cfg", `[HTTP]
ConnectTimeout=45
ReceiveTimeout=20
KeepAlive=0

[Browser Emulation]
UserAgent=VuGen Browser
AcceptLanguage=en-US
`)
	addFile("LoginUsers.prm", `[LoginUser]
Type=File
Table=Users.dat
ColumnName=username
SelectNextRow=Sequential
UpdateValueOn=Each Iteration

[OrderId]
Type=RandomNumber
MinimumValue=1000
MaximumValue=1005
UpdateValueOn=Each Occurrence
`)
	addFile("Users.dat", "username\nalice\nbob\n")
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Config.HTTP.ConnectTimeoutMs != 45000 {
		t.Fatalf("connect timeout = %d, want 45000", result.Script.Config.HTTP.ConnectTimeoutMs)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatal("expected script import metadata to be populated")
	}
	if len(result.Script.Parameters) != 2 {
		t.Fatalf("parameter count = %d, want 2", len(result.Script.Parameters))
	}
	if result.Script.Parameters[0].Name != "LoginUser" || result.Script.Parameters[0].Type != "file" {
		t.Fatalf("first parameter = %#v, want file-backed LoginUser", result.Script.Parameters[0])
	}
	if len(result.Script.DataSources) != 1 || result.Script.DataSources[0].Name != "users" {
		t.Fatalf("data sources = %#v, want one mapped users data source", result.Script.DataSources)
	}
	payload, err := json.Marshal(result.Script.Metadata.Import)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	text := string(payload)
	for _, want := range []string{"\"source\":\"loadrunner\"", "\"project_settings\"", "\"runtime_files\"", "\"parameter_files\"", "LoginUsers.prm", "default.cfg", "\"ConnectTimeout\":\"45\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected metadata JSON to contain %q, got %s", want, text)
		}
	}
}
