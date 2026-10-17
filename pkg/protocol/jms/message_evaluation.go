package jms

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/jsonextract"
)

var extractorRegexCache = boundedcache.NewStringCache[*regexp.Regexp](0, 0, 0)

func compileExtractorRegex(expr string) (*regexp.Regexp, error) {
	if cached, ok := extractorRegexCache.Load(expr); ok {
		return cached, nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	actual, _ := extractorRegexCache.LoadOrStore(expr, re)
	return actual, nil
}

func runExtractors(extractors []protocol.Extractor, result *protocol.Result, msg message) error {
	for _, extractor := range extractors {
		value, err := extract(extractor, msg)
		if err != nil {
			switch strings.ToLower(strings.TrimSpace(extractor.OnMiss)) {
			case "", "ignore":
				continue
			case "empty":
				value = ""
			default:
				return err
			}
		}
		if err := protocol.StoreExtraction(result, extractor.Name, value); err != nil {
			result.Extractions = nil
			return fmt.Errorf("JMS extractor %q output rejected: %w", extractor.Name, err)
		}
	}
	return nil
}

func extract(ext protocol.Extractor, msg message) (string, error) {
	switch ext.Source {
	case "body":
		switch ext.Type {
		case "regex":
			re, err := compileExtractorRegex(ext.Expression)
			if err != nil {
				return "", fmt.Errorf("invalid regex: %w", err)
			}
			matches := re.FindSubmatch(msg.Body)
			if len(matches) == 0 {
				return "", fmt.Errorf("no match for regex %q", ext.Expression)
			}
			if len(matches) == 1 {
				return string(matches[0]), nil
			}
			return string(matches[1]), nil
		case "jsonpath":
			return extractJSONPath(ext.Expression, msg.Body)
		default:
			return "", fmt.Errorf("unsupported JMS body extraction type: %s", ext.Type)
		}
	case "header":
		return lookupString(ext.Expression, msg.Headers, "header")
	case "property":
		return lookupString(ext.Expression, msg.Properties, "property")
	case "message_id":
		return msg.ID, nil
	case "correlation_id":
		return msg.CorrelationID, nil
	default:
		return "", fmt.Errorf("unsupported JMS extraction source: %s", ext.Source)
	}
}

func evaluateAssertions(assertions []protocol.Assertion, result *protocol.Result, msg message) error {
	for _, assertion := range assertions {
		switch assertion.Type {
		case "message_received":
			if msg.ID == "" {
				return fmt.Errorf("expected a JMS message")
			}
		case "body_contains":
			if !strings.Contains(string(msg.Body), assertion.Value) {
				return fmt.Errorf("JMS message body does not contain %q", assertion.Value)
			}
		case "property_equals":
			if msg.Properties[assertion.Value] != fmt.Sprint(assertion.Expected) {
				return fmt.Errorf("JMS property %q = %q, want %q", assertion.Value, msg.Properties[assertion.Value], assertion.Expected)
			}
		case "header_equals":
			if msg.Headers[assertion.Value] != fmt.Sprint(assertion.Expected) {
				return fmt.Errorf("JMS header %q = %q, want %q", assertion.Value, msg.Headers[assertion.Value], assertion.Expected)
			}
		case "correlation_match":
			if msg.CorrelationID != assertion.Value {
				return fmt.Errorf("JMS correlation_id = %q, want %q", msg.CorrelationID, assertion.Value)
			}
		case "response_time_ms":
			if assertion.Max > 0 && result.Duration.Milliseconds() > int64(assertion.Max) {
				return fmt.Errorf("JMS response time %dms exceeded max %dms", result.Duration.Milliseconds(), assertion.Max)
			}
		default:
			return fmt.Errorf("unsupported JMS assertion type: %s", assertion.Type)
		}
	}
	return nil
}

func extractJSONPath(expr string, body []byte) (string, error) {
	return jsonextract.MessagingJSONPath(expr, body)
}
