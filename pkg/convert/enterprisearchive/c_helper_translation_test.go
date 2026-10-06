package enterprisearchive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type targetRecorder struct{ targets []string }

func (p *targetRecorder) Name() string                               { return "http" }
func (p *targetRecorder) Init(context.Context, map[string]any) error { return nil }
func (p *targetRecorder) Teardown(context.Context) error             { return nil }
func (p *targetRecorder) HealthCheck(context.Context) error          { return nil }
func (p *targetRecorder) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.targets = append(p.targets, action.Target)
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}

const formatHelperSource = `Action()
{
	lr_save_string("(501) 218-4574", "ctn");
	formatCTN(lr_eval_string("{ctn}"));
	web_url("use", "URL=https://example.com/lines/{FormattedCTN}/{DashedCTN}", LAST);
	return 0;
}

void formatCTN(char* str)
{
	// Copy the three digit groups out of "(NNN) NNN-NNNN"
	char area[4]="\0", exchange[4]="\0", line[5]="\0", formatted[11]="\0";
	strncpy(area, str+1, 3);
	strncpy(exchange, str+6, 3);
	strncpy(line, str+10, 4);
	sprintf(formatted, "%s%s%s", area, exchange, line);
	lr_save_string(formatted, "FormattedCTN");
	strcpy(formatted, area);
	strcat(formatted, "-");
	strcat(formatted, exchange);
	lr_save_string(formatted, "DashedCTN");
}
`

