package engine

import (
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const defaultDateTimeLayout = "2006-01-02 15:04:05"
const defaultTimeLayout = "15:04:05"

type ParameterRuntime struct {
	params   map[string]dsl.Parameter
	order    []string
	states   map[string]*parameterState
	provider *DataProvider
	vuserID  int
	now      func() time.Time
}

type parameterState struct {
	hasCached  bool
	cached     string
	nextIndex  int
	used       map[int]bool
	counter    int
	seeded     bool
	random     *rand.Rand
	values     []string
	loaded     bool
	lastIndex  int
	hasRowPick bool
}

func NewParameterRuntime(parameters []dsl.Parameter, provider *DataProvider, vuserID int) *ParameterRuntime {
	if len(parameters) == 0 {
		return nil
	}

	runtime := &ParameterRuntime{
		params:   make(map[string]dsl.Parameter, len(parameters)),
		order:    make([]string, 0, len(parameters)),
		states:   make(map[string]*parameterState, len(parameters)),
		provider: provider,
		vuserID:  vuserID,
		now:      time.Now,
	}
	for i, param := range parameters {
		runtime.params[param.Name] = param
		runtime.order = append(runtime.order, param.Name)
		seed := time.Now().UnixNano() + int64((vuserID+1)*(i+1))*7919
		if param.Seed != nil {
			seed = *param.Seed
		}
		runtime.states[param.Name] = &parameterState{
			used:   make(map[int]bool),
			random: rand.New(rand.NewSource(seed)),
		}
	}
	return runtime
}

func (pr *ParameterRuntime) Prime() {
	if pr == nil {
		return
	}
	for _, name := range pr.order {
		param := pr.params[name]
		if effectiveParameterUpdate(param) == "each_occurrence" || hasSameLineReference(param) {
			continue
		}
		pr.refresh(name)
	}
	for _, name := range pr.order {
		param := pr.params[name]
		if effectiveParameterUpdate(param) == "each_occurrence" {
			continue
		}
		if hasSameLineReference(param) {
			pr.refresh(name)
		}
	}
}

func (pr *ParameterRuntime) RefreshIteration() {
	if pr == nil {
		return
	}
	for _, name := range pr.order {
		param := pr.params[name]
		if effectiveParameterUpdate(param) == "each_iteration" && !hasSameLineReference(param) {
			pr.refresh(name)
		}
	}
	for _, name := range pr.order {
		param := pr.params[name]
		if effectiveParameterUpdate(param) == "each_iteration" && hasSameLineReference(param) {
			pr.refresh(name)
		}
	}
}

func (pr *ParameterRuntime) ResolveNamed(name string) (string, bool) {
	if pr == nil {
		return "", false
	}
	paramName := name
	param, ok := pr.params[paramName]
	if !ok {
		for candidate, candidateParam := range pr.params {
			if strings.EqualFold(candidate, name) {
				paramName = candidate
				param = candidateParam
				ok = true
				break
			}
		}
		if !ok {
			return "", false
		}
	}
	state := pr.states[paramName]
	if effectiveParameterUpdate(param) == "each_occurrence" {
		return pr.generate(param, state, nil)
	}
	if !state.hasCached {
		pr.refresh(paramName)
	}
	if !state.hasCached {
		return "", false
	}
	return state.cached, true
}

func effectiveParameterUpdate(param dsl.Parameter) string {
	if param.Update != "" {
		return param.Update
	}
	return "each_iteration"
}

func effectiveParameterSelection(param dsl.Parameter) string {
	if param.Selection != "" {
		return param.Selection
	}
	return "sequential"
}

func effectiveOnExhaustion(param dsl.Parameter) string {
	if param.OnExhaustion != "" {
		return param.OnExhaustion
	}
	return "wrap"
}

func hasSameLineReference(param dsl.Parameter) bool {
	return strings.TrimSpace(param.SameLineAs) != ""
}

func (pr *ParameterRuntime) refresh(name string) {
	pr.refreshWithStack(name, nil)
}

func (pr *ParameterRuntime) refreshWithStack(name string, visiting map[string]bool) {
	param := pr.params[name]
	state := pr.states[name]
	value, ok := pr.generate(param, state, visiting)
	if !ok {
		state.cached = ""
		state.hasCached = false
		state.hasRowPick = false
		return
	}
	state.cached = value
	state.hasCached = true
}

func (pr *ParameterRuntime) generate(param dsl.Parameter, state *parameterState, visiting map[string]bool) (string, bool) {
	switch dsl.CanonicalParameterType(param.Type) {
	case "file", "list":
		if hasSameLineReference(param) {
			return pr.generateSameLineValue(param, state, visiting)
		}
		values, ok := pr.valuesForParameter(param, state)
		if !ok || len(values) == 0 {
			return "", false
		}
		value, index, ok := generateSelectedValue(values, param, state)
		if ok && index >= 0 {
			state.lastIndex = index
			state.hasRowPick = true
		} else if ok {
			state.hasRowPick = false
		}
		return value, ok
	case "random_number":
		return formatNumericValue(state.random.Intn(param.Max-param.Min+1)+param.Min, param), true
	case "date_time":
		now, ok := resolveParameterTime(pr.now, param)
		if !ok {
			return "", false
		}
		layout := param.Format
		if layout == "" {
			layout = defaultDateTimeLayout
		}
		return now.Format(layout), true
	case "time":
		now, ok := resolveParameterTime(pr.now, param)
		if !ok {
			return "", false
		}
		layout := param.Format
		if layout == "" {
			layout = defaultTimeLayout
		}
		return now.Format(layout), true
	case "timestamp":
		now, ok := resolveParameterTime(pr.now, param)
		if !ok {
			return "", false
		}
		switch param.Format {
		case "", "unix":
			return strconv.FormatInt(now.Unix(), 10), true
		case "unix_ms":
			return strconv.FormatInt(now.UnixMilli(), 10), true
		case "iso8601", "rfc3339":
			return now.Format(time.RFC3339), true
		case "rfc3339_nano":
			return now.Format(time.RFC3339Nano), true
		default:
			return now.Format(param.Format), true
		}
	case "counter":
		return generateCounterValue(param, state)
	case "current_iteration":
		iterationParam := param
		if iterationParam.Start == 0 {
			iterationParam.Start = 1
		}
		if iterationParam.Step == 0 {
			iterationParam.Step = 1
		}
		return generateCounterValue(iterationParam, state)
	case "host":
		return generateHostValue(param)
	case "uuid":
		return uuid.NewString(), true
	case "random_string":
		return generateRandomString(state.random, param.Length, param.Charset), true
	default:
		return "", false
	}
}

func (pr *ParameterRuntime) generateSameLineValue(param dsl.Parameter, state *parameterState, visiting map[string]bool) (string, bool) {
	referenceName, _, referenceState, ok := pr.findParameter(param.SameLineAs)
	if !ok || referenceState == state {
		return "", false
	}
	if !referenceState.hasRowPick {
		if visiting == nil {
			visiting = make(map[string]bool)
		}
		if visiting[referenceName] {
			return "", false
		}
		visiting[referenceName] = true
		pr.refreshWithStack(referenceName, visiting)
		delete(visiting, referenceName)
	}
	if !referenceState.hasRowPick {
		return "", false
	}
	values, ok := pr.valuesForParameter(param, state)
	if !ok || len(values) == 0 {
		return "", false
	}
	if referenceState.lastIndex < 0 || referenceState.lastIndex >= len(values) {
		return "", false
	}
	state.lastIndex = referenceState.lastIndex
	state.hasRowPick = true
	return values[referenceState.lastIndex], true
}

func (pr *ParameterRuntime) findParameter(name string) (string, dsl.Parameter, *parameterState, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", dsl.Parameter{}, nil, false
	}
	if param, ok := pr.params[name]; ok {
		return name, param, pr.states[name], true
	}
	for candidate, param := range pr.params {
		if strings.EqualFold(candidate, name) {
			return candidate, param, pr.states[candidate], true
		}
	}
	return "", dsl.Parameter{}, nil, false
}

