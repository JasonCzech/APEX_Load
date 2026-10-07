package appdynamics

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

var analyticsField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)
var opaqueRunID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func validAnalyticsField(field string) bool {
	return len(field) <= 128 && analyticsField.MatchString(field)
}

// CompileAnalyticsQuery inserts the administrator-selected field predicate
// before trailing clauses. Runtime values cannot become ADQL syntax, and an
// existing OR expression is enclosed so every row must satisfy the run filter.
func CompileAnalyticsQuery(q AnalyticsQuery, runID string, runHeader bool) (string, error) {
	if !validAnalyticsQuery(q.Query) {
		return "", errors.New("invalid read-only Analytics query")
	}
	if !numericID.MatchString(q.ApplicationID) || (q.ApplicationIDField == "") == (q.ApplicationName == "") {
		return "", errors.New("Analytics query requires approved application scope")
	}
	predicate := "application = " + strconv.Quote(q.ApplicationName)
	if q.ApplicationIDField != "" {
		if !validAnalyticsField(q.ApplicationIDField) {
			return "", errors.New("invalid Analytics application filter")
		}
		predicate = q.ApplicationIDField + " = " + q.ApplicationID
	}
	if runHeader && q.RunIDField != "" {
		if !validAnalyticsField(q.RunIDField) || !opaqueRunID.MatchString(runID) {
			return "", errors.New("invalid Analytics run filter")
		}
		predicate += " AND " + q.RunIDField + " = " + strconv.Quote(runID)
	}
	words, err := analyticsTopLevelWords(q.Query)
	if err != nil {
		return "", err
	}
	from, where, suffix := -1, -1, len(q.Query)
	for _, word := range words {
		switch word.value {
		case "FROM":
			if from >= 0 {
				return "", errors.New("multiple Analytics FROM clauses")
			}
			from = word.at
		case "WHERE":
			if where >= 0 || from < 0 {
				return "", errors.New("invalid Analytics WHERE clause")
			}
			where = word.at
		case "SINCE", "UNTIL", "HAVING", "GROUP", "ORDER", "ORDERBY", "LIMIT":
			if from >= 0 && word.at < suffix {
				suffix = word.at
			}
		}
	}
	if from < 0 || where >= suffix {
		return "", errors.New("invalid Analytics run-filter query")
	}
	prefix := strings.TrimSpace(q.Query[:suffix])
	if where >= 0 {
		expression := strings.TrimSpace(q.Query[where+len("WHERE") : suffix])
		if expression == "" {
			return "", errors.New("empty Analytics WHERE expression")
		}
		prefix = strings.TrimSpace(q.Query[:where]) + " WHERE (" + expression + ") AND " + predicate
	} else {
		prefix += " WHERE " + predicate
	}
	if suffix < len(q.Query) {
		prefix += " " + strings.TrimSpace(q.Query[suffix:])
	}
	if len(prefix) > 16<<10 {
		return "", errors.New("Analytics runtime query exceeds bound")
	}
	return prefix, nil
}

func (c *Client) validateAnalyticsApplication(ctx context.Context, q AnalyticsQuery) error {
	if q.ApplicationName == "" {
		return nil
	}
	cache, _ := ctx.Value(analyticsApplicationCacheKey{}).(*analyticsApplicationCache)
	key := q.ApplicationID + "\x00" + q.ApplicationName
	if cache != nil {
		cache.mu.Lock()
		verified := cache.verified[key]
		cache.mu.Unlock()
		if verified {
			return nil
		}
	}
	applications, err := c.Discover(ctx, q.ApplicationID, "applications", "")
	if err != nil {
		return err
	}
	for _, app := range applications {
		if app.ID == q.ApplicationID && app.Name == q.ApplicationName {
			if cache != nil {
				cache.mu.Lock()
				cache.verified[key] = true
				cache.mu.Unlock()
			}
			return nil
		}
	}
	return errors.New("Analytics application name does not match the selected Controller identity")
}

type analyticsApplicationCacheKey struct{}
type analyticsApplicationCache struct {
	mu       sync.Mutex
	verified map[string]bool
}

func withAnalyticsApplicationCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, analyticsApplicationCacheKey{}, &analyticsApplicationCache{verified: map[string]bool{}})
}

type analyticsWord struct {
	value string
	at    int
}

func analyticsTopLevelWords(query string) ([]analyticsWord, error) {
	var words []analyticsWord
	var quote byte
	depth := 0
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if quote != 0 {
			if ch == '\\' {
				i++
			} else if ch == quote {
				if i+1 < len(query) && query[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, errors.New("invalid Analytics expression")
			}
		default:
			if depth == 0 && (unicode.IsLetter(rune(ch)) || ch == '_') {
				start := i
				for i+1 < len(query) && (unicode.IsLetter(rune(query[i+1])) || unicode.IsDigit(rune(query[i+1])) || query[i+1] == '_') {
					i++
				}
				words = append(words, analyticsWord{value: strings.ToUpper(query[start : i+1]), at: start})
			}
		}
	}
	if quote != 0 || depth != 0 {
		return nil, errors.New("unbalanced Analytics expression")
	}
	return words, nil
}
