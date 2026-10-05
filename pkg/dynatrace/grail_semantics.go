package dynatrace

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var grailDefaultParameter = regexp.MustCompile(`(?i)\bdefault\s*:`)
var grailRateParameter = regexp.MustCompile(`(?i)\brate\s*:`)

// Numeric definitions expose one declared statistic. Keep that statistic tied
// to the final projected aggregate rather than trusting an administrator label
// after arbitrary arithmetic or reassignment of the value field.
func grailNumericSemantics(q GrailQuery) (synthetic bool, err error) {
	parts, err := splitPipeline(q.Query)
	if err != nil {
		return false, err
	}
	found := false
	for _, part := range parts {
		part = strings.TrimSpace(part)
		command := strings.Fields(part)[0]
		arguments := strings.TrimSpace(part[len(command):])
		command = strings.ToLower(command)
		aggregate := command == "timeseries" || command == "summarize" || command == "maketimeseries"
		if aggregate && grailDefaultParameter.MatchString(dqlOutsideStrings(arguments)) {
			synthetic = true
		}
		if !aggregate && command != "fields" && command != "fieldsadd" {
			continue
		}
		for _, expression := range splitDQLArguments(arguments) {
			field, value, assigned := strings.Cut(expression, "=")
			if !assigned || strings.TrimSpace(field) != q.ValueField {
				continue
			}
			if !aggregate {
				return false, errors.New("numeric Grail value cannot be reassigned after aggregation")
			}
			value = strings.TrimSpace(value)
			open := strings.IndexByte(value, '(')
			if open < 1 || !strings.HasSuffix(value, ")") {
				return false, errors.New("numeric Grail value requires one explicit aggregate")
			}
			function := strings.ToLower(strings.TrimSpace(value[:open]))
			arguments := value[open+1 : len(value)-1]
			// A trailing arithmetic expression is not part of one function call.
			if !oneDQLFunction(value[open:]) {
				return false, errors.New("numeric Grail aggregate arithmetic is unsupported")
			}
			statistic := function
			switch function {
			case "avg":
				statistic = "average"
				if q.Statistic == "time_average" {
					statistic = q.Statistic
				}
			case "count", "countif":
				statistic = "sum"
				if command == "timeseries" {
					// Metrics count is series cardinality, not request count.
					statistic = "value"
				}
			case "percentile":
				parameters := splitDQLArguments(arguments)
				if len(parameters) < 2 {
					return false, errors.New("numeric Grail percentile requires a literal rank")
				}
				rank, parseErr := strconv.ParseFloat(strings.TrimSpace(parameters[1]), 64)
				if parseErr != nil || rank != 95 && rank != 99 {
					return false, errors.New("numeric Grail percentile requires rank 95 or 99")
				}
				statistic = "p" + strconv.FormatFloat(rank, 'f', -1, 64)
			case "sum", "min", "max":
			default:
				return false, errors.New("numeric Grail aggregate is outside the supported statistic subset")
			}
			if statistic != q.Statistic {
				return false, errors.New("numeric Grail aggregation differs from declared statistic")
			}
			// The provider cannot distinguish filled slots from observations.
			// Conservatively exclude all values from this query from acceptance.
			if grailRateParameter.MatchString(dqlOutsideStrings(arguments)) {
				return false, errors.New("numeric Grail rate normalization requires a separate rate statistic")
			}
			found = true
		}
	}
	if !found {
		return false, errors.New("numeric Grail definition must explicitly assign its value field to an aggregate")
	}
	return synthetic, nil
}

// Retain offsets and punctuation while hiding quoted contents from semantic
// checks. The enclosing governed-query parser has already checked balance.
func dqlOutsideStrings(value string) string {
	var out strings.Builder
	quote := byte(0)
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if quote != 0 {
			if ch == '\\' && i+1 < len(value) {
				out.WriteString("  ")
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			out.WriteByte(' ')
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			out.WriteByte(' ')
			continue
		}
		out.WriteByte(ch)
	}
	return out.String()
}

func splitDQLArguments(value string) []string {
	masked := dqlOutsideStrings(value)
	parts, start, depth := []string{}, 0, 0
	for i := 0; i < len(masked); i++ {
		switch masked[i] {
		case '(', '{':
			depth++
		case ')', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(value[start:i]))
				start = i + 1
			}
		}
	}
	return append(parts, strings.TrimSpace(value[start:]))
}

func oneDQLFunction(value string) bool {
	masked := dqlOutsideStrings(value)
	depth := 0
	for i := 0; i < len(masked); i++ {
		if masked[i] == '(' {
			depth++
		} else if masked[i] == ')' {
			depth--
			if depth == 0 {
				return strings.TrimSpace(masked[i+1:]) == ""
			}
		}
	}
	return false
}

// Grouping expressions are part of series identity. Admit plain field names
// only, so an omitted/ambiguous expression can never merge distinct tuples.
func grailGroupingFields(q GrailQuery) ([]string, error) {
	parts, err := splitPipeline(q.Query)
	if err != nil {
		return nil, err
	}
	fields := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		command := strings.Fields(part)[0]
		arguments := strings.TrimSpace(part[len(command):])
		switch strings.ToLower(command) {
		case "timeseries", "summarize", "maketimeseries":
			// Each aggregate establishes its own output grouping.
			fields = nil
		default:
			if strings.EqualFold(command, "fields") || strings.EqualFold(command, "fieldsadd") {
				for _, expression := range splitDQLArguments(arguments) {
					field, _, assigned := strings.Cut(expression, "=")
					if assigned && (containsIdentity(fields, strings.TrimSpace(field)) || strings.TrimSpace(field) == q.EntityIDField && q.EntityIDField != "") {
						return nil, errors.New("numeric Grail grouping fields cannot be reassigned after aggregation")
					}
				}
			}
			continue
		}
		for _, argument := range splitDQLArguments(arguments) {
			name, expression, parameter := strings.Cut(argument, ":")
			if !parameter || !strings.EqualFold(strings.TrimSpace(name), "by") {
				continue
			}
			expression = strings.TrimSpace(expression)
			if strings.HasPrefix(expression, "{") && strings.HasSuffix(expression, "}") {
				expression = strings.TrimSpace(expression[1 : len(expression)-1])
			}
			for _, field := range splitDQLArguments(expression) {
				if !safeField.MatchString(field) || field == q.ValueField || field == "timeframe" || field == "interval" {
					return nil, errors.New("numeric Grail grouping requires explicit primitive fields")
				}
				fields = appendUnique(fields, field)
			}
		}
	}
	if q.EntityIDField != "" {
		fields = appendUnique(fields, q.EntityIDField)
	}
	if len(fields) > 16 {
		return nil, errors.New("numeric Grail grouping exceeds dimension limit")
	}
	return fields, nil
}
