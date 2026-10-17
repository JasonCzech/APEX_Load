package paramize

import (
	"errors"
	"fmt"
	"regexp"
)

var ErrSuggestionWorkLimit = errors.New("parameterization suggestion work limit exceeded")

const (
	maxSuggestionRegexSearchBytes int64 = 64 << 20
	maxSuggestionRegexMatches           = 4096
)

// suggestionRegexBudget bounds both input rescans and regex result allocation
// across one suggestion class. Callers discard that class on exhaustion so an
// oversized import cannot be mistaken for a complete, clean analysis.
type suggestionRegexBudget struct {
	searchBytes int64
	matches     int
}

func (b *suggestionRegexBudget) chargeSearchBytes(size int) error {
	valueBytes := int64(size)
	if valueBytes > maxSuggestionRegexSearchBytes-b.searchBytes {
		return fmt.Errorf("%w: input scans exceed %d bytes", ErrSuggestionWorkLimit, maxSuggestionRegexSearchBytes)
	}
	b.searchBytes += valueBytes
	return nil
}

func (b *suggestionRegexBudget) findAllSubmatches(pattern *regexp.Regexp, value string) ([][]string, error) {
	if err := b.chargeSearchBytes(len(value)); err != nil {
		return nil, err
	}

	remaining := maxSuggestionRegexMatches - b.matches
	if remaining < 0 {
		return nil, fmt.Errorf("%w: regex matches exceed %d", ErrSuggestionWorkLimit, maxSuggestionRegexMatches)
	}
	matches := pattern.FindAllStringSubmatch(value, remaining+1)
	if len(matches) > remaining {
		return nil, fmt.Errorf("%w: regex matches exceed %d", ErrSuggestionWorkLimit, maxSuggestionRegexMatches)
	}
	b.matches += len(matches)
	return matches, nil
}

func (b *suggestionRegexBudget) findAllSubmatchIndexes(pattern *regexp.Regexp, value string) ([][]int, error) {
	if err := b.chargeSearchBytes(len(value)); err != nil {
		return nil, err
	}

	remaining := maxSuggestionRegexMatches - b.matches
	if remaining < 0 {
		return nil, fmt.Errorf("%w: regex matches exceed %d", ErrSuggestionWorkLimit, maxSuggestionRegexMatches)
	}
	matches := pattern.FindAllStringSubmatchIndex(value, remaining+1)
	if len(matches) > remaining {
		return nil, fmt.Errorf("%w: regex matches exceed %d", ErrSuggestionWorkLimit, maxSuggestionRegexMatches)
	}
	b.matches += len(matches)
	return matches, nil
}
