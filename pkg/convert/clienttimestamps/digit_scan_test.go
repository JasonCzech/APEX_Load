package clienttimestamps

import (
	"math/rand"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// oracleDigitRun and the oracle functions below are the regexp
// implementations the byte scanner replaced, kept to prove equal output.
var oracleDigitRun = regexp.MustCompile(`[0-9]+`)

func oracleAddServerText(e *Evidence, text string) {
	if e.Server == nil {
		e.Server = map[string]bool{}
	}
	for _, run := range oracleDigitRun.FindAllString(text, -1) {
		if len(run) == 10 || len(run) == 13 {
			e.Server[run] = true
		}
	}
}

func oracleKeyBefore(prefix string) string {
	if len(prefix) > 96 {
		prefix = prefix[len(prefix)-96:]
	}
	prefix = strings.NewReplacer("%22", `"`, "%3A", ":", "%3a", ":", "%3D", "=", "%3d", "=").Replace(prefix)
	if match := keyPattern.FindStringSubmatch(prefix); match != nil {
		key := match[1]
		if index := strings.LastIndexAny(key, ".["); index >= 0 {
			key = key[index+1:]
		}
		return key
	}
	return ""
}

func oracleApply(script *dsl.Script, evidence Evidence) Report {
	report := Report{}
	if script == nil || evidence.From.IsZero() {
		return report
	}
	var found []occurrence
	scan := func(text string) {
		for _, span := range oracleDigitRun.FindAllStringIndex(text, -1) {
			number := text[span[0]:span[1]]
			if evidence.Server[number] || evidence.templateFor(number) == "" {
				continue
			}
			found = append(found, occurrence{number: number, key: oracleKeyBefore(text[:span[0]])})
		}
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if action := ref.Action; action != nil && safeBody(action) {
			scan(action.Target)
			scan(action.Body)
			for _, value := range action.BodyMultipart.MultipartTextValues() {
				scan(*value)
			}
		} else if action != nil {
			scan(action.Target)
		} else if ref.Request != nil {
			target, _ := ref.Request["target"].(string)
			scan(target)
			if safeRequestBody(ref.Request) {
				body, _ := ref.Request["body"].(string)
				scan(body)
			}
		}
		return nil
	})
	if len(found) == 0 {
		return report
	}
	values := map[string]map[string]int{}
	for _, occ := range found {
		if values[occ.key] == nil {
			values[occ.key] = map[string]int{}
		}
		values[occ.key][occ.number]++
	}
	qualifies := map[string]bool{}
	for key, numbers := range values {
		if key == "" {
			continue
		}
		fresh := len(numbers) >= 2
		for _, count := range numbers {
			fresh = fresh && count == 1
		}
		qualifies[key] = timeKeys[strings.ToLower(key)] || fresh
	}
	replace := func(text string) string {
		var out strings.Builder
		last := 0
		for _, span := range oracleDigitRun.FindAllStringIndex(text, -1) {
			number := text[span[0]:span[1]]
			if evidence.Server[number] || !qualifies[oracleKeyBefore(text[:span[0]])] {
				continue
			}
			template := evidence.templateFor(number)
			if template == "" {
				continue
			}
			out.WriteString(text[last:span[0]])
			out.WriteString(template)
			last = span[1]
			report.Replaced++
		}
		if last == 0 {
			return text
		}
		out.WriteString(text[last:])
		return out.String()
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		action := ref.Action
		if action == nil {
			if ref.Request != nil {
				if target, ok := ref.Request["target"].(string); ok {
					ref.Request["target"] = replace(target)
				}
				if body, ok := ref.Request["body"].(string); ok && safeRequestBody(ref.Request) {
					ref.Request["body"] = replace(body)
				}
			}
			return nil
		}
		action.Target = replace(action.Target)
		if safeBody(action) {
			action.Body = replace(action.Body)
			for _, value := range action.BodyMultipart.MultipartTextValues() {
				*value = replace(*value)
			}
		}
		return nil
	})
	for key, ok := range qualifies {
		if ok {
			report.Keys = append(report.Keys, key)
		} else {
			report.LeftAsRecord += len(values[key])
		}
	}
	sort.Strings(report.Keys)
	return report
}

var digitScanEdgeCases = []string{
	"", "0", "a", "00", "0a0", "a0a", "123456789", "1234567890", "12345678901", "1234567890123",
	"12345678901234", "1234567890-1234567890123", "é1234567890é", "日本1791547200000語",
	"\xff1791547200\xfe", "\xc31791547200", "1791547200\xc3", "%3A1791547200%3a1791547200000",
	"_=1791547200000&t=1791547200&x=17915472001", "１２３", "٣٤٥", "0\x000", "9" + strings.Repeat("0", 200),
}