func importHelperArchive(t *testing.T, action string) *ConvertResult {
	t.Helper()
	result, err := ConvertZip(zipFilesForTest(t, map[string]string{
		"Fmt/Fmt.usr":      "[General]\nType=Multi\nActiveTypes=QTWeb\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n",
		"Fmt/vuser_init.c": "vuser_init()\n{\n\treturn 0;\n}\n",
		"Fmt/vuser_end.c":  "vuser_end()\n{\n\treturn 0;\n}\n",
		"Fmt/Action.c":     action,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	return result
}

// A helper called with an argument that slices and rebuilds a string with
// strncpy, sprintf, strcpy and strcat runs as LoadRunner parameter operations.
func TestHelperWithArgumentsRunsItsStringOperations(t *testing.T) {
	result := importHelperArchive(t, formatHelperSource)
	if n := countType(result.Script.Actions, "custom_call"); n != 0 {
		t.Fatalf("custom_call actions = %d, warnings = %v, want the helper translated", n, result.Warnings)
	}
	script := result.Script
	script.Config.RunLogic.Iterations = 1
	recorder := &targetRecorder{}
	vu := &engine.VUser{ID: 1, Group: "g", Proto: recorder, Variables: map[string]string{}, Metrics: engine.NewMetricsCollector(64), Script: script}
	vu.Run(context.Background())
	if len(recorder.targets) != 1 || !strings.HasSuffix(recorder.targets[0], "/lines/5012184574/501-218") {
		t.Fatalf("targets = %v, want the reformatted numbers", recorder.targets)
	}
}

func TestHelperWithUnsupportedBodyStaysForReview(t *testing.T) {
	result := importHelperArchive(t, `Action()
{
	countChars(lr_eval_string("{ctn}"));
	return 0;
}

void countChars(char* str)
{
	int i;
	for (i = 0; str[i]; i++) {
		lr_output_message("%c", str[i]);
	}
}
`)
	if countType(result.Script.Actions, "custom_call") != 1 {
		t.Fatalf("actions = %#v, want the call kept for review", result.Script.Actions)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "countChars(...) was not inlined because") {
		t.Fatalf("warnings = %#v, want the reason the helper was not translated", result.Warnings)
	}
}

func TestHelperCallingANonLoadRunnerFunctionStaysForReview(t *testing.T) {
	result := importHelperArchive(t, `Action()
{
	toText(lr_eval_string("{count}"));
	return 0;
}

void toText(char* str)
{
	char buffer[16]="\0";
	itoa(atoi(str), buffer, 10);
	lr_save_string(buffer, "CountText");
}
`)
	if countType(result.Script.Actions, "custom_call") != 1 || !strings.Contains(strings.Join(result.Warnings, "\n"), "calls itoa(), which has no LoadRunner equivalent") {
		t.Fatalf("actions = %#v warnings = %#v, want the write into a local refused", result.Script.Actions, result.Warnings)
	}
}

func TestHelperWithoutArgumentsTranslatesItsStringOperations(t *testing.T) {
	result := importHelperArchive(t, `Action()
{
	lr_save_string("AB-1234", "code");
	splitCode();
	web_url("use", "URL=https://example.com/{CodeNumber}", LAST);
	return 0;
}

void splitCode()
{
	char number[5]="\0";
	strncpy(number, lr_eval_string("{code}")+3, 4);
	lr_save_string(number, "CodeNumber");
}
`)
	script := result.Script
	script.Config.RunLogic.Iterations = 1
	recorder := &targetRecorder{}
	vu := &engine.VUser{ID: 1, Group: "g", Proto: recorder, Variables: map[string]string{}, Metrics: engine.NewMetricsCollector(64), Script: script}
	vu.Run(context.Background())
	if len(recorder.targets) != 1 || !strings.HasSuffix(recorder.targets[0], "/1234") {
		t.Fatalf("targets = %v, warnings = %v, want the sliced code", recorder.targets, result.Warnings)
	}
}

// A script that appends lines to a load-generator file replays each line as
// an output message instead.
func TestFileOutputReplaysAsOutputMessages(t *testing.T) {
	result := importHelperArchive(t, `Action()
{
	long handle;
	handle = fopen("C:\\temp\\users.txt", "a+");
	fprintf(handle, "%s %s \n", lr_eval_string("{user}"), lr_eval_string("{pin}"));
	fclose(handle);
	return 0;
}
`)
	var message *dslActionView
	for _, action := range result.Script.Actions {
		if action.Type == "custom_call" {
			t.Fatalf("custom_call %q, want fprintf/fclose mapped", action.Name)
		}
		if action.Type == "source_message" {
			message = &dslActionView{format: action.Params["format"], args: action.Params["args"], stream: action.Params["output_stream"]}
		}
	}
	if message == nil || message.format != "%s %s" || message.stream != "handle" {
		t.Fatalf("message = %+v, want the fprintf format with its stream", message)
	}
	if args, _ := message.args.([]any); len(args) != 2 || args[0] != "{{var.user}}" {
		t.Fatalf("args = %#v, want the two values", message.args)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "APEX Load does not write generator files") {
		t.Fatalf("warnings = %#v, want the file-output note", result.Warnings)
	}
}

type dslActionView struct {
	format, args, stream any
}

// Spaces at the edges of a built string survive translation and replay.
func TestHelperStringOperationsKeepEdgeSpaces(t *testing.T) {
	result := importHelperArchive(t, `Action()
{
	joinName(lr_eval_string("{first}"));
	web_url("use", "URL=https://example.com/{FullName}", LAST);
	return 0;
}

void joinName(char* first)
{
	char full[32]="\0";
	strcpy(full, first);
	strcat(full, " ");
	strcat(full, "Smith");
	sprintf(full, "%s ", full);
	lr_save_string(full, "FullName");
}
`)
	script := result.Script
	script.Config.RunLogic.Iterations = 1
	recorder := &targetRecorder{}
	vu := &engine.VUser{ID: 1, Group: "g", Proto: recorder, Variables: map[string]string{"first": "Ann"}, Metrics: engine.NewMetricsCollector(64), Script: script}
	vu.Run(context.Background())
	if len(recorder.targets) != 1 || !strings.HasSuffix(recorder.targets[0], "/Ann Smith ") {
		t.Fatalf("targets = %q, warnings = %v, want the spaces kept", recorder.targets, result.Warnings)
	}
}

// Character locals start empty on every call, an empty or blank literal saves
// under its name, and spaces inside literals survive translation.
func TestHelperLocalsResetEachCallAndLiteralsKeepTheirSpaces(t *testing.T) {
	result := importHelperArchive(t, `Action()
{
	build("a");
	web_url("one", "URL=https://example.com/{R}", LAST);
	build("b");
	web_url("two", "URL=https://example.com/{R}", LAST);
	lr_save_string(" ", "sep");
	web_url("three", "URL=https://example.com/x{sep}y", LAST);
	return 0;
}

void build(char* str)
{
	char out[64] = "\0";
	strcat(out, str);
	strcat(out, "-x");
	lr_save_string(out, "R");
	web_custom_request("post", "URL=https://example.com/post", "Method=POST", "Body=two  spaces", LAST);
}
`)
	script := result.Script
	script.Config.RunLogic.Iterations = 1
	recorder := &targetRecorder{}
	vu := &engine.VUser{ID: 1, Group: "g", Proto: recorder, Variables: map[string]string{}, Metrics: engine.NewMetricsCollector(64), Script: script}
	vu.Run(context.Background())
	var gets []string
	for _, target := range recorder.targets {
		if !strings.HasSuffix(target, "/post") {
			gets = append(gets, target)
		}
	}
	if strings.Join(gets, " ") != "/a-x /b-x /x y" {
		t.Fatalf("targets = %q, want fresh locals per call and the blank separator saved", recorder.targets)
	}
	var bodies []string
	for _, action := range script.Actions {
		if action.Type == "POST" {
			bodies = append(bodies, action.Body)
		}
	}
	if len(bodies) != 2 || bodies[0] != "two  spaces" {
		t.Fatalf("bodies = %q, want the literal's double space kept", bodies)
	}
}
