package dsl

import (
	"fmt"
	"strings"
	"time"
)

// FormatLoadRunnerTime renders the bounded numeric and English-name strftime
// subset used by imported date/time parameters. Literals are copied as bytes,
// so .000 and Z never acquire Go layout or timezone semantics. Locale-dependent
// compound forms and unknown directives are rejected rather than guessed.
func FormatLoadRunnerTime(t time.Time, pattern string) (string, error) {
	if len(pattern) == 0 || len(pattern) > 256 {
		return "", fmt.Errorf("unsupported LoadRunner date format")
	}
	layouts := map[byte]string{'Y': "2006", 'y': "06", 'm': "01", 'd': "02", 'e': "_2", 'H': "15", 'I': "03", 'M': "04", 'S': "05", 'b': "Jan", 'B': "January", 'a': "Mon", 'A': "Monday", 'p': "PM", 'z': "-0700", 'Z': "MST"}
	var out strings.Builder
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '%' {
			out.WriteByte(pattern[i])
			continue
		}
		i++
		if i >= len(pattern) {
			return "", fmt.Errorf("unsupported LoadRunner date format")
		}
		if pattern[i] == '%' {
			out.WriteByte('%')
			continue
		}
		layout, ok := layouts[pattern[i]]
		if !ok {
			return "", fmt.Errorf("unsupported LoadRunner date format")
		}
		out.WriteString(t.Format(layout))
	}
	return out.String(), nil
}
