package enterprisearchive

import (
	"strings"
	"testing"
)

func TestCollectSourceStringConstants(t *testing.T) {
	files := map[string]string{
		"custom_body.h": `#ifndef _CUSTOM_BODY_H
#define _CUSTOM_BODY_H

// Step Name : step_1
const char * const body_variable_1 = "Body=message=%7B%22id%22%3A%221%3Ba%22%7D";

// Step Name : step_2
const char * const body_variable_2 = "Body=first-fragment&"
	"second-fragment=with%20esc\"quote\"&"
	"third-fragment";

const char *plain_ptr = "Body=plain";
const int not_a_string = 42;
const char * const broken = concat("a", "b");
#endif`,
		"notes.txt": `const char * const ignored_in_txt = "Body=nope";`,
	}

	constants := collectSourceStringConstants(files)

	if got := constants["body_variable_1"]; got != `"Body=message=%7B%22id%22%3A%221%3Ba%22%7D"` {
		t.Errorf("body_variable_1 = %q", got)
	}
	want2 := `"Body=first-fragment&second-fragment=with%20esc\"quote\"&third-fragment"`
	if got := constants["body_variable_2"]; got != want2 {
		t.Errorf("body_variable_2 = %q, want %q", got, want2)
	}
	if got := constants["plain_ptr"]; got != `"Body=plain"` {
		t.Errorf("plain_ptr = %q", got)
	}
	if _, ok := constants["not_a_string"]; ok {
		t.Error("non-string constant should be skipped")
	}
	if _, ok := constants["broken"]; ok {
		t.Error("non-literal initializer should be skipped")
	}
	if _, ok := constants["ignored_in_txt"]; ok {
		t.Error("non-source files should be skipped")
	}
}

func TestInlineSourceStringConstants(t *testing.T) {
	constants := map[string]string{
		"body_variable_2": `"Body=message=%7B%22actions%22%3A%5B%5D%7D"`,
	}
	content := `	web_custom_request("step_9",
		"URL=https://example.test/api?r=1",
		"Method=POST",
		"EncType=application/x-www-form-urlencoded;charset=UTF-8",
		body_variable_2,
		LAST);

	// body_variable_2 mentioned in a comment stays untouched as a non-argument
	int body_variable_2_count = 0;
`

	inlined := inlineSourceStringConstants(content, constants)

	if !strings.Contains(inlined, `"Body=message=%7B%22actions%22%3A%5B%5D%7D",`) {
		t.Errorf("constant argument was not inlined:\n%s", inlined)
	}
	if strings.Contains(inlined, "\n\t\tbody_variable_2,") {
		t.Error("identifier argument should have been replaced")
	}
	if !strings.Contains(inlined, "body_variable_2_count") {
		t.Error("longer identifiers sharing the prefix must not be rewritten")
	}
}

func TestInlineSourceStringConstantsDollarSafety(t *testing.T) {
	constants := map[string]string{
		"body_with_dollar": `"Body=price=$10&tpl=${name}"`,
	}
	content := `web_custom_request("s", "Method=POST", body_with_dollar, LAST);`
	inlined := inlineSourceStringConstants(content, constants)
	if !strings.Contains(inlined, `"Body=price=$10&tpl=${name}"`) {
		t.Errorf("dollar signs must survive replacement:\n%s", inlined)
	}
}

func TestParseActionFileWithInlinedBodyConstant(t *testing.T) {
	files := map[string]string{
		"lrw_custom_body.h": `const char * const body_variable_1 = "Body=message=%7B%22a%22%3A1%7D";`,
	}
	action := `Action()
{
	web_custom_request("step_1",
		"URL=https://example.test/api",
		"Method=POST",
		body_variable_1,
		LAST);
	return 0;
}`

	constants := collectSourceStringConstants(files)
	parsed, err := ParseActionFile("Action.c", inlineSourceStringConstants(action, constants))
	if err != nil {
		t.Fatalf("ParseActionFile: %v", err)
	}
	var found bool
	for _, fn := range parsed.Functions {
		if fn.Name == "web_custom_request" {
			found = true
			if fn.Body != "message=%7B%22a%22%3A1%7D" {
				t.Errorf("body = %q, want inlined constant payload", fn.Body)
			}
		}
	}
	if !found {
		t.Fatal("web_custom_request not parsed")
	}
}

func TestInlineSourceStringConstantsNeverRewritesInsideLiterals(t *testing.T) {
	constants := map[string]string{"body_1": `"Body=secret"`}
	content := `	web_url("step", "URL=https://x/api?cb=foo(body_1)", LAST);`
	inlined := inlineSourceStringConstants(content, constants)
	if inlined != content {
		t.Errorf("constant names inside string literals must stay untouched:\n%s", inlined)
	}
}

func TestInlineSourceStringConstantsAdjacentSameNameArgs(t *testing.T) {
	constants := map[string]string{"c1": `"Body=v"`}
	inlined := inlineSourceStringConstants(`some_call("x", c1, c1, LAST);`, constants)
	want := `some_call("x", "Body=v", "Body=v", LAST);`
	if inlined != want {
		t.Errorf("both adjacent same-name arguments must inline:\n got %s\nwant %s", inlined, want)
	}
}

func TestInlineSourceStringConstantsNoCrossConstantResubstitution(t *testing.T) {
	// A literal whose text mentions another constant in argument punctuation
	// must not be rewritten a second time.
	constants := map[string]string{
		"outer": `"Body=call(inner,"`,
		"inner": `"Body=oops"`,
	}
	inlined := inlineSourceStringConstants(`f("a", outer, LAST);`, constants)
	want := `f("a", "Body=call(inner,", LAST);`
	if inlined != want {
		t.Errorf("injected literals must not be re-scanned:\n got %s\nwant %s", inlined, want)
	}
}

func TestInlineSourceStringConstantsSkipsComments(t *testing.T) {
	constants := map[string]string{"body_1": `"Body=secret"`}
	content := "/* f(body_1, x) */\n// g(body_1)\nf(body_1, LAST);"
	inlined := inlineSourceStringConstants(content, constants)
	want := "/* f(body_1, x) */\n// g(body_1)\nf(\"Body=secret\", LAST);"
	if inlined != want {
		t.Errorf("comments must stay untouched:\n got %s\nwant %s", inlined, want)
	}
}

func TestInlineSourceStringConstantsNotPartOfLongerIdentifier(t *testing.T) {
	constants := map[string]string{"c1": `"Body=v"`}
	inlined := inlineSourceStringConstants(`f(c1x, x_c1, c1, LAST);`, constants)
	want := `f(c1x, x_c1, "Body=v", LAST);`
	if inlined != want {
		t.Errorf("only standalone identifiers inline:\n got %s\nwant %s", inlined, want)
	}
}