func generateCounterValue(param dsl.Parameter, state *parameterState) (string, bool) {
	if !state.seeded {
		state.counter = param.Start
		state.seeded = true
	}
	current := state.counter
	if isCounterExhausted(current, param) {
		handled, ok := handleCounterExhaustion(param, state)
		if !ok {
			return "", false
		}
		if handled == "" && effectiveOnExhaustion(param) == "empty" {
			return "", true
		}
		current = state.counter
	}
	state.counter += param.Step
	return formatNumericValue(current, param), true
}

func generateHostValue(param dsl.Parameter) (string, bool) {
	for _, value := range param.Values {
		if strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	hostname, err := os.Hostname()
	if err == nil && strings.TrimSpace(hostname) != "" {
		return hostname, true
	}
	return "localhost", true
}

func resolveParameterTime(now func() time.Time, param dsl.Parameter) (time.Time, bool) {
	current := now()
	if param.Timezone != "" {
		loc, err := time.LoadLocation(param.Timezone)
		if err != nil {
			return time.Time{}, false
		}
		current = current.In(loc)
	}
	if param.OffsetSeconds != 0 {
		current = current.Add(time.Duration(param.OffsetSeconds) * time.Second)
	}
	return current, true
}

func generateSelectedValue(values []string, param dsl.Parameter, state *parameterState) (string, int, bool) {
	if len(values) == 0 {
		return "", -1, false
	}
	switch effectiveParameterSelection(param) {
	case "random":
		index := state.random.Intn(len(values))
		return values[index], index, true
	case "unique":
		if len(state.used) >= len(values) {
			switch effectiveOnExhaustion(param) {
			case "wrap":
				state.used = make(map[int]bool)
				state.nextIndex = 0
			case "empty":
				return "", -1, true
			default:
				return "", -1, false
			}
		}
		for attempts := 0; attempts < len(values); attempts++ {
			idx := (state.nextIndex + attempts) % len(values)
			if state.used[idx] {
				continue
			}
			state.used[idx] = true
			state.nextIndex = idx + 1
			return values[idx], idx, true
		}
		return "", -1, false
	default:
		if state.nextIndex >= len(values) {
			switch effectiveOnExhaustion(param) {
			case "wrap":
				state.nextIndex = 0
			case "empty":
				return "", -1, true
			default:
				return "", -1, false
			}
		}
		index := state.nextIndex
		value := values[index]
		state.nextIndex++
		return value, index, true
	}
}

func handleCounterExhaustion(param dsl.Parameter, state *parameterState) (string, bool) {
	switch effectiveOnExhaustion(param) {
	case "wrap":
		state.counter = param.Start
		return "wrapped", true
	case "empty":
		return "", true
	default:
		return "", false
	}
}

func isCounterExhausted(current int, param dsl.Parameter) bool {
	if param.End == 0 {
		return false
	}
	if param.Step > 0 {
		return current > param.End
	}
	return current < param.End
}

func formatNumericValue(value int, param dsl.Parameter) string {
	formatted := strconv.Itoa(value)
	if param.PadWidth <= 0 {
		return formatted
	}
	padChar := "0"
	if param.PadChar != "" {
		padChar = string(param.PadChar[0])
	}
	if len(formatted) >= param.PadWidth {
		return formatted
	}
	return strings.Repeat(padChar, param.PadWidth-len(formatted)) + formatted
}

func generateRandomString(generator *rand.Rand, length int, charset string) string {
	letters := charset
	if letters == "" {
		letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	}
	b := make([]byte, length)
	for i := range b {
		b[i] = letters[generator.Intn(len(letters))]
	}
	return string(b)
}

func (pr *ParameterRuntime) valuesForParameter(param dsl.Parameter, state *parameterState) ([]string, bool) {
	if state.loaded {
		return state.values, len(state.values) > 0
	}
	if len(param.Values) > 0 {
		state.values = normalizeParameterValues(param.Values)
		state.loaded = true
		return state.values, true
	}
	if param.DataSource == "" || param.Column == "" || pr.provider == nil {
		return nil, false
	}
	values, ok := pr.provider.GetColumnValues(param.DataSource, param.Column)
	if !ok {
		return nil, false
	}
	state.values = values
	state.loaded = true
	return state.values, true
}

func normalizeParameterValues(values []string) []string {
	normalized := make([]string, len(values))
	for index, value := range values {
		normalized[index] = strings.TrimSpace(value)
	}
	return normalized
}

func resolveBuiltinFunction(expr string, now func() time.Time) (string, bool) {
	switch expr {
	case "fn.uuid()", "fn.uuid", "fn.uuid.v4":
		return uuid.NewString(), true
	case "fake.uuid", "fake.uuid()":
		return uuid.NewString(), true
	case "fake.email", "fake.email()":
		generator := rand.New(rand.NewSource(time.Now().UnixNano()))
		localPart := strings.ToLower(generateRandomString(generator, 12, "abcdefghijklmnopqrstuvwxyz0123456789"))
		return localPart + "@example.test", true
	case "fn.timestamp.unix":
		return strconv.FormatInt(now().Unix(), 10), true
	case "fn.timestamp.unix_ms":
		return strconv.FormatInt(now().UnixMilli(), 10), true
	case "fn.timestamp.iso8601", "fn.timestamp.rfc3339":
		return now().Format(time.RFC3339), true
	}

	if strings.HasPrefix(expr, "fn.random(") && strings.HasSuffix(expr, ")") {
		args := splitFunctionArgs(expr[len("fn.random(") : len(expr)-1])
		if len(args) != 2 {
			return "", false
		}
		min, err := strconv.Atoi(args[0])
		if err != nil {
			return "", false
		}
		max, err := strconv.Atoi(args[1])
		if err != nil || min > max {
			return "", false
		}
		generator := rand.New(rand.NewSource(time.Now().UnixNano()))
		return strconv.Itoa(generator.Intn(max-min+1) + min), true
	}

	if strings.HasPrefix(expr, "fn.random_string(") && strings.HasSuffix(expr, ")") {
		args := splitFunctionArgs(expr[len("fn.random_string(") : len(expr)-1])
		if len(args) != 1 {
			return "", false
		}
		length, err := strconv.Atoi(args[0])
		if err != nil || length <= 0 {
			return "", false
		}
		generator := rand.New(rand.NewSource(time.Now().UnixNano()))
		return generateRandomString(generator, length, ""), true
	}

	return "", false
}

func splitFunctionArgs(raw string) []string {
	parts := strings.Split(raw, ",")
	args := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(strings.Trim(part, `"'`))
		if trimmed == "" {
			continue
		}
		args = append(args, trimmed)
	}
	return args
}
