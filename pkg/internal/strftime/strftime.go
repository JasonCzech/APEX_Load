// Package strftime formats and parses dates with the C strftime specifiers
// LoadRunner date functions use. Literal text is copied verbatim, so a format
// such as "%d.%m.%Y T1" cannot collide with Go's layout tokens.
package strftime

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// specifiers maps each supported specifier to its Go layout and the pattern
// that parses it.
var specifiers = map[byte]struct {
	layout  string
	pattern string
}{
	'Y': {"2006", `(\d{4})`},
	'y': {"06", `(\d{2})`},
	'm': {"01", `(\d{1,2})`},
	'd': {"02", `(\d{1,2})`},
	'e': {"_2", `\s?(\d{1,2})`},
	'H': {"15", `(\d{1,2})`},
	'I': {"03", `(\d{1,2})`},
	'M': {"04", `(\d{1,2})`},
	'S': {"05", `(\d{1,2})`},
	'p': {"PM", `([AaPp][Mm])`},
	'b': {"Jan", `([A-Za-z]{3})`},
	'B': {"January", `([A-Za-z]+)`},
	'a': {"Mon", `([A-Za-z]{3})`},
	'A': {"Monday", `([A-Za-z]+)`},
	'j': {"002", `(\d{3})`},
}

// Supported reports whether every specifier in format is supported.
func Supported(format string) bool {
	_, err := tokens(format)
	return err == nil
}

type token struct {
	literal string
	spec    byte
}

func tokens(format string) ([]token, error) {
	var out []token
	var literal strings.Builder
	for index := 0; index < len(format); index++ {
		if format[index] != '%' {
			literal.WriteByte(format[index])
			continue
		}
		index++
		if index >= len(format) {
			return nil, fmt.Errorf("format ends with a lone %%")
		}
		spec := format[index]
		if spec == '%' {
			literal.WriteByte('%')
			continue
		}
		if _, ok := specifiers[spec]; !ok {
			return nil, fmt.Errorf("unsupported specifier %%%c", spec)
		}
		if literal.Len() > 0 {
			out = append(out, token{literal: literal.String()})
			literal.Reset()
		}
		out = append(out, token{spec: spec})
	}
	if literal.Len() > 0 {
		out = append(out, token{literal: literal.String()})
	}
	return out, nil
}

// Format renders t with format.
func Format(t time.Time, format string) (string, error) {
	parts, err := tokens(format)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, part := range parts {
		if part.spec == 0 {
			out.WriteString(part.literal)
			continue
		}
		out.WriteString(t.Format(specifiers[part.spec].layout))
	}
	return out.String(), nil
}

// Parse reads value written with format, in loc. Fields the format does not
// name default to January 1 of year 1900 at midnight, as C's strptime leaves
// them zero.
func Parse(value, format string, loc *time.Location) (time.Time, error) {
	parts, err := tokens(format)
	if err != nil {
		return time.Time{}, err
	}
	var pattern strings.Builder
	pattern.WriteString("^")
	for _, part := range parts {
		if part.spec == 0 {
			pattern.WriteString(regexp.QuoteMeta(part.literal))
		} else {
			pattern.WriteString(specifiers[part.spec].pattern)
		}
	}
	pattern.WriteString("$")
	match := regexp.MustCompile(pattern.String()).FindStringSubmatch(value)
	if match == nil {
		return time.Time{}, fmt.Errorf("%q does not match the date format %q", value, format)
	}
	year, month, day, hour, minute, second, yearDay := 1900, time.January, 1, 0, 0, 0, -1
	pm, twelveHour := false, false
	group := 1
	for _, part := range parts {
		if part.spec == 0 {
			continue
		}
		text := match[group]
		group++
		number, _ := strconv.Atoi(text)
		switch part.spec {
		case 'Y':
			year = number
		case 'y':
			// POSIX: 69-99 are 1969-1999, 00-68 are 2000-2068.
			year = 2000 + number
			if number >= 69 {
				year = 1900 + number
			}
		case 'm':
			month = time.Month(number)
		case 'd', 'e':
			day = number
		case 'H':
			hour = number
		case 'I':
			hour, twelveHour = number, true
		case 'M':
			minute = number
		case 'S':
			second = number
		case 'p':
			pm = strings.EqualFold(text, "pm")
		case 'b', 'B':
			parsed, err := time.Parse(map[byte]string{'b': "Jan", 'B': "January"}[part.spec], strings.ToUpper(text[:1])+strings.ToLower(text[1:]))
			if err != nil {
				return time.Time{}, fmt.Errorf("%q is not a month name", text)
			}
			month = parsed.Month()
		case 'j':
			yearDay = number
		}
	}
	if twelveHour {
		hour %= 12
		if pm {
			hour += 12
		}
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || second > 60 {
		return time.Time{}, fmt.Errorf("%q is not a valid date for %q", value, format)
	}
	if loc == nil {
		loc = time.Local
	}
	if yearDay >= 0 {
		parsed := time.Date(year, time.January, yearDay, hour, minute, second, 0, loc)
		if yearDay == 0 || parsed.Year() != year {
			return time.Time{}, fmt.Errorf("%q is not a valid day of the year for %q", value, format)
		}
		return parsed, nil
	}
	parsed := time.Date(year, month, day, hour, minute, second, 0, loc)
	if parsed.Day() != day {
		return time.Time{}, fmt.Errorf("%q is not a valid date for %q", value, format)
	}
	return parsed, nil
}
