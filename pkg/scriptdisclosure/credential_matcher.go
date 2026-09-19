package scriptdisclosure

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
)

// credentialMatcher uses failure links to scan all credential literals together.
// Work is proportional to the text rather than text bytes × credential count.
// Longest suffix matches suffice for long credentials; short credentials retain
// the existing whole-value and locator-proven token-boundary rules.
type credentialMatcher struct {
	nodes []credentialMatchNode
	exact map[string]struct{}
}

type credentialMatchNode struct {
	next    map[byte]int
	fail    int
	longest int
	short   []string
}

const maxCredentialMatcherNodes = 250_000

func newCredentialMatcher(secrets []string, forced map[string]struct{}) *credentialMatcher {
	m := &credentialMatcher{nodes: []credentialMatchNode{{}}, exact: make(map[string]struct{})}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		m.exact[secret] = struct{}{}
		if strings.TrimSpace(secret) == "" {
			continue
		}
		if len(secret) < 4 {
			if _, ok := forced[secret]; !ok {
				continue
			}
		}
		state := 0
		for i := 0; i < len(secret); i++ {
			next, ok := m.nodes[state].next[secret[i]]
			if !ok {
				if len(m.nodes) >= maxCredentialMatcherNodes {
					return nil
				}
				next = len(m.nodes)
				m.nodes = append(m.nodes, credentialMatchNode{})
				if m.nodes[state].next == nil {
					m.nodes[state].next = make(map[byte]int)
				}
				m.nodes[state].next[secret[i]] = next
			}
			state = next
		}
		if len(secret) >= 4 {
			m.nodes[state].longest = len(secret)
		} else {
			m.nodes[state].short = []string{secret}
		}
	}
	queue := make([]int, 0, len(m.nodes))
	for _, next := range m.nodes[0].next {
		queue = append(queue, next)
	}
	work := maxKnownCredentialComparisonWork
	for head := 0; head < len(queue); head++ {
		state := queue[head]
		for ch, next := range m.nodes[state].next {
			failure := m.nodes[state].fail
			for failure != 0 && m.nodes[failure].next[ch] == 0 {
				work--
				if work <= 0 {
					return nil
				}
				failure = m.nodes[failure].fail
			}
			fallback := m.nodes[failure].next[ch]
			m.nodes[next].fail = fallback
			if m.nodes[fallback].longest > m.nodes[next].longest {
				m.nodes[next].longest = m.nodes[fallback].longest
			}
			m.nodes[next].short = append(m.nodes[next].short, m.nodes[fallback].short...)
			queue = append(queue, next)
		}
	}
	return m
}

func (m *credentialMatcher) replace(value string) string {
	if _, ok := m.exact[value]; ok {
		return credential.Redacted
	}
	type span struct{ start, end int }
	var spans []span
	state := 0
	for i := 0; i < len(value); i++ {
		for state != 0 && m.nodes[state].next[value[i]] == 0 {
			state = m.nodes[state].fail
		}
		state = m.nodes[state].next[value[i]]
		length := m.nodes[state].longest
		for _, secret := range m.nodes[state].short {
			start, end := i+1-len(secret), i+1
			bounded := credentialTokenBoundary(value, start, end)
			if !containsCredentialTokenRune(secret) {
				bounded = credentialPunctuationBoundary(value, secret, start, end)
			}
			if bounded && len(secret) > length {
				length = len(secret)
			}
		}
		if length == 0 {
			continue
		}
		match := span{i + 1 - length, i + 1}
		// A later match can overlap multiple earlier matches. Merge the complete
		// union so overlapping secrets never leave a literal suffix exposed.
		for len(spans) > 0 && spans[len(spans)-1].end >= match.start {
			prior := spans[len(spans)-1]
			if prior.start < match.start {
				match.start = prior.start
			}
			spans = spans[:len(spans)-1]
		}
		spans = append(spans, match)
	}
	if len(spans) == 0 {
		return value
	}
	var out strings.Builder
	from := 0
	for _, match := range spans {
		out.WriteString(value[from:match.start])
		out.WriteString(credential.Redacted)
		from = match.end
	}
	out.WriteString(value[from:])
	return out.String()
}
