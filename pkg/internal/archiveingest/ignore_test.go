package archiveingest

import "testing"

func TestJunkReasonRecognizesSystemAndVersionControlFiles(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{".DS_Store", SkipReasonSystemFile},
		{"collection/sub/.DS_Store", SkipReasonSystemFile},
		{"Thumbs.db", SkipReasonSystemFile},
		{"collection/desktop.ini", SkipReasonSystemFile},
		{"__MACOSX/collection/request.yml", SkipReasonSystemFile},
		{"collection/._request.yml", SkipReasonSystemFile},
		{".gitignore", SkipReasonVersionControl},
		{"collection/.gitattributes", SkipReasonVersionControl},
		{"collection/.git/config", SkipReasonVersionControl},
		{"collection/.svn/entries", SkipReasonVersionControl},
		{"collection/request.yml", ""},
		{"collection/.env", ""},
		{"collection/git/request.yml", ""},
		{"collection/_request.yml", ""},
	} {
		if got := JunkReason(test.path); got != test.want {
			t.Errorf("JunkReason(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

func TestIgnoreRulesFollowGitignoreBasics(t *testing.T) {
	rules := ParseIgnoreRules([]string{
		"# comment",
		"",
		"node_modules",
		"*.log",
		"/build",
		"docs/drafts/",
		"**/tmp",
		"!keep.log",
		"a/**/b",
		"[",
	})
	for _, test := range []struct {
		path string
		want bool
	}{
		{"node_modules/lib/index.js", true},
		{"api/node_modules/x.js", true},
		{"server.log", true},
		{"api/requests/server.log", true},
		{"build/out.yml", true},
		{"api/build/out.yml", false},
		{"docs/drafts/one.yml", true},
		{"docs/drafts", false},
		{"deep/tmp/file.yml", true},
		{"keep.log", true},
		{"a/x/b", false},
		{"api/request.yml", false},
		{"", false},
	} {
		if got := rules.Match(test.path); got != test.want {
			t.Errorf("Match(%q) = %t, want %t", test.path, got, test.want)
		}
	}
	if !ParseIgnoreRules([]string{"# only comments", "!negated"}).Empty() {
		t.Fatal("rules with no supported patterns are not empty")
	}
}
