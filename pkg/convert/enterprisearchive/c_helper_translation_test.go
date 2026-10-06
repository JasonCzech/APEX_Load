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
