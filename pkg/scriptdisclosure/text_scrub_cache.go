package scriptdisclosure

const (
	maxCachedScrubTexts     = 1 << 16
	maxCachedScrubTextBytes = 8 << 20
)

// scrubText reuses only the context-independent built-in scrub result. Import
// drafts repeat header values, labels, source files and snippets across
// thousands of actions and evidence paths, and scrubbing even a short value
// runs a dozen regexps, so every length is cached. The cache belongs to one
// projection, is bounded by both bytes and entries, and never changes the
// subsequent field-aware or known-credential checks.
func (p *projector) scrubText(value string) string {
	if value == "" {
		return value
	}
	if projected, ok := p.textScrubCache[value]; ok {
		return projected
	}
	projected := scrubWithTemplateProjector(value, false, p.scrubMemo.Value)
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
