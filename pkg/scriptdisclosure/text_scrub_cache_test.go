package scriptdisclosure

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

func TestProjectionTextScrubCacheRetainsCredentialChecks(t *testing.T) {
	value := strings.Repeat("ordinary source line\n", 100) + " password=cache-secret-canary"
	p := &projector{}
	first := p.projectString(value, projectionContext{opaque: true})
	if strings.Contains(first, "cache-secret-canary") || !strings.Contains(first, credential.Redacted) {
		t.Fatal("scrubbed source disclosed a credential")
	}
	if len(p.textScrubCache) != 1 || p.projectString(value, projectionContext{opaque: true}) != first {
		t.Fatal("repeated source text did not reuse an identical scrub result")
	}
	if got := p.projectString(value, projectionContext{key: "password"}); got != credential.Redacted {
		t.Fatal("cached free text bypassed sensitive field classification")
	}
	p.knownSecrets = []string{"ordinary source line"}
	p.configureKnownMatching(len(value))
	if got := p.projectString(value, projectionContext{opaque: true}); strings.Contains(got, "ordinary source line") {
		t.Fatal("cached text bypassed known-credential equivalence")
	}
	if got := (&projector{}).projectString(value, projectionContext{opaque: true}); got != first {
		t.Fatal("cache escaped the lifetime of its projection")
	}
}

func TestProjectionTextScrubCacheBounds(t *testing.T) {
	p := &projector{}
	if got := p.scrubText("ordinary short text"); got != "ordinary short text" || len(p.textScrubCache) != 1 {
		t.Fatal("short text was not cached")
	}
	if got := p.scrubText("token=short-secret-canary"); got != scrubWithTemplates("token=short-secret-canary", false) || strings.Contains(got, "short-secret-canary") {
		t.Fatal("cached short text differs from a fresh scrub")
	}
	for i := 0; i < maxCachedScrubTexts+10; i++ {
		p.scrubText("x" + strconv.Itoa(i))
	}
	if len(p.textScrubCache) != maxCachedScrubTexts || p.textScrubCacheBytes > maxCachedScrubTextBytes {
		t.Fatal("entry or byte cache bound exceeded")
	}
	p = &projector{textScrubCacheBytes: maxCachedScrubTextBytes - 1}
	p.scrubText(strings.Repeat("x ", 600))
	if len(p.textScrubCache) != 0 {
		t.Fatal("text was retained past the byte budget")
	}
}

func TestTemplateProtectionRepeatedReferencesAndCollisions(t *testing.T) {
	value := "apex~template~0~safe apex~template~0~safe~ " +
		strings.Repeat(`{"ordinary":"{{param.row}}","password":"secret-canary"} `, 2000)
	protected, replacements := protectTemplateReferences(value)
	if len(replacements) != 2000 || portableTemplatePattern.MatchString(protected) {
		t.Fatal("template spans were not all protected")
	}
	if got := restoreTextPlaceholders(protected, replacements); got != value {
		t.Fatal("placeholder restoration changed source formatting or collided with source text")
	}
	got := scrubWithTemplateProjector(value, false, nil)
	if strings.Contains(got, "secret-canary") || strings.Count(got, "{{param.row}}") != 2000 ||
		!strings.HasPrefix(got, "apex~template~0~safe apex~template~0~safe~ ") {
		t.Fatal("template protection damaged references or disclosed credentials")
	}
}

func BenchmarkProjectionRepeatedSourceText(b *testing.B) {
	value := strings.Repeat(`source {"ordinary":"value","password":"benchmark-canary"}`+"\n", 1200)
	b.SetBytes(int64(len(value) * 10))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p := &projector{}
		for repeat := 0; repeat < 10; repeat++ {
			p.projectString(value, projectionContext{opaque: true})
		}
	}
}

func BenchmarkTemplateProtectionManyReferences(b *testing.B) {
	value := strings.Repeat("source {{param.row}} {{secret.binding}}\n", 4000)
	b.SetBytes(int64(len(value)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		protected, replacements := protectTemplateReferences(value)
		if restoreTextPlaceholders(protected, replacements) != value {
			b.Fatal("template restoration changed source")
		}
	}
}
