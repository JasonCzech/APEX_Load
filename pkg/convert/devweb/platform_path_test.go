package devweb

import "testing"

func TestResolveDevWebHelperModuleUsesLogicalSeparators(t *testing.T) {
	t.Parallel()
	helpers := []string{"checkout/helpers/auth.js"}
	for _, source := range []string{"checkout/main.js", `checkout\main.js`} {
		for _, required := range []string{"./helpers/auth", `.\helpers\auth`} {
			if got := resolveDevWebHelperModule(source, required, helpers); got != helpers[0] {
				t.Fatalf("resolveDevWebHelperModule(%q, %q) = %q", source, required, got)
			}
		}
	}
}
