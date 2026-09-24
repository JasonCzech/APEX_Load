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
	params         map[string]dsl.Parameter
	canonicalNames map[string]string
	order          []string
	states         map[string]*parameterState
	provider       *DataProvider
	vuserID        int
	allocation     *ParameterAllocation
	sourceState    *sourceParameterState
	sourcePlan     string
	now            func() time.Time
}

type parameterState struct {
	hasCached  bool
	cached     string
	nextIndex  int
	counter    int
	seeded     bool
	random     *rand.Rand
	javaRandom *javaRandom
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
		params:         make(map[string]dsl.Parameter, len(parameters)),
		canonicalNames: make(map[string]string, len(parameters)),
		order:          make([]string, 0, len(parameters)),
		states:         make(map[string]*parameterState, len(parameters)),
		provider:       provider,
		vuserID:        vuserID,
		now:            time.Now,
	}
	for i, param := range parameters {
		runtime.params[param.Name] = param
		runtime.order = append(runtime.order, param.Name)
		normalizedName := strings.ToLower(strings.TrimSpace(param.Name))
		if _, exists := runtime.canonicalNames[normalizedName]; normalizedName != "" && !exists {
			// Valid scripts cannot declare case-insensitive duplicates. Retaining
			// the first declaration here makes direct/legacy runtime construction
			// deterministic instead of depending on Go map iteration order.
			runtime.canonicalNames[normalizedName] = param.Name
		}
		seed := time.Now().UnixNano() + int64((vuserID+1)*(i+1))*7919
		if param.Seed != nil {
			seed = *param.Seed
		}
		state := &parameterState{random: rand.New(rand.NewSource(seed))}
		if param.RandomAlgorithm == "java48" {
			state.javaRandom = newJavaRandom(seed)
		}
		runtime.states[param.Name] = state
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
		paramName, ok = pr.canonicalNames[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return "", false
		}
		param = pr.params[paramName]
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
	if param.Scope == "source_engine" {
		if pr.sourceState == nil {
			return "", false
		}
		return pr.sourceState.next(pr.sourcePlan, param)
	}
	if param.RandomAlgorithm == "java48" {
		return javaRandomValue(param, state.javaRandom)
	}
	if param.Type == "counter" && param.ScopeID != "" {
		return jmeterCounterValue(param, state)
	}
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
		value, ok := randomIntInclusive(param.Min, param.Max, state.random.Uint64)
		if !ok {
			return "", false
		}
		return formatNumericValue(value, param), true
	case "date_time":
		now, ok := resolveParameterTime(pr.now, param)
		if !ok {
			return "", false
		}
		if param.FormatDialect == "java_simple_date_format" {
			value, err := dsl.FormatJMeterTime(now, param.Format)
			return value, err == nil
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
	case "vuser_id":
		return strconv.Itoa(pr.vuserID + 1), true
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
	canonical, ok := pr.canonicalNames[strings.ToLower(name)]
	if !ok {
		return "", dsl.Parameter{}, nil, false
	}
	param := pr.params[canonical]
	return canonical, param, pr.states[canonical], true
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
	state.counter = saturatingAddInt(state.counter, param.Step)
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
		if param.OffsetSeconds < -dsl.MaxParameterOffsetSeconds || param.OffsetSeconds > dsl.MaxParameterOffsetSeconds {
			return time.Time{}, false
		}
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
		return strings.TrimSpace(values[index]), index, true
	case "unique":
		if state.nextIndex >= len(values) {
			switch effectiveOnExhaustion(param) {
			case "wrap":
				state.nextIndex = 0
			case "empty":
				return "", -1, true
			case "last":
				index := len(values) - 1
				if state.hasRowPick && state.lastIndex >= 0 && state.lastIndex < len(values) {
					index = state.lastIndex
				}
				return strings.TrimSpace(values[index]), index, true
			default:
				return "", -1, false
			}
		}
		index := state.nextIndex
		state.nextIndex++
		return strings.TrimSpace(values[index]), index, true
	default:
		if state.nextIndex >= len(values) {
			switch effectiveOnExhaustion(param) {
			case "wrap":
				state.nextIndex = 0
			case "empty":
				return "", -1, true
			case "last":
				return strings.TrimSpace(values[len(values)-1]), len(values) - 1, true
			default:
				return "", -1, false
			}
		}
		index := state.nextIndex
		value := strings.TrimSpace(values[index])
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
	case "last":
		if param.End != 0 {
			state.counter = param.End
		}
		return "last", true
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
	if param.PadWidth <= 0 || param.PadWidth > dsl.MaxGeneratedParameterBytes {
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
	return generateRandomStringWithIntn(generator.Intn, length, charset)
}

func generateRandomStringGlobal(length int, charset string) string {
	return generateRandomStringWithIntn(rand.Intn, length, charset)
}

func generateRandomStringWithIntn(intn func(int) int, length int, charset string) string {
	if length <= 0 || length > dsl.MaxGeneratedParameterBytes || len(charset) > dsl.MaxParameterCharsetBytes {
		return ""
	}
	letters := charset
	if letters == "" {
		letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	}
	b := make([]byte, length)
	for i := range b {
		b[i] = letters[intn(len(letters))]
	}
	return string(b)
}

func (pr *ParameterRuntime) valuesForParameter(param dsl.Parameter, state *parameterState) ([]string, bool) {
	if state.loaded {
		return state.values, len(state.values) > 0
	}
	if len(param.Values) > 0 {
		state.values = pr.applyParameterRowAllocation(param.Values, param)
		state.loaded = true
		return state.values, true
	}
	if param.DataSource == "" || param.Column == "" || pr.provider == nil {
		return nil, false
	}
	values, ok := pr.provider.GetColumnValues(param.DataSource, param.Column)
	if pr.allocation != nil && param.Selection == "unique" && (param.AutoAllocate || param.BlockSize > 0) {
		values, ok = allocationValues(param, pr.provider)
	}
	if !ok {
		return nil, false
	}
	state.values = pr.applyParameterRowAllocation(values, param)
	state.loaded = true
	return state.values, true
}

func (pr *ParameterRuntime) applyParameterRowAllocation(values []string, param dsl.Parameter) []string {
	if len(values) == 0 {
		return values
	}
	if param.AutoAllocate && pr.allocation == nil {
		return nil
	}
	start := 0
	if param.StartRow > 1 {
		start = param.StartRow - 1
	}
	if start >= len(values) {
		if param.Selection != "unique" && effectiveOnExhaustion(param) == "wrap" {
			start = start % len(values)
		} else {
			return nil
		}
	}
	values = values[start:]
	if len(values) == 0 {
		return values
	}
	if param.BlockSize > 0 {
		vuserID := pr.vuserID
		if pr.allocation != nil && param.Selection == "unique" {
			vuserID = pr.allocation.Offset
		}
		if vuserID < 0 {
			vuserID = 0
		}
		blockStart := 0
		if vuserID <= (len(values)-1)/param.BlockSize {
			blockStart = vuserID * param.BlockSize
		} else {
			switch effectiveOnExhaustion(param) {
			case "wrap":
				if param.Selection == "unique" {
					return nil
				}
				blockStart = safePositiveMulMod(vuserID, param.BlockSize, len(values))
			case "last":
				if param.Selection != "unique" {
					return values[len(values)-1:]
				}
				return nil
			default:
				return nil
			}
		}
		blockEnd := len(values)
		if param.BlockSize < len(values)-blockStart {
			blockEnd = blockStart + param.BlockSize
		}
		values = values[blockStart:blockEnd]
	}
	if param.ValueForEachVUser && param.BlockSize == 0 {
		index := pr.vuserID
		if index < 0 {
			index = 0
		}
		if index >= len(values) {
			switch effectiveOnExhaustion(param) {
			case "wrap":
				index = index % len(values)
			case "last":
				index = len(values) - 1
			default:
				return nil
			}
		}
		return values[index : index+1]
	}
	return values
}

func safePositiveMulMod(a, b, modulus int) int {
	if a <= 0 || b <= 0 || modulus <= 1 {
		return 0
	}
	x := uint64(a % modulus)
	y := uint64(b % modulus)
	m := uint64(modulus)
	var result uint64
	addMod := func(left, right uint64) uint64 {
		if left >= m-right {
			return left - (m - right)
		}
		return left + right
	}
	for y > 0 {
		if y&1 == 1 {
			result = addMod(result, x)
		}
		y >>= 1
		if y > 0 {
			x = addMod(x, x)
		}
	}
	return int(result)
}

func resolveBuiltinFunction(expr string, now func() time.Time, rng *rand.Rand) (string, bool) {
	switch strings.TrimSuffix(expr, "()") {
	case "fn.uuid", "fn.uuid.v4":
		return uuid.NewString(), true
	case "fake.uuid":
		return uuid.NewString(), true
	case "fake.email":
		localPart := ""
		if rng != nil {
			localPart = generateRandomString(rng, 12, "abcdefghijklmnopqrstuvwxyz0123456789")
		} else {
			localPart = generateRandomStringGlobal(12, "abcdefghijklmnopqrstuvwxyz0123456789")
		}
		localPart = strings.ToLower(localPart)
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
		next := rand.Uint64
		if rng != nil {
			next = rng.Uint64
		}
		value, ok := randomIntInclusive(min, max, next)
		if !ok {
			return "", false
		}
		return strconv.Itoa(value), true
	}

	if strings.HasPrefix(expr, "fn.random_string(") && strings.HasSuffix(expr, ")") {
		args := splitFunctionArgs(expr[len("fn.random_string(") : len(expr)-1])
		if len(args) != 1 {
			return "", false
		}
		length, err := strconv.Atoi(args[0])
		if err != nil || length <= 0 || length > dsl.MaxGeneratedParameterBytes {
			return "", false
		}
		if rng != nil {
			return generateRandomString(rng, length, ""), true
		}
		return generateRandomStringGlobal(length, ""), true
	}

	return "", false
}

func saturatingAddInt(value, delta int) int {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	if delta > 0 && value > maxInt-delta {
		return maxInt
	}
	if delta < 0 && value < minInt-delta {
		return minInt
	}
	return value + delta
}

func randomIntInclusive(minimum, maximum int, next func() uint64) (int, bool) {
	if minimum > maximum || next == nil {
		return 0, false
	}
	if strconv.IntSize == 32 {
		minBits := uint32(int32(minimum))
		span := uint64(uint32(int32(maximum))-minBits) + 1
		offset := randomUint64Below(span, next)
		return int(int32(minBits + uint32(offset))), true
	}
	minBits := uint64(int64(minimum))
	span := uint64(int64(maximum)) - minBits + 1
	offset := randomUint64Below(span, next)
	return int(int64(minBits + offset)), true
}

func randomUint64Below(span uint64, next func() uint64) uint64 {
	if span == 0 { // the full uint64 range
		return next()
	}
	threshold := -span % span
	for {
		value := next()
		if value >= threshold {
			return value % span
		}
	}
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
