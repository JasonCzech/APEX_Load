package scriptdisclosure

const (
	minCachedScrubTextBytes = 1024
	maxCachedScrubTexts     = 256
	maxCachedScrubTextBytes = 8 << 20
)

// scrubText reuses only the context-independent built-in scrub result. Import
// review metadata repeats source files and snippets at several evidence paths.
// The cache belongs to one projection, is bounded by both bytes and entries,
// and never changes the subsequent field-aware or known-credential checks.
func (p *projector) scrubText(value string) string {
	if len(value) < minCachedScrubTextBytes {
		return scrubText(value)
	}
	if projected, ok := p.textScrubCache[value]; ok {
		return projected
	}
	projected := scrubText(value)
	bytes := len(value) + len(projected)
	if len(p.textScrubCache) < maxCachedScrubTexts &&
		bytes <= maxCachedScrubTextBytes-p.textScrubCacheBytes {
		if p.textScrubCache == nil {
			p.textScrubCache = make(map[string]string)
		}
		p.textScrubCache[value] = projected
		p.textScrubCacheBytes += bytes
	}
	return projected
}