// randomDigitText mixes digit runs (often epoch-sized and inside the test
// capture window), keys, separators, escapes and UTF-8, valid and not.
func randomDigitText(rng *rand.Rand, captured time.Time) string {
	pieces := []string{
		"_", "t", "ts", "time", "since", "order", "id", "cb", "nocache", "a.b.", "x[",
		"=", ":", `"`, `\"`, " ", "&", "?", "/", ",", "%22", "%3A", "%3a", "%3D", "%3d", "-",
		"é", "日", "\xff", "\xc3", "\x80", "０", "0", "9", "12", "123456789",
	}
	var b strings.Builder
	for n := rng.Intn(40); n > 0; n-- {
		switch rng.Intn(6) {
		case 0:
			b.WriteString(strconv.FormatInt(captured.Add(time.Duration(rng.Intn(7200)-3600)*time.Second).UnixMilli(), 10))
		case 1:
			b.WriteString(strconv.FormatInt(captured.Add(time.Duration(rng.Intn(7200)-3600)*time.Second).Unix(), 10))
		case 2:
			b.WriteString(strconv.FormatInt(rng.Int63n(1e15), 10))
		default:
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
	}
	return b.String()
}

func TestForEachDigitRunMatchesRegexp(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	captured := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	inputs := append([]string{}, digitScanEdgeCases...)
	for i := 0; i < 20000; i++ {
		inputs = append(inputs, randomDigitText(rng, captured))
	}
	for i := 0; i < 2000; i++ {
		raw := make([]byte, rng.Intn(64))
		for j := range raw {
			raw[j] = byte(rng.Intn(256))
			if rng.Intn(2) == 0 {
				raw[j] = '0' + byte(rng.Intn(10))
			}
		}
		inputs = append(inputs, string(raw))
	}
	for _, text := range inputs {
		var got [][]int
		forEachDigitRun(text, func(start, end int) { got = append(got, []int{start, end}) })
		if want := oracleDigitRun.FindAllStringIndex(text, -1); !reflect.DeepEqual(got, want) {
			t.Fatalf("digit runs of %q = %v, want %v", text, got, want)
		}
		gotEvidence, wantEvidence := Evidence{}, Evidence{}
		gotEvidence.AddServerText(text)
		oracleAddServerText(&wantEvidence, text)
		if !reflect.DeepEqual(gotEvidence, wantEvidence) {
			t.Fatalf("AddServerText(%q) = %v, want %v", text, gotEvidence.Server, wantEvidence.Server)
		}
	}
}

func TestApplyMatchesRegexpOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	captured := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	replaced, left := 0, 0
	for round := 0; round < 3000; round++ {
		var actions []dsl.Action
		var texts []string
		for n := 1 + rng.Intn(6); n > 0; n-- {
			action := dsl.Action{Type: "http", Method: "POST", Target: "/p?" + randomDigitText(rng, captured), Body: randomDigitText(rng, captured)}
			if rng.Intn(5) == 0 {
				action.BodyEncoding = "base64"
			}
			texts = append(texts, action.Target, action.Body)
			actions = append(actions, action)
		}
		if round < len(digitScanEdgeCases) {
			actions = append(actions, dsl.Action{Type: "http", Method: "GET", Target: digitScanEdgeCases[round], Body: digitScanEdgeCases[round]})
		}
		evidence := Evidence{Server: map[string]bool{}}
		if round%10 != 0 {
			evidence.Observe(captured)
			evidence.Observe(captured.Add(time.Duration(rng.Intn(600)) * time.Second))
		}
		// Some recorded numbers also appear in responses.
		for _, text := range texts {
			if rng.Intn(3) == 0 {
				forEachDigitRun(text, func(start, end int) {
					if rng.Intn(2) == 0 {
						evidence.Server[text[start:end]] = true
					}
				})
			}
		}
		got := &dsl.Script{Actions: append([]dsl.Action{}, actions...)}
		want := &dsl.Script{Actions: append([]dsl.Action{}, actions...)}
		gotReport, wantReport := Apply(got, evidence), oracleApply(want, evidence)
		if !reflect.DeepEqual(gotReport, wantReport) || !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: Apply = %+v %+v, oracle = %+v %+v", round, gotReport, got.Actions, wantReport, want.Actions)
		}
		replaced, left = replaced+gotReport.Replaced, left+gotReport.LeftAsRecord
	}
	if replaced == 0 || left == 0 {
		t.Fatalf("inputs did not exercise both outcomes: replaced %d, left %d", replaced, left)
	}
}

func TestKeyBeforeMatchesOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	captured := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 20000; i++ {
		prefix := randomDigitText(rng, captured)
		if got, want := keyBefore(prefix), oracleKeyBefore(prefix); got != want {
			t.Fatalf("keyBefore(%q) = %q, want %q", prefix, got, want)
		}
	}
}
